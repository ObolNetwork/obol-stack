package erc8004

import _ "embed"

//go:embed identity_registry.abi.json
var identityRegistryABI string

const (
	// IdentityRegistryBaseSepolia is the ERC-8004 Identity Registry on Base Sepolia.
	IdentityRegistryBaseSepolia = "0x8004A818BFB912233c491871b3d84c89A494BD9e"

	// ReputationRegistryBaseSepolia is the ERC-8004 Reputation Registry on Base Sepolia.
	ReputationRegistryBaseSepolia = "0x8004B663056A597Dffe9eCcC1965A193B7388713"

	// ValidationRegistryBaseSepolia is the ERC-8004 Validation Registry on Base Sepolia.
	ValidationRegistryBaseSepolia = "0x8004CB39f29c09145F24Ad9dDe2A108C1A2cdfC5"

	// DefaultRPCURL is the default JSON-RPC endpoint for Base Sepolia.
	DefaultRPCURL = "https://sepolia.base.org"

	// BaseSepoliaChainID is the EIP-155 chain ID for Base Sepolia.
	BaseSepoliaChainID = 84532
)
