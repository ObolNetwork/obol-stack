package erc8004

import _ "embed"

//go:embed identity_registry.abi.json
var identityRegistryABI string

const (
	// IdentityRegistryBaseSepolia is the ERC-8004 Identity Registry on Base Sepolia.
	IdentityRegistryBaseSepolia = "0x8004A818BFB912233c491871b3d84c89A494BD9e"

	// IdentityRegistryMainnet is the ERC-8004 Identity Registry on Ethereum
	// mainnet and Base mainnet (deployed at the same address via CREATE2).
	IdentityRegistryMainnet = "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"

	// ReputationRegistryBaseSepolia is the ERC-8004 Reputation Registry on Base Sepolia.
	ReputationRegistryBaseSepolia = "0x8004B663056A597Dffe9eCcC1965A193B7388713"

	// ValidationRegistryBaseSepolia is the v1.0.0 ERC-8004 Validation Registry
	// address from the pre-v2 draft.
	//
	// Deprecated: this address has NO CODE on Base Sepolia (it was an Ethereum
	// Sepolia deployment). Use ValidationRegistryAddress(network) /
	// ValidationRegistryV2BaseSepolia (validation.go) — verified on-chain,
	// getVersion()=="2.0.0".
	ValidationRegistryBaseSepolia = "0x8004CB39f29c09145F24Ad9dDe2A108C1A2cdfC5"

	// DefaultRPCBase is the default JSON-RPC base URL the controller uses to
	// build per-chain ERC-8004 clients. It points at the in-cluster eRPC
	// service; per-chain endpoints are derived as
	//   <base>/<NetworkConfig.ERPCNetwork>
	// (see erc8004.NewClientForNetwork). Override with ERC8004_RPC_BASE.
	DefaultRPCBase = "http://erpc.erpc.svc.cluster.local/rpc"

	// BaseSepoliaChainID is the EIP-155 chain ID for Base Sepolia.
	BaseSepoliaChainID = 84532
)
