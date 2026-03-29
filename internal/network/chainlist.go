package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	// ChainListURL is the DefiLlama-hosted ChainList RPC data endpoint.
	chainListURL = "https://chainlist.org/rpcs.json"

	// Maximum number of RPCs to return from FetchChainListRPCs.
	defaultMaxRPCs = 3

	// HTTP timeout for ChainList fetch.
	chainListTimeout = 15 * time.Second
)

// RPCEndpoint represents a single RPC endpoint from ChainList.
type RPCEndpoint struct {
	URL      string `json:"url"`
	Tracking string `json:"tracking"` // "none", "limited", or "yes"
}

// ChainEntry represents a single chain entry from the ChainList API.
type ChainEntry struct {
	Name    string `json:"name"`
	Chain   string `json:"chain"`
	ChainID int    `json:"chainId"`
	RPC     []any  `json:"rpc"` // mix of strings and objects
}

// chainNames maps common chain names/aliases to chain IDs.
var chainNames = map[string]int{
	"mainnet":      1,
	"ethereum":     1,
	"base":         8453,
	"base-sepolia": 84532,
	"arbitrum":     42161,
	"arbitrum-one": 42161,
	"optimism":     10,
	"op-mainnet":   10,
	"polygon":      137,
	"avalanche":    43114,
	"avax":         43114,
	"bsc":          56,
	"bnb":          56,
	"gnosis":       100,
	"sepolia":      11155111,
	"hoodi":        560048,
	"zksync":       324,
	"scroll":       534352,
	"linea":        59144,
	"fantom":       250,
	"celo":         42220,
}

// ResolveChainID converts a chain name or numeric string to a chain ID.
// Returns the chain ID and the resolved name (for display).
func ResolveChainID(nameOrID string) (int, string, error) {
	nameOrID = strings.ToLower(strings.TrimSpace(nameOrID))

	// Check name map first.
	if id, ok := chainNames[nameOrID]; ok {
		return id, nameOrID, nil
	}

	// Try parsing as numeric chain ID.
	var chainID int
	if _, err := fmt.Sscanf(nameOrID, "%d", &chainID); err == nil && chainID > 0 {
		return chainID, fmt.Sprintf("chain-%d", chainID), nil
	}

	// Build suggestions.
	var names []string
	for name := range chainNames {
		names = append(names, name)
	}

	sort.Strings(names)

	return 0, "", fmt.Errorf("unknown chain %q. Known chains: %s\nOr use a numeric chain ID (e.g., 8453)", nameOrID, strings.Join(names, ", "))
}

// ChainListFetcher abstracts the HTTP fetch so tests can inject fixtures.
type ChainListFetcher func() ([]byte, error)

// DefaultChainListFetcher fetches from the real ChainList API.
func DefaultChainListFetcher() ([]byte, error) {
	client := &http.Client{Timeout: chainListTimeout}

	resp, err := client.Get(chainListURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ChainList data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChainList returned HTTP %d", resp.StatusCode)
	}

	// Limit to 10MB to avoid unbounded reads.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read ChainList response: %w", err)
	}

	return data, nil
}

// FetchChainListRPCs fetches RPCs for a given chain ID from ChainList,
// filters for free/public HTTPS endpoints, and returns up to maxRPCs
// sorted by quality (tracking=none preferred over tracking=limited).
func FetchChainListRPCs(chainID int, fetcher ChainListFetcher) ([]RPCEndpoint, string, error) {
	if fetcher == nil {
		fetcher = DefaultChainListFetcher
	}

	data, err := fetcher()
	if err != nil {
		return nil, "", err
	}

	return ParseAndFilterRPCs(data, chainID, defaultMaxRPCs)
}

// ParseAndFilterRPCs parses ChainList JSON, finds the chain, filters and sorts RPCs.
// Exported for testing.
func ParseAndFilterRPCs(data []byte, chainID, maxRPCs int) ([]RPCEndpoint, string, error) {
	var chains []ChainEntry
	if err := json.Unmarshal(data, &chains); err != nil {
		return nil, "", fmt.Errorf("failed to parse ChainList JSON: %w", err)
	}

	// Find the chain entry.
	var target *ChainEntry

	for i := range chains {
		if chains[i].ChainID == chainID {
			target = &chains[i]
			break
		}
	}

	if target == nil {
		return nil, "", fmt.Errorf("chain ID %d not found in ChainList data", chainID)
	}

	// Parse RPCs — the RPC field is a mix of strings and objects.
	var endpoints []RPCEndpoint

	for _, raw := range target.RPC {
		var ep RPCEndpoint

		switch v := raw.(type) {
		case string:
			ep = RPCEndpoint{URL: v, Tracking: "unknown"}
		case map[string]any:
			if url, ok := v["url"].(string); ok {
				ep.URL = url
			}

			if tracking, ok := v["tracking"].(string); ok {
				ep.Tracking = tracking
			} else {
				ep.Tracking = "unknown"
			}
		default:
			continue
		}

		endpoints = append(endpoints, ep)
	}

	// Filter: HTTPS only, no heavy tracking.
	filtered := FilterFreeRPCs(endpoints)

	// Sort by quality.
	SortByQuality(filtered)

	// Cap at maxRPCs.
	if len(filtered) > maxRPCs {
		filtered = filtered[:maxRPCs]
	}

	return filtered, target.Name, nil
}

// FilterFreeRPCs filters RPC endpoints to only include free, HTTPS, non-tracking endpoints.
func FilterFreeRPCs(endpoints []RPCEndpoint) []RPCEndpoint {
	var result []RPCEndpoint

	for _, ep := range endpoints {
		// HTTPS only.
		if !strings.HasPrefix(ep.URL, "https://") {
			continue
		}

		// Skip endpoints with full tracking.
		if ep.Tracking == "yes" {
			continue
		}

		// Skip endpoints that require API keys (contain placeholders).
		if strings.Contains(ep.URL, "${") || strings.Contains(ep.URL, "{") {
			continue
		}

		// Skip WebSocket endpoints.
		if strings.HasPrefix(ep.URL, "wss://") {
			continue
		}

		result = append(result, ep)
	}

	return result
}

// SortByQuality sorts RPC endpoints by tracking quality.
// Preference: tracking=none > tracking=limited > tracking=unknown > anything else.
func SortByQuality(endpoints []RPCEndpoint) {
	sort.SliceStable(endpoints, func(i, j int) bool {
		return trackingScore(endpoints[i].Tracking) < trackingScore(endpoints[j].Tracking)
	})
}

// trackingScore returns a numeric score for sorting (lower is better).
func trackingScore(tracking string) int {
	switch tracking {
	case "none":
		return 0
	case "limited":
		return 1
	case "unknown":
		return 2
	default:
		return 3
	}
}
