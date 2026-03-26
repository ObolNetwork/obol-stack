package erc8004

import (
	"fmt"
	"strings"
)

// NetworkConfig describes an ERC-8004 registration network.
type NetworkConfig struct {
	// Name is the canonical network name (e.g. "base-sepolia", "base", "ethereum").
	Name string

	// ChainID is the EIP-155 chain identifier.
	ChainID int64

	// RegistryAddress is the ERC-8004 Identity Registry contract address on this chain.
	RegistryAddress string

	// SponsorURL is the sponsored registration API endpoint (empty if not available).
	SponsorURL string

	// DelegateAddress is the EIP-7702 delegation contract used by the sponsor.
	DelegateAddress string

	// ERPCNetwork is the path segment used by eRPC to route to this chain
	// (e.g. "base-sepolia", "base", "mainnet").
	ERPCNetwork string
}

// HasSponsor returns true if this network supports sponsored (zero-gas) registration.
func (n NetworkConfig) HasSponsor() bool {
	return n.SponsorURL != ""
}

// CAIP10Registry returns the CAIP-10 formatted registry identifier.
func (n NetworkConfig) CAIP10Registry() string {
	return fmt.Sprintf("eip155:%d:%s", n.ChainID, n.RegistryAddress)
}

// Predefined network configurations.
var (
	BaseSepolia = NetworkConfig{
		Name:            "base-sepolia",
		ChainID:         84532,
		RegistryAddress: IdentityRegistryBaseSepolia,
		ERPCNetwork:     "base-sepolia",
	}

	Base = NetworkConfig{
		Name:            "base",
		ChainID:         8453,
		RegistryAddress: IdentityRegistryBaseSepolia, // CREATE2 — same address across chains
		ERPCNetwork:     "base",
	}

	Ethereum = NetworkConfig{
		Name:            "ethereum",
		ChainID:         1,
		RegistryAddress: "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432",
		SponsorURL:      "https://sponsored.howto8004.com/api/register",
		DelegateAddress: "0x77fb3D2ff6dB9dcbF1b7E0693b3c746B30499eE8",
		ERPCNetwork:     "mainnet",
	}
)

// allNetworks is the set of supported registration networks.
var allNetworks = []NetworkConfig{BaseSepolia, Base, Ethereum}

// ResolveNetwork maps a network name to its configuration.
func ResolveNetwork(name string) (NetworkConfig, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "base-sepolia":
		return BaseSepolia, nil
	case "base", "base-mainnet":
		return Base, nil
	case "ethereum", "ethereum-mainnet", "mainnet":
		return Ethereum, nil
	default:
		return NetworkConfig{}, fmt.Errorf("unsupported registration network: %q (supported: base-sepolia, base, ethereum)", name)
	}
}

// ResolveNetworks parses a comma-separated list of network names.
func ResolveNetworks(csv string) ([]NetworkConfig, error) {
	parts := strings.Split(csv, ",")
	seen := map[string]bool{}
	var result []NetworkConfig

	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		net, err := ResolveNetwork(name)
		if err != nil {
			return nil, err
		}
		if seen[net.Name] {
			continue // deduplicate
		}
		seen[net.Name] = true
		result = append(result, net)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid networks specified")
	}
	return result, nil
}

// SupportedNetworks returns all supported registration networks.
func SupportedNetworks() []NetworkConfig {
	out := make([]NetworkConfig, len(allNetworks))
	copy(out, allNetworks)
	return out
}
