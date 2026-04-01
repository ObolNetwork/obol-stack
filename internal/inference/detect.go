package inference

// detect.go — auto-discovery of local inference servers.
//
// Probes well-known ports for llama-server, ollama, vLLM, and LiteLLM,
// hitting their OpenAI-compatible /v1/models endpoint to enumerate
// available models. Used by the first-run wizard to auto-configure.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

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

// commonPorts maps well-known ports to their expected server type.
var commonPorts = []struct {
	Port       int
	ServerType string
}{
	{8080, "llama-server"},
	{11434, "ollama"},
	{8000, "vllm"},
	{4000, "litellm"},
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
// and returned in the same order as commonPorts.
func ScanLocalEndpointsContext(ctx context.Context) ([]EndpointInfo, error) {
	type result struct {
		idx int
		ep  *EndpointInfo
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []result
	)

	for i, cp := range commonPorts {
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
	sorted := make([]*EndpointInfo, len(commonPorts))
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
	// Check ollama-specific /api/tags first.
	if ok := probeURL(ctx, client, baseURL+"/api/tags"); ok {
		return "ollama"
	}
	// Check llama-server /health endpoint.
	if ok := probeURL(ctx, client, baseURL+"/health"); ok {
		return "llama-server"
	}
	// Generic OpenAI-compatible /v1/models.
	if ok := probeURL(ctx, client, baseURL+"/v1/models"); ok {
		return "openai-compat"
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
