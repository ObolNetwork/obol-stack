package model

// Ollama is intentionally NOT discovered here — autoConfigureLLM has a
// dedicated branch that calls ListOllamaModels() and routes through the
// in-cluster ollama.llm.svc service, not host.k3d.internal:11434. LiteLLM
// itself (port 4000) is also skipped to avoid configuring it against
// itself.

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/ObolNetwork/obol-stack/internal/inference"
)

// DiscoverDisabledEnv is the env var that disables local-server discovery.
// Set to "1" or "true" to skip the scan entirely (useful when an unrelated
// service binds one of the well-known inference ports).
const DiscoverDisabledEnv = "OBOL_DISABLE_LOCAL_MODEL_DISCOVERY"

// DiscoveredProvider is one local OpenAI-compatible inference server that
// auto-config can register with LiteLLM.
type DiscoveredProvider struct {
	Label           string
	ServerType      string
	HostEndpoint    string
	ClusterEndpoint string
	Entries         []ModelEntry
}

// DiscoverLocalProviders probes well-known local inference ports and
// returns one DiscoveredProvider per host endpoint that exposes at least
// one model. Returns an empty slice (not an error) when discovery is
// disabled or nothing is reachable. Honors OBOL_DISABLE_LOCAL_MODEL_DISCOVERY.
func DiscoverLocalProviders(ctx context.Context) ([]DiscoveredProvider, error) {
	if v := os.Getenv(DiscoverDisabledEnv); v == "1" || v == "true" {
		return nil, nil
	}

	endpoints, err := inference.ScanLocalEndpointsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan local endpoints: %w", err)
	}

	var out []DiscoveredProvider
	for _, ep := range endpoints {
		if !shouldRegisterEndpoint(ep) {
			continue
		}
		out = append(out, buildDiscoveredProvider(ep))
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// shouldRegisterEndpoint returns false for endpoints that auto-config
// must skip — LiteLLM (configuring itself), Ollama (separate code path),
// and anything with no models loaded.
func shouldRegisterEndpoint(ep inference.EndpointInfo) bool {
	if ep.Port == inference.LiteLLMPort {
		return false
	}
	if ep.ServerType == inference.ServerTypeOllama {
		return false
	}
	if len(ep.Models) == 0 {
		return false
	}
	return true
}

func buildDiscoveredProvider(ep inference.EndpointInfo) DiscoveredProvider {
	host := ep.BaseURL()
	cluster := localhostToClusterEndpoint(host)

	entries := make([]ModelEntry, 0, len(ep.Models))
	for _, m := range ep.Models {
		if m.ID == "" {
			continue
		}
		// The /v1 suffix is required — LiteLLM's OpenAI provider does not
		// append it (CLAUDE.md pitfall 6). Without it, discovered vLLM
		// endpoints 404 on /chat/completions and poison the model group
		// alongside any correct `model setup custom` entry.
		entries = append(entries, buildCustomEndpointEntry(m.ID, cluster+"/v1", ""))
	}

	return DiscoveredProvider{
		Label:           fmt.Sprintf("%s@%d", ep.ServerType, ep.Port),
		ServerType:      ep.ServerType,
		HostEndpoint:    host,
		ClusterEndpoint: cluster,
		Entries:         entries,
	}
}
