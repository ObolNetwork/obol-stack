package erc8004

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

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
	return c.chainID
}

// ETH returns the underlying ethclient for direct RPC calls.
func (c *Client) ETH() *ethclient.Client {
	return c.eth
}

// Register mints a new agent NFT with the given agentURI using a raw private key.
// Returns the minted agentId (token ID).
func (c *Client) Register(ctx context.Context, key *ecdsa.PrivateKey, agentURI string) (*big.Int, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(key, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("erc8004: transactor: %w", err)
	}
	opts.Context = ctx
	return c.RegisterWithOpts(ctx, opts, agentURI)
}

// RegisterWithOpts mints a new agent NFT using the provided TransactOpts.
// This allows callers to supply a custom Signer (e.g. remote-signer).
func (c *Client) RegisterWithOpts(ctx context.Context, opts *bind.TransactOpts, agentURI string) (*big.Int, error) {
	tx, err := c.contract.Transact(opts, "register", agentURI)
	if err != nil {
		return nil, fmt.Errorf("erc8004: register tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, c.eth, tx)
	if err != nil {
		return nil, fmt.Errorf("erc8004: wait mined: %w", err)
	}

	return c.parseRegisteredEvent(receipt, tx.Hash())
}

// parseRegisteredEvent extracts the agentId from the Registered event in a receipt.
func (c *Client) parseRegisteredEvent(receipt *types.Receipt, txHash common.Hash) (*big.Int, error) {
	registeredEvent := c.parsedABI.Events["Registered"]
	for _, vLog := range receipt.Logs {
		if vLog.Topics[0] != registeredEvent.ID {
			continue
		}
		// agentId is indexed (topic[1]).
		agentID := new(big.Int).SetBytes(vLog.Topics[1].Bytes())
		return agentID, nil
	}

	return nil, fmt.Errorf("erc8004: Registered event not found in receipt (tx: %s)", txHash.Hex())
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
