package erc8004

// ERC-8004 Reputation Registry (v2.0.0) calldata builders and read helpers.
//
// IMPORTANT — signing model: the serviceoffer/servicebounty controller NEVER
// signs feedback transactions. Client agents submit giveFeedback (and
// revokeFeedback) with THEIR OWN wallets; agent operators submit
// appendResponse with theirs. This package only builds calldata and reads
// recorded feedback.
//
// Function signatures verified against:
//   - Spec: https://eips.ethereum.org/EIPS/eip-8004 (Reputation Registry)
//   - Reference impl + official ABI:
//     https://github.com/erc-8004/erc-8004-contracts
//     (abis/ReputationRegistry.json, contracts/ReputationRegistryUpgradeable.sol,
//     getVersion() == "2.0.0")
//
//	giveFeedback(uint256 agentId, int128 value, uint8 valueDecimals, string tag1, string tag2, string endpoint, string feedbackURI, bytes32 feedbackHash)
//	revokeFeedback(uint256 agentId, uint64 feedbackIndex)
//	appendResponse(uint256 agentId, address clientAddress, uint64 feedbackIndex, string responseURI, bytes32 responseHash)
//	getSummary(uint256 agentId, address[] clientAddresses, string tag1, string tag2) -> (uint64 count, int128 summaryValue, uint8 summaryValueDecimals)
//	readFeedback(uint256 agentId, address clientAddress, uint64 feedbackIndex) -> (int128, uint8, string, string, bool)
//	getLastIndex(uint256 agentId, address clientAddress) -> uint64
//	getClients(uint256 agentId) -> address[]

import (
	"context"
	_ "embed"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

//go:embed reputation_registry.abi.json
var reputationRegistryABI string

// ReputationRegistryMainnet is the ERC-8004 v2.0.0 Reputation Registry on
// Ethereum mainnet and Base mainnet (deployed at the same address via
// CREATE2). The Base Sepolia deployment is the existing
// ReputationRegistryBaseSepolia constant in abi.go.
// Source: https://github.com/erc-8004/erc-8004-contracts README +
// scripts/addresses.ts; on-chain: code present on both chains,
// getVersion() == "2.0.0".
const ReputationRegistryMainnet = "0x8004BAa17C55a88189AE136b182e5fdA19dE9b63"

// MaxFeedbackValueDecimals is the maximum valueDecimals accepted by
// giveFeedback. The contract reverts with "too many decimals" above this.
const MaxFeedbackValueDecimals = 18

// maxFeedbackAbsValue mirrors the contract's MAX_ABS_VALUE = 1e38 bound on
// the int128 feedback value.
var maxFeedbackAbsValue = new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)

var (
	reputationABIOnce   sync.Once
	reputationABIParsed abi.ABI
	reputationABIErr    error
)

// reputationABI lazily parses the embedded Reputation Registry ABI once.
func reputationABI() (abi.ABI, error) {
	reputationABIOnce.Do(func() {
		reputationABIParsed, reputationABIErr = abi.JSON(strings.NewReader(reputationRegistryABI))
	})
	if reputationABIErr != nil {
		return abi.ABI{}, fmt.Errorf("erc8004: parse reputation registry abi: %w", reputationABIErr)
	}
	return reputationABIParsed, nil
}

// ReputationRegistryAddress maps a supported network name to the deployed
// ERC-8004 v2.0.0 Reputation Registry address. It accepts the same aliases
// as ResolveNetwork. Networks without an on-chain-verified deployment return
// an error rather than a guessed address.
func ReputationRegistryAddress(network string) (string, error) {
	net, err := ResolveNetwork(network)
	if err != nil {
		return "", fmt.Errorf("erc8004: reputation registry: %w", err)
	}
	switch net.Name {
	case BaseSepolia.Name:
		return ReputationRegistryBaseSepolia, nil
	case Base.Name, Ethereum.Name:
		return ReputationRegistryMainnet, nil
	default:
		return "", fmt.Errorf("erc8004: no verified reputation registry deployment for network %q", net.Name)
	}
}

// EncodeGiveFeedback builds calldata for
// giveFeedback(uint256,int128,uint8,string,string,string,string,bytes32).
// value is a fixed-point score scaled by 10^valueDecimals (|value| <= 1e38,
// valueDecimals <= 18). The transaction must be submitted by the client
// agent's own wallet — the contract forbids self-feedback from the agent's
// owner/operators, and the controller never signs. tag1, tag2, endpoint,
// feedbackURI, and feedbackHash are optional per spec and may be zero values.
func EncodeGiveFeedback(agentID *big.Int, value *big.Int, valueDecimals uint8, tag1, tag2, endpoint, feedbackURI string, feedbackHash common.Hash) ([]byte, error) {
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("erc8004: feedback value must not be nil")
	}
	if value.CmpAbs(maxFeedbackAbsValue) > 0 {
		return nil, fmt.Errorf("erc8004: feedback value %s out of range [-1e38, 1e38]", value)
	}
	if valueDecimals > MaxFeedbackValueDecimals {
		return nil, fmt.Errorf("erc8004: valueDecimals %d out of range [0,%d]", valueDecimals, MaxFeedbackValueDecimals)
	}

	parsed, err := reputationABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("giveFeedback", agentID, value, valueDecimals, tag1, tag2, endpoint, feedbackURI, feedbackHash)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack giveFeedback: %w", err)
	}
	return data, nil
}

// EncodeRevokeFeedback builds calldata for revokeFeedback(uint256,uint64).
// Must be submitted by the wallet that gave the feedback. Feedback indices
// are 1-based.
func EncodeRevokeFeedback(agentID *big.Int, feedbackIndex uint64) ([]byte, error) {
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	if feedbackIndex == 0 {
		return nil, fmt.Errorf("erc8004: feedbackIndex must be > 0 (indices are 1-based)")
	}

	parsed, err := reputationABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("revokeFeedback", agentID, feedbackIndex)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack revokeFeedback: %w", err)
	}
	return data, nil
}

// EncodeAppendResponse builds calldata for
// appendResponse(uint256,address,uint64,string,bytes32) — an on-chain reply
// to existing feedback. Submitted by the responder's own wallet.
func EncodeAppendResponse(agentID *big.Int, clientAddress common.Address, feedbackIndex uint64, responseURI string, responseHash common.Hash) ([]byte, error) {
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	if clientAddress == (common.Address{}) {
		return nil, fmt.Errorf("erc8004: clientAddress must not be the zero address")
	}
	if feedbackIndex == 0 {
		return nil, fmt.Errorf("erc8004: feedbackIndex must be > 0 (indices are 1-based)")
	}
	if responseURI == "" {
		return nil, fmt.Errorf("erc8004: responseURI must not be empty")
	}

	parsed, err := reputationABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("appendResponse", agentID, clientAddress, feedbackIndex, responseURI, responseHash)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack appendResponse: %w", err)
	}
	return data, nil
}

// GiveFeedbackCall is the decoded argument set of a giveFeedback call.
type GiveFeedbackCall struct {
	AgentID       *big.Int
	Value         *big.Int
	ValueDecimals uint8
	Tag1          string
	Tag2          string
	Endpoint      string
	FeedbackURI   string
	FeedbackHash  common.Hash
}

// DecodeGiveFeedbackCalldata decodes giveFeedback calldata (selector +
// ABI-encoded args). Useful for provenance checks on observed transactions
// and for tests.
func DecodeGiveFeedbackCalldata(data []byte) (GiveFeedbackCall, error) {
	parsed, err := reputationABI()
	if err != nil {
		return GiveFeedbackCall{}, err
	}
	values, err := unpackCalldata(parsed, "giveFeedback", data)
	if err != nil {
		return GiveFeedbackCall{}, err
	}
	if len(values) != 8 {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: giveFeedback arg count = %d, want 8", len(values))
	}

	out := GiveFeedbackCall{}
	var ok bool
	if out.AgentID, ok = values[0].(*big.Int); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: agentId type = %T", values[0])
	}
	if out.Value, ok = values[1].(*big.Int); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: value type = %T", values[1])
	}
	if out.ValueDecimals, ok = values[2].(uint8); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: valueDecimals type = %T", values[2])
	}
	if out.Tag1, ok = values[3].(string); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: tag1 type = %T", values[3])
	}
	if out.Tag2, ok = values[4].(string); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: tag2 type = %T", values[4])
	}
	if out.Endpoint, ok = values[5].(string); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: endpoint type = %T", values[5])
	}
	if out.FeedbackURI, ok = values[6].(string); !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: feedbackURI type = %T", values[6])
	}
	hash, ok := values[7].([32]byte)
	if !ok {
		return GiveFeedbackCall{}, fmt.Errorf("erc8004: feedbackHash type = %T", values[7])
	}
	out.FeedbackHash = common.Hash(hash)
	return out, nil
}

// RevokeFeedbackCall is the decoded argument set of a revokeFeedback call.
type RevokeFeedbackCall struct {
	AgentID       *big.Int
	FeedbackIndex uint64
}

// DecodeRevokeFeedbackCalldata decodes revokeFeedback calldata.
func DecodeRevokeFeedbackCalldata(data []byte) (RevokeFeedbackCall, error) {
	parsed, err := reputationABI()
	if err != nil {
		return RevokeFeedbackCall{}, err
	}
	values, err := unpackCalldata(parsed, "revokeFeedback", data)
	if err != nil {
		return RevokeFeedbackCall{}, err
	}
	if len(values) != 2 {
		return RevokeFeedbackCall{}, fmt.Errorf("erc8004: revokeFeedback arg count = %d, want 2", len(values))
	}

	out := RevokeFeedbackCall{}
	var ok bool
	if out.AgentID, ok = values[0].(*big.Int); !ok {
		return RevokeFeedbackCall{}, fmt.Errorf("erc8004: agentId type = %T", values[0])
	}
	if out.FeedbackIndex, ok = values[1].(uint64); !ok {
		return RevokeFeedbackCall{}, fmt.Errorf("erc8004: feedbackIndex type = %T", values[1])
	}
	return out, nil
}

// AppendResponseCall is the decoded argument set of an appendResponse call.
type AppendResponseCall struct {
	AgentID       *big.Int
	ClientAddress common.Address
	FeedbackIndex uint64
	ResponseURI   string
	ResponseHash  common.Hash
}

// DecodeAppendResponseCalldata decodes appendResponse calldata.
func DecodeAppendResponseCalldata(data []byte) (AppendResponseCall, error) {
	parsed, err := reputationABI()
	if err != nil {
		return AppendResponseCall{}, err
	}
	values, err := unpackCalldata(parsed, "appendResponse", data)
	if err != nil {
		return AppendResponseCall{}, err
	}
	if len(values) != 5 {
		return AppendResponseCall{}, fmt.Errorf("erc8004: appendResponse arg count = %d, want 5", len(values))
	}

	out := AppendResponseCall{}
	var ok bool
	if out.AgentID, ok = values[0].(*big.Int); !ok {
		return AppendResponseCall{}, fmt.Errorf("erc8004: agentId type = %T", values[0])
	}
	if out.ClientAddress, ok = values[1].(common.Address); !ok {
		return AppendResponseCall{}, fmt.Errorf("erc8004: clientAddress type = %T", values[1])
	}
	if out.FeedbackIndex, ok = values[2].(uint64); !ok {
		return AppendResponseCall{}, fmt.Errorf("erc8004: feedbackIndex type = %T", values[2])
	}
	if out.ResponseURI, ok = values[3].(string); !ok {
		return AppendResponseCall{}, fmt.Errorf("erc8004: responseURI type = %T", values[3])
	}
	hash, ok := values[4].([32]byte)
	if !ok {
		return AppendResponseCall{}, fmt.Errorf("erc8004: responseHash type = %T", values[4])
	}
	out.ResponseHash = common.Hash(hash)
	return out, nil
}

// FeedbackSummary mirrors the reputation getSummary return values. The
// aggregate score is SummaryValue scaled by 10^-SummaryValueDecimals.
type FeedbackSummary struct {
	Count                uint64
	SummaryValue         *big.Int
	SummaryValueDecimals uint8
}

// FeedbackEntry mirrors readFeedback return values.
type FeedbackEntry struct {
	Value         *big.Int
	ValueDecimals uint8
	Tag1          string
	Tag2          string
	IsRevoked     bool
}

// ReputationReader provides read-only access to a Reputation Registry. The
// controller uses it to observe recorded feedback; it holds no signer.
type ReputationReader struct {
	contract *bind.BoundContract
}

// NewReputationReader binds a read-only Reputation Registry at
// registryAddress. caller is typically (*erc8004.Client).ETH() or any
// *ethclient.Client.
func NewReputationReader(caller bind.ContractCaller, registryAddress string) (*ReputationReader, error) {
	if caller == nil {
		return nil, fmt.Errorf("erc8004: reputation reader: caller must not be nil")
	}
	if !common.IsHexAddress(registryAddress) {
		return nil, fmt.Errorf("erc8004: reputation reader: invalid registry address %q", registryAddress)
	}
	parsed, err := reputationABI()
	if err != nil {
		return nil, err
	}
	return &ReputationReader{
		contract: bind.NewBoundContract(common.HexToAddress(registryAddress), parsed, caller, nil, nil),
	}, nil
}

// Summary reads getSummary(agentId, clientAddresses, tag1, tag2).
func (r *ReputationReader) Summary(ctx context.Context, agentID *big.Int, clientAddresses []common.Address, tag1, tag2 string) (FeedbackSummary, error) {
	if err := checkAgentID(agentID); err != nil {
		return FeedbackSummary{}, err
	}
	if clientAddresses == nil {
		clientAddresses = []common.Address{}
	}
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getSummary", agentID, clientAddresses, tag1, tag2); err != nil {
		return FeedbackSummary{}, fmt.Errorf("erc8004: reputation getSummary: %w", err)
	}
	if len(out) != 3 {
		return FeedbackSummary{}, fmt.Errorf("erc8004: reputation getSummary returned %d values, want 3", len(out))
	}

	summary := FeedbackSummary{}
	var ok bool
	if summary.Count, ok = out[0].(uint64); !ok {
		return FeedbackSummary{}, fmt.Errorf("erc8004: reputation getSummary count type = %T", out[0])
	}
	if summary.SummaryValue, ok = out[1].(*big.Int); !ok {
		return FeedbackSummary{}, fmt.Errorf("erc8004: reputation getSummary summaryValue type = %T", out[1])
	}
	if summary.SummaryValueDecimals, ok = out[2].(uint8); !ok {
		return FeedbackSummary{}, fmt.Errorf("erc8004: reputation getSummary summaryValueDecimals type = %T", out[2])
	}
	return summary, nil
}

// ReadFeedback reads readFeedback(agentId, clientAddress, feedbackIndex).
// Feedback indices are 1-based.
func (r *ReputationReader) ReadFeedback(ctx context.Context, agentID *big.Int, clientAddress common.Address, feedbackIndex uint64) (FeedbackEntry, error) {
	if err := checkAgentID(agentID); err != nil {
		return FeedbackEntry{}, err
	}
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "readFeedback", agentID, clientAddress, feedbackIndex); err != nil {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback: %w", err)
	}
	if len(out) != 5 {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback returned %d values, want 5", len(out))
	}

	entry := FeedbackEntry{}
	var ok bool
	if entry.Value, ok = out[0].(*big.Int); !ok {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback value type = %T", out[0])
	}
	if entry.ValueDecimals, ok = out[1].(uint8); !ok {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback valueDecimals type = %T", out[1])
	}
	if entry.Tag1, ok = out[2].(string); !ok {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback tag1 type = %T", out[2])
	}
	if entry.Tag2, ok = out[3].(string); !ok {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback tag2 type = %T", out[3])
	}
	if entry.IsRevoked, ok = out[4].(bool); !ok {
		return FeedbackEntry{}, fmt.Errorf("erc8004: readFeedback isRevoked type = %T", out[4])
	}
	return entry, nil
}

// LastIndex reads getLastIndex(agentId, clientAddress) — the most recent
// (1-based) feedback index the client has submitted for the agent; 0 when
// none.
func (r *ReputationReader) LastIndex(ctx context.Context, agentID *big.Int, clientAddress common.Address) (uint64, error) {
	if err := checkAgentID(agentID); err != nil {
		return 0, err
	}
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getLastIndex", agentID, clientAddress); err != nil {
		return 0, fmt.Errorf("erc8004: getLastIndex: %w", err)
	}
	if len(out) != 1 {
		return 0, fmt.Errorf("erc8004: getLastIndex returned %d values, want 1", len(out))
	}
	idx, ok := out[0].(uint64)
	if !ok {
		return 0, fmt.Errorf("erc8004: getLastIndex type = %T", out[0])
	}
	return idx, nil
}
