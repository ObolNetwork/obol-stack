package erc8004

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client interacts with the ERC-8004 Identity Registry.
type Client struct {
	eth       *ethclient.Client
	contract  *bind.BoundContract
	parsedABI abi.ABI
	address   common.Address
	chainID   *big.Int
}

// NewClient connects to rpcURL and binds to the Identity Registry on Base Sepolia.
// For multi-network support, use NewClientForNetwork instead.
func NewClient(ctx context.Context, rpcURL string) (*Client, error) {
	return newClient(ctx, rpcURL, IdentityRegistryBaseSepolia)
}

// NewClientForNetwork connects to the eRPC base URL and binds to the Identity
// Registry on the given network. The RPC URL is constructed as
// rpcBaseURL + "/" + net.ERPCNetwork.
func NewClientForNetwork(ctx context.Context, rpcBaseURL string, net NetworkConfig) (*Client, error) {
	rpcURL := strings.TrimRight(rpcBaseURL, "/") + "/" + net.ERPCNetwork
	return newClient(ctx, rpcURL, net.RegistryAddress)
}

func newClient(ctx context.Context, rpcURL, registryAddr string) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("erc8004: dial %s: %w", rpcURL, err)
	}

	chainID, err := eth.ChainID(ctx)
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("erc8004: chain id: %w", err)
	}

	parsed, err := abi.JSON(strings.NewReader(identityRegistryABI))
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("erc8004: parse abi: %w", err)
	}

	addr := common.HexToAddress(registryAddr)
	contract := bind.NewBoundContract(addr, parsed, eth, eth, eth)

	return &Client{
		eth:       eth,
		contract:  contract,
		parsedABI: parsed,
		address:   addr,
		chainID:   chainID,
	}, nil
}

// Close releases the underlying RPC connection.
func (c *Client) Close() {
	c.eth.Close()
}

// ChainID returns the chain ID for this client's connection.
func (c *Client) ChainID() *big.Int {
	return new(big.Int).Set(c.chainID)
}

// ETH returns the underlying ethclient for direct RPC calls.
func (c *Client) ETH() *ethclient.Client {
	return c.eth
}

// Register mints a new agent NFT with the given agentURI using a raw private key.
// Returns the minted agentId (token ID).
func (c *Client) Register(ctx context.Context, key *ecdsa.PrivateKey, agentURI string) (*big.Int, error) {
	agentID, _, err := c.RegisterDetailed(ctx, key, agentURI)
	return agentID, err
}

// RegisterDetailed mints a new agent NFT with the given agentURI and returns
// both the minted agentId and transaction hash.
func (c *Client) RegisterDetailed(ctx context.Context, key *ecdsa.PrivateKey, agentURI string) (*big.Int, string, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(key, c.chainID)
	if err != nil {
		return nil, "", fmt.Errorf("erc8004: transactor: %w", err)
	}
	opts.Context = ctx
	return c.RegisterWithOptsDetailed(ctx, opts, agentURI)
}

// RegisterWithOpts mints a new agent NFT using the provided TransactOpts.
// This allows callers to supply a custom Signer (e.g. remote-signer).
func (c *Client) RegisterWithOpts(ctx context.Context, opts *bind.TransactOpts, agentURI string) (*big.Int, error) {
	agentID, _, err := c.RegisterWithOptsDetailed(ctx, opts, agentURI)
	return agentID, err
}

// RegisterWithOptsDetailed mints a new agent NFT using the provided
// TransactOpts and returns both the minted agentId and transaction hash.
func (c *Client) RegisterWithOptsDetailed(ctx context.Context, opts *bind.TransactOpts, agentURI string) (*big.Int, string, error) {
	tx, err := c.contract.Transact(opts, "register", agentURI)
	if err != nil {
		return nil, "", fmt.Errorf("erc8004: register tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, c.eth, tx)
	if err != nil {
		return nil, "", fmt.Errorf("erc8004: wait mined: %w", err)
	}

	return c.parseRegisteredEvent(receipt, tx.Hash())
}

// SubmitRegister submits a registration transaction and returns its hash
// without waiting for the receipt.
func (c *Client) SubmitRegister(ctx context.Context, key *ecdsa.PrivateKey, agentURI string) (string, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(key, c.chainID)
	if err != nil {
		return "", fmt.Errorf("erc8004: transactor: %w", err)
	}
	opts.Context = ctx

	tx, err := c.contract.Transact(opts, "register", agentURI)
	if err != nil {
		return "", fmt.Errorf("erc8004: register tx: %w", err)
	}

	return tx.Hash().Hex(), nil
}

// CurrentBlockNumber returns the current tip height for the connected chain.
func (c *Client) CurrentBlockNumber(ctx context.Context) (uint64, error) {
	height, err := c.eth.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("erc8004: current block number: %w", err)
	}
	return height, nil
}

// parseRegisteredEvent extracts the agentId from the Registered event in a receipt.
func (c *Client) parseRegisteredEvent(receipt *types.Receipt, txHash common.Hash) (*big.Int, string, error) {
	for _, vLog := range receipt.Logs {
		agentID, _, _, err := c.parseRegisteredLog(vLog)
		if err == nil {
			return agentID, txHash.Hex(), nil
		}
	}

	return nil, "", fmt.Errorf("erc8004: Registered event not found in receipt (tx: %s)", txHash.Hex())
}

// FindRegistrationByTxHash resolves a previously submitted registration
// transaction once it has been mined.
func (c *Client) FindRegistrationByTxHash(ctx context.Context, txHash string) (*big.Int, string, bool, error) {
	hash := common.HexToHash(strings.TrimSpace(txHash))
	if hash == (common.Hash{}) {
		return nil, "", false, nil
	}

	receipt, err := c.eth.TransactionReceipt(ctx, hash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("erc8004: transaction receipt %s: %w", txHash, err)
	}

	agentID, registeredTxHash, err := c.parseRegisteredEvent(receipt, hash)
	if err != nil {
		return nil, "", false, err
	}

	return agentID, registeredTxHash, true, nil
}

// FindRegistrationByOwnerAndURI recovers a registration by scanning recent
// Registered events for the controller wallet and published URI.
func (c *Client) FindRegistrationByOwnerAndURI(ctx context.Context, owner common.Address, agentURI string, fromBlock uint64) (*big.Int, string, bool, error) {
	registeredEvent := c.parsedABI.Events["Registered"]
	query := ethereum.FilterQuery{
		Addresses: []common.Address{c.address},
		FromBlock: new(big.Int).SetUint64(fromBlock),
		Topics: [][]common.Hash{
			{registeredEvent.ID},
			nil,
			{common.BytesToHash(owner.Bytes())},
		},
	}

	logs, err := c.eth.FilterLogs(ctx, query)
	if err != nil {
		return nil, "", false, fmt.Errorf("erc8004: filter logs: %w", err)
	}

	wantURI := strings.TrimSpace(agentURI)
	for i := len(logs) - 1; i >= 0; i-- {
		agentID, loggedURI, loggedOwner, err := c.parseRegisteredLog(&logs[i])
		if err != nil {
			continue
		}
		if loggedOwner != owner || strings.TrimSpace(loggedURI) != wantURI {
			continue
		}
		return agentID, logs[i].TxHash.Hex(), true, nil
	}

	return nil, "", false, nil
}

func (c *Client) parseRegisteredLog(vLog *types.Log) (*big.Int, string, common.Address, error) {
	registeredEvent := c.parsedABI.Events["Registered"]
	if len(vLog.Topics) < 3 || vLog.Topics[0] != registeredEvent.ID {
		return nil, "", common.Address{}, fmt.Errorf("erc8004: not a Registered log")
	}

	values, err := registeredEvent.Inputs.NonIndexed().Unpack(vLog.Data)
	if err != nil {
		return nil, "", common.Address{}, fmt.Errorf("erc8004: unpack Registered log: %w", err)
	}
	if len(values) != 1 {
		return nil, "", common.Address{}, fmt.Errorf("erc8004: Registered log data length = %d, want 1", len(values))
	}

	agentURI, ok := values[0].(string)
	if !ok {
		return nil, "", common.Address{}, fmt.Errorf("erc8004: Registered agentURI type = %T", values[0])
	}

	return new(big.Int).SetBytes(vLog.Topics[1].Bytes()), agentURI, common.BytesToAddress(vLog.Topics[2].Bytes()), nil
}

// SetMetadataWithOpts stores key-value metadata using the provided TransactOpts.
func (c *Client) SetMetadataWithOpts(ctx context.Context, opts *bind.TransactOpts, agentID *big.Int, k string, v []byte) error {
	tx, err := c.contract.Transact(opts, "setMetadata", agentID, k, v)
	if err != nil {
		return fmt.Errorf("erc8004: setMetadata tx: %w", err)
	}

	if _, err := bind.WaitMined(ctx, c.eth, tx); err != nil {
		return fmt.Errorf("erc8004: wait mined: %w", err)
	}
	return nil
}

// SetAgentURI updates the agentURI for an existing agent NFT.
func (c *Client) SetAgentURI(ctx context.Context, key *ecdsa.PrivateKey, agentID *big.Int, uri string) error {
	opts, err := bind.NewKeyedTransactorWithChainID(key, c.chainID)
	if err != nil {
		return fmt.Errorf("erc8004: transactor: %w", err)
	}
	opts.Context = ctx

	tx, err := c.contract.Transact(opts, "setAgentURI", agentID, uri)
	if err != nil {
		return fmt.Errorf("erc8004: setAgentURI tx: %w", err)
	}

	if _, err := bind.WaitMined(ctx, c.eth, tx); err != nil {
		return fmt.Errorf("erc8004: wait mined: %w", err)
	}
	return nil
}

// SetMetadata stores arbitrary key-value metadata on the agent NFT.
func (c *Client) SetMetadata(ctx context.Context, key *ecdsa.PrivateKey, agentID *big.Int, k string, v []byte) error {
	opts, err := bind.NewKeyedTransactorWithChainID(key, c.chainID)
	if err != nil {
		return fmt.Errorf("erc8004: transactor: %w", err)
	}
	opts.Context = ctx

	tx, err := c.contract.Transact(opts, "setMetadata", agentID, k, v)
	if err != nil {
		return fmt.Errorf("erc8004: setMetadata tx: %w", err)
	}

	if _, err := bind.WaitMined(ctx, c.eth, tx); err != nil {
		return fmt.Errorf("erc8004: wait mined: %w", err)
	}
	return nil
}

// GetMetadata reads metadata for the given key from the agent NFT.
func (c *Client) GetMetadata(ctx context.Context, agentID *big.Int, k string) ([]byte, error) {
	var out []interface{}
	err := c.contract.Call(&bind.CallOpts{Context: ctx}, &out, "getMetadata", agentID, k)
	if err != nil {
		return nil, fmt.Errorf("erc8004: getMetadata: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, ok := out[0].([]byte)
	if !ok {
		return nil, fmt.Errorf("erc8004: getMetadata: unexpected type %T", out[0])
	}
	return b, nil
}

// TokenURI returns the ERC-721 tokenURI for the agent NFT.
func (c *Client) TokenURI(ctx context.Context, agentID *big.Int) (string, error) {
	var out []interface{}
	err := c.contract.Call(&bind.CallOpts{Context: ctx}, &out, "tokenURI", agentID)
	if err != nil {
		return "", fmt.Errorf("erc8004: tokenURI: %w", err)
	}
	if len(out) == 0 {
		return "", nil
	}
	s, ok := out[0].(string)
	if !ok {
		return "", fmt.Errorf("erc8004: tokenURI: unexpected type %T", out[0])
	}
	return s, nil
}
