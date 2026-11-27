package erc8004

// ERC-8004 Validation Registry (v2.0.0) calldata builders and read helpers.
//
// IMPORTANT — signing model: the serviceoffer/servicebounty controller NEVER
// signs validation transactions. Poster agents submit validationRequest and
// evaluator agents submit validationResponse with THEIR OWN wallets; this
// package only builds calldata for them and reads/records results on-chain.
//
// Function signatures verified against:
//   - Spec: https://eips.ethereum.org/EIPS/eip-8004 (Validation Registry)
//   - Reference impl + official ABI:
//     https://github.com/erc-8004/erc-8004-contracts
//     (abis/ValidationRegistry.json, contracts/ValidationRegistryUpgradeable.sol,
//     getVersion() == "2.0.0")
//
//	validationRequest(address validatorAddress, uint256 agentId, string requestURI, bytes32 requestHash)
//	validationResponse(bytes32 requestHash, uint8 response, string responseURI, bytes32 responseHash, string tag)
//	getValidationStatus(bytes32 requestHash) -> (address, uint256, uint8, bytes32, string, uint256)
//	getSummary(uint256 agentId, address[] validatorAddresses, string tag) -> (uint64 count, uint8 avgResponse)
//	getAgentValidations(uint256 agentId) -> bytes32[]
//	getValidatorRequests(address validatorAddress) -> bytes32[]

import (
	"bytes"
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

//go:embed validation_registry.abi.json
var validationRegistryABI string

const (
	// ValidationRegistryV2BaseSepolia is the ERC-8004 v2.0.0 Validation
	// Registry on Base Sepolia (CREATE2 vanity proxy, same address on all
	// supported testnets).
	//
	// NOTE: this intentionally differs from the legacy
	// ValidationRegistryBaseSepolia constant in abi.go
	// (0x8004CB39f29c09145F24Ad9dDe2A108C1A2cdfC5): that address has NO code
	// on Base Sepolia — it is a v1.0.0 deployment that only exists on
	// Ethereum Sepolia (verified via eth_getCode + getVersion(), 2026-06-10).
	// Source: https://github.com/erc-8004/erc-8004-contracts
	// (scripts/addresses.ts TESTNET_ADDRESSES.validationRegistry); on-chain:
	// getVersion() == "2.0.0", getIdentityRegistry() ==
	// IdentityRegistryBaseSepolia.
	ValidationRegistryV2BaseSepolia = "0x8004Cb1BF31DAf7788923b405b754f57acEB4272"

	// ValidationRegistryV2Mainnet is the ERC-8004 v2.0.0 Validation Registry
	// on Ethereum mainnet and Base mainnet (deployed at the same address via
	// CREATE2). Source: https://github.com/erc-8004/erc-8004-contracts
	// (scripts/addresses.ts MAINNET_ADDRESSES.validationRegistry); on-chain:
	// code present on both chains, getVersion() == "2.0.0",
	// getIdentityRegistry() == IdentityRegistryMainnet.
	ValidationRegistryV2Mainnet = "0x8004Cc8439f36fd5F9F049D9fF86523Df6dAAB58"

	// MaxValidationResponse is the maximum validationResponse score. The
	// contract reverts with "resp>100" above this.
	MaxValidationResponse = 100
)

var (
	validationABIOnce   sync.Once
	validationABIParsed abi.ABI
	validationABIErr    error
)

// validationABI lazily parses the embedded Validation Registry ABI once.
func validationABI() (abi.ABI, error) {
	validationABIOnce.Do(func() {
		validationABIParsed, validationABIErr = abi.JSON(strings.NewReader(validationRegistryABI))
	})
	if validationABIErr != nil {
		return abi.ABI{}, fmt.Errorf("erc8004: parse validation registry abi: %w", validationABIErr)
	}
	return validationABIParsed, nil
}

// ValidationRegistryAddress maps a supported network name to the deployed
// ERC-8004 v2.0.0 Validation Registry address. It accepts the same aliases as
// ResolveNetwork. Networks without an on-chain-verified deployment return an
// error rather than a guessed address.
func ValidationRegistryAddress(network string) (string, error) {
	net, err := ResolveNetwork(network)
	if err != nil {
		return "", fmt.Errorf("erc8004: validation registry: %w", err)
	}
	switch net.Name {
	case BaseSepolia.Name:
		return ValidationRegistryV2BaseSepolia, nil
	case Base.Name, Ethereum.Name:
		return ValidationRegistryV2Mainnet, nil
	default:
		return "", fmt.Errorf("erc8004: no verified validation registry deployment for network %q", net.Name)
	}
}

// checkAgentID rejects agent ids that cannot be ABI-encoded as uint256.
func checkAgentID(agentID *big.Int) error {
	if agentID == nil {
		return fmt.Errorf("erc8004: agentId must not be nil")
	}
	if agentID.Sign() < 0 {
		return fmt.Errorf("erc8004: agentId must not be negative (got %s)", agentID)
	}
	if agentID.BitLen() > 256 {
		return fmt.Errorf("erc8004: agentId does not fit in uint256")
	}
	return nil
}

// unpackCalldata verifies the 4-byte selector against the named method and
// unpacks the argument payload.
func unpackCalldata(parsed abi.ABI, name string, data []byte) ([]interface{}, error) {
	method, ok := parsed.Methods[name]
	if !ok {
		return nil, fmt.Errorf("erc8004: method %q not in ABI", name)
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("erc8004: calldata too short (%d bytes, need at least 4)", len(data))
	}
	if !bytes.Equal(data[:4], method.ID) {
		return nil, fmt.Errorf("erc8004: selector mismatch: got 0x%x, want 0x%x (%s)", data[:4], method.ID, method.Sig)
	}
	values, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		return nil, fmt.Errorf("erc8004: unpack %s calldata: %w", name, err)
	}
	return values, nil
}

// EncodeValidationRequest builds calldata for
// validationRequest(address,uint256,string,bytes32). The transaction must be
// submitted by the owner or an approved operator of agentId (the poster
// agent's own wallet) — never by the controller.
func EncodeValidationRequest(validatorAddress common.Address, agentID *big.Int, requestURI string, requestHash common.Hash) ([]byte, error) {
	if validatorAddress == (common.Address{}) {
		return nil, fmt.Errorf("erc8004: validatorAddress must not be the zero address")
	}
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	if requestHash == (common.Hash{}) {
		return nil, fmt.Errorf("erc8004: requestHash must not be the zero hash")
	}

	parsed, err := validationABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("validationRequest", validatorAddress, agentID, requestURI, requestHash)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack validationRequest: %w", err)
	}
	return data, nil
}

// EncodeValidationResponse builds calldata for
// validationResponse(bytes32,uint8,string,bytes32,string). response is the
// 0-100 score; the transaction must be submitted by the validator address
// named in the matching validationRequest (the evaluator's own wallet) —
// never by the controller. responseURI, responseHash, and tag are optional
// per spec and may be zero values.
func EncodeValidationResponse(requestHash common.Hash, response uint8, responseURI string, responseHash common.Hash, tag string) ([]byte, error) {
	if requestHash == (common.Hash{}) {
		return nil, fmt.Errorf("erc8004: requestHash must not be the zero hash")
	}
	if response > MaxValidationResponse {
		return nil, fmt.Errorf("erc8004: response %d out of range [0,%d]", response, MaxValidationResponse)
	}

	parsed, err := validationABI()
	if err != nil {
		return nil, err
	}
	data, err := parsed.Pack("validationResponse", requestHash, response, responseURI, responseHash, tag)
	if err != nil {
		return nil, fmt.Errorf("erc8004: pack validationResponse: %w", err)
	}
	return data, nil
}

// ValidationRequestCall is the decoded argument set of a validationRequest call.
type ValidationRequestCall struct {
	ValidatorAddress common.Address
	AgentID          *big.Int
	RequestURI       string
	RequestHash      common.Hash
}

// DecodeValidationRequestCalldata decodes validationRequest calldata
// (selector + ABI-encoded args). Useful for provenance checks on observed
// transactions and for tests.
func DecodeValidationRequestCalldata(data []byte) (ValidationRequestCall, error) {
	parsed, err := validationABI()
	if err != nil {
		return ValidationRequestCall{}, err
	}
	values, err := unpackCalldata(parsed, "validationRequest", data)
	if err != nil {
		return ValidationRequestCall{}, err
	}
	if len(values) != 4 {
		return ValidationRequestCall{}, fmt.Errorf("erc8004: validationRequest arg count = %d, want 4", len(values))
	}

	out := ValidationRequestCall{}
	var ok bool
	if out.ValidatorAddress, ok = values[0].(common.Address); !ok {
		return ValidationRequestCall{}, fmt.Errorf("erc8004: validatorAddress type = %T", values[0])
	}
	if out.AgentID, ok = values[1].(*big.Int); !ok {
		return ValidationRequestCall{}, fmt.Errorf("erc8004: agentId type = %T", values[1])
	}
	if out.RequestURI, ok = values[2].(string); !ok {
		return ValidationRequestCall{}, fmt.Errorf("erc8004: requestURI type = %T", values[2])
	}
	hash, ok := values[3].([32]byte)
	if !ok {
		return ValidationRequestCall{}, fmt.Errorf("erc8004: requestHash type = %T", values[3])
	}
	out.RequestHash = common.Hash(hash)
	return out, nil
}

// ValidationResponseCall is the decoded argument set of a validationResponse call.
type ValidationResponseCall struct {
	RequestHash  common.Hash
	Response     uint8
	ResponseURI  string
	ResponseHash common.Hash
	Tag          string
}

// DecodeValidationResponseCalldata decodes validationResponse calldata
// (selector + ABI-encoded args). Useful for provenance checks on observed
// evaluator transactions and for tests.
func DecodeValidationResponseCalldata(data []byte) (ValidationResponseCall, error) {
	parsed, err := validationABI()
	if err != nil {
		return ValidationResponseCall{}, err
	}
	values, err := unpackCalldata(parsed, "validationResponse", data)
	if err != nil {
		return ValidationResponseCall{}, err
	}
	if len(values) != 5 {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: validationResponse arg count = %d, want 5", len(values))
	}

	out := ValidationResponseCall{}
	reqHash, ok := values[0].([32]byte)
	if !ok {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: requestHash type = %T", values[0])
	}
	out.RequestHash = common.Hash(reqHash)
	if out.Response, ok = values[1].(uint8); !ok {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: response type = %T", values[1])
	}
	if out.ResponseURI, ok = values[2].(string); !ok {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: responseURI type = %T", values[2])
	}
	respHash, ok := values[3].([32]byte)
	if !ok {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: responseHash type = %T", values[3])
	}
	out.ResponseHash = common.Hash(respHash)
	if out.Tag, ok = values[4].(string); !ok {
		return ValidationResponseCall{}, fmt.Errorf("erc8004: tag type = %T", values[4])
	}
	return out, nil
}

// ValidationStatus mirrors getValidationStatus(bytes32) return values.
type ValidationStatus struct {
	ValidatorAddress common.Address
	AgentID          *big.Int
	Response         uint8
	ResponseHash     common.Hash
	Tag              string
	LastUpdate       *big.Int
}

// ValidationReader provides read-only access to a Validation Registry. The
// controller uses it to observe evaluator responses; it holds no signer.
type ValidationReader struct {
	contract *bind.BoundContract
}

// NewValidationReader binds a read-only Validation Registry at
// registryAddress. caller is typically (*erc8004.Client).ETH() or any
// *ethclient.Client.
func NewValidationReader(caller bind.ContractCaller, registryAddress string) (*ValidationReader, error) {
	if caller == nil {
		return nil, fmt.Errorf("erc8004: validation reader: caller must not be nil")
	}
	if !common.IsHexAddress(registryAddress) {
		return nil, fmt.Errorf("erc8004: validation reader: invalid registry address %q", registryAddress)
	}
	parsed, err := validationABI()
	if err != nil {
		return nil, err
	}
	return &ValidationReader{
		contract: bind.NewBoundContract(common.HexToAddress(registryAddress), parsed, caller, nil, nil),
	}, nil
}

// ValidationStatus reads getValidationStatus(requestHash).
func (r *ValidationReader) ValidationStatus(ctx context.Context, requestHash common.Hash) (ValidationStatus, error) {
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getValidationStatus", requestHash); err != nil {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus: %w", err)
	}
	if len(out) != 6 {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus returned %d values, want 6", len(out))
	}

	status := ValidationStatus{}
	var ok bool
	if status.ValidatorAddress, ok = out[0].(common.Address); !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus validatorAddress type = %T", out[0])
	}
	if status.AgentID, ok = out[1].(*big.Int); !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus agentId type = %T", out[1])
	}
	if status.Response, ok = out[2].(uint8); !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus response type = %T", out[2])
	}
	respHash, ok := out[3].([32]byte)
	if !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus responseHash type = %T", out[3])
	}
	status.ResponseHash = common.Hash(respHash)
	if status.Tag, ok = out[4].(string); !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus tag type = %T", out[4])
	}
	if status.LastUpdate, ok = out[5].(*big.Int); !ok {
		return ValidationStatus{}, fmt.Errorf("erc8004: getValidationStatus lastUpdate type = %T", out[5])
	}
	return status, nil
}

// Summary reads getSummary(agentId, validatorAddresses, tag) and returns the
// response count and 0-100 average.
func (r *ValidationReader) Summary(ctx context.Context, agentID *big.Int, validatorAddresses []common.Address, tag string) (count uint64, avgResponse uint8, err error) {
	if err := checkAgentID(agentID); err != nil {
		return 0, 0, err
	}
	if validatorAddresses == nil {
		validatorAddresses = []common.Address{}
	}
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getSummary", agentID, validatorAddresses, tag); err != nil {
		return 0, 0, fmt.Errorf("erc8004: validation getSummary: %w", err)
	}
	if len(out) != 2 {
		return 0, 0, fmt.Errorf("erc8004: validation getSummary returned %d values, want 2", len(out))
	}
	count, ok := out[0].(uint64)
	if !ok {
		return 0, 0, fmt.Errorf("erc8004: validation getSummary count type = %T", out[0])
	}
	avgResponse, ok = out[1].(uint8)
	if !ok {
		return 0, 0, fmt.Errorf("erc8004: validation getSummary avgResponse type = %T", out[1])
	}
	return count, avgResponse, nil
}

// AgentValidations reads getAgentValidations(agentId) — all request hashes
// recorded for the agent.
func (r *ValidationReader) AgentValidations(ctx context.Context, agentID *big.Int) ([]common.Hash, error) {
	if err := checkAgentID(agentID); err != nil {
		return nil, err
	}
	var out []interface{}
	if err := r.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getAgentValidations", agentID); err != nil {
		return nil, fmt.Errorf("erc8004: getAgentValidations: %w", err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("erc8004: getAgentValidations returned %d values, want 1", len(out))
	}
	raw, ok := out[0].([][32]byte)
	if !ok {
		return nil, fmt.Errorf("erc8004: getAgentValidations type = %T", out[0])
	}
	hashes := make([]common.Hash, len(raw))
	for i, h := range raw {
		hashes[i] = common.Hash(h)
	}
	return hashes, nil
}
