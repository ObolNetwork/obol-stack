package inference

// detect.go — auto-discovery of local inference servers.
//
// Probes well-known ports for llama-server, ollama, vLLM, and LiteLLM,
// hitting their OpenAI-compatible /v1/models endpoint to enumerate
// available models. Used by `obol sell inference` and `obol model setup`
// to auto-detect providers when the user doesn't specify --upstream or --model.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LocalDiscoveryPortsEnv lets operators add to (or override) the default
// list of probed local inference ports. Format: comma-separated
// "port[:label]" pairs, e.g. "9000:vllm,5001:custom". Labels are
// informational; the actual server type is still detected by probing.
const LocalDiscoveryPortsEnv = "OBOL_LOCAL_MODEL_DISCOVERY_PORTS"

// Server-type identifiers returned by DetectServerType / set as the
// expected label on probe entries. Centralised here so callers
// (internal/model/discover.go, status output) match by constant.
const (
	ServerTypeOllama       = "ollama"
	ServerTypeLlamaServer  = "llama-server"
	ServerTypeVLLM         = "vllm"
	ServerTypeLMStudio     = "lm-studio"
	ServerTypeLiteLLM      = "litellm"
	ServerTypeOpenAICompat = "openai-compat"
)

// LiteLLMPort is LiteLLM's default listen port — exposed so callers like
// internal/model can skip it during discovery without redefining the
// constant.
const LiteLLMPort = 4000

// ModelInfo describes a single model exposed by an inference server.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	Created int64  `json:"created"`
}

// EndpointInfo describes a discovered local inference endpoint.
type EndpointInfo struct {
	Host       string
	Port       int
	ServerType string
	Models     []ModelInfo
	Healthy    bool
}

// BaseURL returns the HTTP base URL for this endpoint.
func (e EndpointInfo) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", e.Host, e.Port)
}

// modelsResponse is the OpenAI /v1/models JSON envelope.
type modelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// portProbe is one entry in the well-known port list. The actual server
// type is still determined by probing (see detectServerTypeWithClient).
type portProbe struct {
	Port       int
	ServerType string
}

// commonPorts maps well-known ports to their expected server type. sglang,
// mlx-lm, and exllamav3 default to 8000 like vLLM, so they share that
// probe. LM Studio defaults to 1234.
var commonPorts = []portProbe{
	{8080, ServerTypeLlamaServer},
	{11434, ServerTypeOllama},
	{8000, ServerTypeVLLM},
	{1234, ServerTypeLMStudio},
	{LiteLLMPort, ServerTypeLiteLLM},
}

// extraPortsFromEnv parses LocalDiscoveryPortsEnv into additional probes.
// Format: "port[:label],port[:label],...". Invalid entries are skipped
// silently — the env var is a power-user knob and we don't want
// `obol stack up` to fail because of a typo.
func extraPortsFromEnv() []portProbe {
	raw := strings.TrimSpace(os.Getenv(LocalDiscoveryPortsEnv))
	if raw == "" {
		return nil
	}
	var out []portProbe
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		portStr, label, _ := strings.Cut(item, ":")
		port, err := strconv.Atoi(strings.TrimSpace(portStr))
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		label = strings.TrimSpace(label)
		if label == "" {
			label = ServerTypeOpenAICompat
		}
		out = append(out, portProbe{Port: port, ServerType: label})
	}
	return out
}

// resolvedProbePorts returns the effective port list for a scan: defaults
// plus any extras from LocalDiscoveryPortsEnv, deduplicated by port (the
// default entry wins so detection priority stays predictable).
func resolvedProbePorts() []portProbe {
	seen := make(map[int]bool, len(commonPorts))
	out := make([]portProbe, 0, len(commonPorts))
	for _, p := range commonPorts {
		if seen[p.Port] {
			continue
		}
		seen[p.Port] = true
		out = append(out, p)
	}
	for _, p := range extraPortsFromEnv() {
		if seen[p.Port] {
			continue
		}
		seen[p.Port] = true
		out = append(out, p)
	}
	return out
}

// probeTimeout is the per-endpoint HTTP timeout.
const probeTimeout = 2 * time.Second

// ProbeEndpoint hits host:port/v1/models and returns discovered info.
func ProbeEndpoint(host string, port int) (*EndpointInfo, error) {
	return ProbeEndpointContext(context.Background(), host, port)
}

// ProbeEndpointContext is the context-aware version of ProbeEndpoint.
// It creates a shared HTTP client used for both server type detection
// and model fetching to avoid redundant connections.
func ProbeEndpointContext(ctx context.Context, host string, port int) (*EndpointInfo, error) {
	client := &http.Client{Timeout: probeTimeout}
	baseURL := fmt.Sprintf("http://%s:%d", host, port)
	serverType := detectServerTypeWithClient(ctx, client, baseURL)
	if serverType == "" {
		return nil, fmt.Errorf("no inference server detected at %s", baseURL)
	}

	models, err := fetchModels(ctx, client, baseURL)
	if err != nil {
		return nil, fmt.Errorf("fetching models from %s: %w", baseURL, err)
	}

	return &EndpointInfo{
		Host:       host,
		Port:       port,
		ServerType: serverType,
		Models:     models,
		Healthy:    true,
	}, nil
}

// ScanLocalEndpoints probes all common local ports and returns any that respond.
func ScanLocalEndpoints() ([]EndpointInfo, error) {
	return ScanLocalEndpointsContext(context.Background())
}

// ScanLocalEndpointsContext probes common ports concurrently with context support.
// All ports are probed in parallel using goroutines; results are collected
// and returned in stable port order. The probed port list is the union of
// the built-in defaults and any extras from LocalDiscoveryPortsEnv.
func ScanLocalEndpointsContext(ctx context.Context) ([]EndpointInfo, error) {
	ports := resolvedProbePorts()

	type result struct {
		idx int
		ep  *EndpointInfo
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []result
	)

	for i, cp := range ports {
		// Check context before launching goroutine.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		wg.Add(1)
		go func(idx int, port int) {
			defer wg.Done()
			ep, err := ProbeEndpointContext(ctx, "127.0.0.1", port)
			if err != nil {
				return // port not running — skip
			}
			mu.Lock()
			results = append(results, result{idx: idx, ep: ep})
			mu.Unlock()
		}(i, cp.Port)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Sort results by original port order.
	sorted := make([]*EndpointInfo, len(ports))
	for _, r := range results {
		sorted[r.idx] = r.ep
	}
	var found []EndpointInfo
	for _, ep := range sorted {
		if ep != nil {
			found = append(found, *ep)
		}
	}
	return found, nil
}

// DetectServerType probes baseURL to determine the server software.
// Returns "ollama", "llama-server", "openai-compat", or "".
func DetectServerType(ctx context.Context, baseURL string) string {
	client := &http.Client{Timeout: probeTimeout}
	return detectServerTypeWithClient(ctx, client, baseURL)
}

// detectServerTypeWithClient probes baseURL using the provided client.
//
// Detection order priority:
//  1. ollama — checked first via /api/tags (ollama-specific endpoint)
//  2. llama-server — checked via /health (llama.cpp health endpoint)
//  3. openai-compat — fallback via /v1/models (generic OpenAI-compatible)
//
// Ollama is checked before the generic OpenAI-compatible endpoint because
// ollama also serves /v1/models, so we need the more specific check first.
func detectServerTypeWithClient(ctx context.Context, client *http.Client, baseURL string) string {
	if probeURL(ctx, client, baseURL+"/api/tags") {
		return ServerTypeOllama
	}
	if probeURL(ctx, client, baseURL+"/health") {
		return ServerTypeLlamaServer
	}
	if probeURL(ctx, client, baseURL+"/v1/models") {
		return ServerTypeOpenAICompat
	}
	return ""
}

// probeURL returns true if a GET to url returns 200.
func probeURL(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// fetchModels retrieves the model list from the /v1/models endpoint.
// The response body is limited to 1 MB to prevent excessive memory usage.
func fetchModels(ctx context.Context, client *http.Client, baseURL string) ([]ModelInfo, error) {
	modelsURL := baseURL + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, modelsURL)
	}

	// Limit response body to 1 MB to prevent oversized responses.
	limited := io.LimitReader(resp.Body, 1<<20)

	var mr modelsResponse
	if err := json.NewDecoder(limited).Decode(&mr); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}
	return mr.Data, nil
}

// ParseModelsResponse parses raw JSON bytes into a slice of ModelInfo.
// Exported for testing.
func ParseModelsResponse(data []byte) ([]ModelInfo, error) {
	var mr modelsResponse
	if err := json.Unmarshal(data, &mr); err != nil {
		return nil, fmt.Errorf("parsing models JSON: %w", err)
	}
	return mr.Data, nil
}

// FormatEndpointDisplay pretty-prints a list of discovered endpoints.
func FormatEndpointDisplay(endpoints []EndpointInfo) string {
	var b strings.Builder
	b.WriteString("Discovered inference endpoints:\n")
	if len(endpoints) == 0 {
		b.WriteString("  (none)\n")
		return b.String()
	}
	for _, ep := range endpoints {
		status := "healthy"
		if !ep.Healthy {
			status = "unhealthy"
		}
		fmt.Fprintf(&b, "\n  %s (%s) [%s]\n", ep.BaseURL(), ep.ServerType, status)
		if len(ep.Models) == 0 {
			b.WriteString("    (no models loaded)\n")
		}
		for _, m := range ep.Models {
			owner := m.OwnedBy
			if owner == "" {
				owner = "unknown"
			}
			fmt.Fprintf(&b, "    - %s (by %s)\n", m.ID, owner)
		}
	}
	return b.String()
}
