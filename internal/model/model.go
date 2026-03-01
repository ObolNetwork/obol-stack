package model

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	namespace     = "llm"
	secretName    = "llms-secrets"
	configMapName = "llmspy-config"
	deployName    = "llmspy"
)

// ProviderInfo describes an llmspy provider discovered from the running pod.
type ProviderInfo struct {
	ID      string // provider id (e.g. "zai", "anthropic")
	Name    string // display name (e.g. "Z.AI", "Anthropic")
	EnvVar  string // env var for API key (e.g. "ZHIPU_API_KEY")
}

// ProviderStatus captures effective global llmspy provider state.
type ProviderStatus struct {
	Enabled   bool
	HasAPIKey bool
	EnvVar    string // environment variable name (e.g. ANTHROPIC_API_KEY)
}

// ConfigureLLMSpy enables a cloud provider in the llmspy gateway.
// It discovers the provider's env var from the running llmspy pod,
// patches the llms-secrets Secret with the API key, enables the provider
// in the llmspy-config ConfigMap, and restarts the deployment.
func ConfigureLLMSpy(cfg *config.Config, u *ui.UI, provider, apiKey string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// Discover the env var name from the llmspy pod's providers.json
	envKey, err := getProviderEnvKey(kubectlBinary, kubeconfigPath, provider)
	if err != nil {
		return err
	}

	// 1. Patch the Secret with the API key
	u.Infof("Configuring llmspy: setting %s key", provider)
	patchJSON := fmt.Sprintf(`{"stringData":{"%s":"%s"}}`, envKey, apiKey)
	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "secret", secretName, "-n", namespace,
		"-p", patchJSON, "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch llmspy secret: %w", err)
	}

	// 2. Read current ConfigMap, enable the provider in llms.json
	u.Infof("Enabling %s provider in llmspy config", provider)
	if err := enableProviderInConfigMap(kubectlBinary, kubeconfigPath, provider); err != nil {
		return fmt.Errorf("failed to update llmspy config: %w", err)
	}

	// 3. Restart the deployment so it picks up new Secret + ConfigMap
	u.Info("Restarting llmspy deployment")
	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "restart", fmt.Sprintf("deployment/%s", deployName), "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart llmspy: %w", err)
	}

	// 4. Wait for rollout to complete
	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "status", fmt.Sprintf("deployment/%s", deployName), "-n", namespace,
		"--timeout=60s"); err != nil {
		u.Warnf("llmspy rollout not confirmed: %v", err)
		u.Print("The deployment may still be rolling out.")
	} else {
		u.Successf("llmspy restarted with %s provider enabled", provider)
	}

	return nil
}

// getProviderEnvKey queries the llmspy pod for the env var name a provider uses.
// It reads the merged providers.json inside the pod (package defaults + ConfigMap overrides).
func getProviderEnvKey(kubectlBinary, kubeconfigPath, provider string) (string, error) {
	script := fmt.Sprintf(`import json
with open('/home/llms/.llms/providers.json') as f:
    d = json.load(f)
p = d.get('%s')
if p and p.get('env'):
    print(p['env'][0])
`, provider)

	output, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"exec", "-n", namespace, fmt.Sprintf("deploy/%s", deployName), "--",
		"python3", "-c", script)
	if err != nil {
		return "", fmt.Errorf("failed to query llmspy for provider %q: %w", provider, err)
	}
	return parseProviderEnvKey(provider, output)
}

// parseProviderEnvKey extracts an env var name from kubectl exec output.
func parseProviderEnvKey(provider, output string) (string, error) {
	envKey := strings.TrimSpace(output)
	if envKey == "" {
		return "", fmt.Errorf("unknown provider %q — run 'obol model status' to see available providers", provider)
	}
	return envKey, nil
}

// GetAvailableProviders queries the llmspy pod for all providers that accept an API key.
func GetAvailableProviders(cfg *config.Config) ([]ProviderInfo, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	script := `import json
with open('/home/llms/.llms/providers.json') as f:
    d = json.load(f)
for pid in sorted(d):
    p = d[pid]
    env = p.get('env', [])
    if env:
        print(pid + '\t' + p.get('name', pid) + '\t' + env[0])
`
	output, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"exec", "-n", namespace, fmt.Sprintf("deploy/%s", deployName), "--",
		"python3", "-c", script)
	if err != nil {
		return nil, fmt.Errorf("failed to query llmspy providers: %w", err)
	}

	return parseAvailableProviders(output), nil
}

// parseAvailableProviders parses tab-separated kubectl exec output into ProviderInfo slices.
func parseAvailableProviders(output string) []ProviderInfo {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	var providers []ProviderInfo
	for _, line := range strings.Split(trimmed, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		providers = append(providers, ProviderInfo{
			ID:     parts[0],
			Name:   parts[1],
			EnvVar: parts[2],
		})
	}
	return providers
}

// GetProviderStatus reads llmspy state and returns global provider status.
// It queries the llmspy pod for available providers and cross-references
// with the ConfigMap (enabled/disabled) and Secret (API keys).
func GetProviderStatus(cfg *config.Config) (map[string]ProviderStatus, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// Get all available providers from llmspy (with env var names)
	available, err := GetAvailableProviders(cfg)
	if err != nil {
		return nil, err
	}

	// Read enabled/disabled state from ConfigMap
	llmsRaw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.llms\\.json}")
	if err != nil {
		return nil, err
	}

	// Read Secret to check which API keys are set
	secretRaw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "secret", secretName, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}

	return buildProviderStatus(available, []byte(llmsRaw), []byte(secretRaw))
}

// buildProviderStatus is the pure logic for building provider status from raw data.
// available: providers discovered from the llmspy pod
// llmsJSON: the llms.json content from the ConfigMap
// secretJSON: the full Secret JSON (with base64-encoded .data)
func buildProviderStatus(available []ProviderInfo, llmsJSON, secretJSON []byte) (map[string]ProviderStatus, error) {
	envKeyByProvider := make(map[string]string)
	for _, p := range available {
		envKeyByProvider[p.ID] = p.EnvVar
	}

	var llmsConfig map[string]interface{}
	if err := json.Unmarshal(llmsJSON, &llmsConfig); err != nil {
		return nil, fmt.Errorf("failed to parse llms.json from ConfigMap: %w", err)
	}

	status := make(map[string]ProviderStatus)

	// Seed from ConfigMap providers (shows what's been configured)
	if providers, ok := llmsConfig["providers"].(map[string]interface{}); ok {
		for name, raw := range providers {
			enabled := false
			if p, ok := raw.(map[string]interface{}); ok {
				if v, ok := p["enabled"].(bool); ok {
					enabled = v
				}
			}
			status[name] = ProviderStatus{
				Enabled:   enabled,
				HasAPIKey: name == "ollama",
				EnvVar:    envKeyByProvider[name],
			}
		}
	}

	// Parse Secret
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(secretJSON, &secret); err != nil {
		return nil, fmt.Errorf("failed to parse llms secret: %w", err)
	}

	// Cross-reference Secret keys with provider env vars
	secretKeys := make(map[string]bool)
	for k, v := range secret.Data {
		if strings.TrimSpace(v) != "" {
			secretKeys[k] = true
		}
	}
	for name, st := range status {
		if st.EnvVar != "" && secretKeys[st.EnvVar] {
			st.HasAPIKey = true
			status[name] = st
		}
	}

	// Ensure Ollama always shows
	if _, ok := status["ollama"]; !ok {
		status["ollama"] = ProviderStatus{
			Enabled:   true,
			HasAPIKey: true,
		}
	}

	return status, nil
}

// enableProviderInConfigMap reads the llmspy-config ConfigMap, parses llms.json,
// sets providers.<name>.enabled = true, and patches the ConfigMap back.
func enableProviderInConfigMap(kubectlBinary, kubeconfigPath, provider string) error {
	// Read current llms.json from ConfigMap
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.llms\\.json}")
	if err != nil {
		return fmt.Errorf("failed to read ConfigMap: %w", err)
	}

	updated, err := patchLLMsJSON([]byte(raw), provider)
	if err != nil {
		return err
	}

	// Build ConfigMap patch
	patchData := map[string]interface{}{
		"data": map[string]string{
			"llms.json": string(updated),
		},
	}
	patchJSON, err := json.Marshal(patchData)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	return kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", string(patchJSON), "--type=merge")
}

// patchLLMsJSON takes raw llms.json content and returns updated JSON
// with providers.<name>.enabled = true.
func patchLLMsJSON(llmsJSON []byte, provider string) ([]byte, error) {
	var llmsConfig map[string]interface{}
	if err := json.Unmarshal(llmsJSON, &llmsConfig); err != nil {
		return nil, fmt.Errorf("failed to parse llms.json: %w", err)
	}

	providers, ok := llmsConfig["providers"].(map[string]interface{})
	if !ok {
		providers = make(map[string]interface{})
		llmsConfig["providers"] = providers
	}

	providerCfg, ok := providers[provider].(map[string]interface{})
	if !ok {
		providerCfg = make(map[string]interface{})
		providers[provider] = providerCfg
	}
	providerCfg["enabled"] = true

	return json.Marshal(llmsConfig)
}


// ollamaEndpoint returns the base URL where host Ollama should be reachable.
// It respects the OLLAMA_HOST environment variable, falling back to http://localhost:11434.
func ollamaEndpoint() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}
		return strings.TrimRight(host, "/")
	}
	return "http://localhost:11434"
}

// OllamaModel describes a model pulled in the local Ollama instance.
type OllamaModel struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// ListOllamaModels queries the local Ollama server for pulled models.
// Returns nil and an error if Ollama is not reachable.
func ListOllamaModels() ([]OllamaModel, error) {
	endpoint := ollamaEndpoint()
	tagsURL, err := url.JoinPath(endpoint, "api", "tags")
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama endpoint: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(tagsURL)
	if err != nil {
		return nil, fmt.Errorf("Ollama is not running at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama returned status %d", resp.StatusCode)
	}

	var result struct {
		Models []OllamaModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse Ollama response: %w", err)
	}
	return result.Models, nil
}

// PullOllamaModel pulls a model from the Ollama registry.
// It streams progress to stdout, matching the UX of `ollama pull`.
func PullOllamaModel(name string) error {
	endpoint := ollamaEndpoint()
	pullURL, err := url.JoinPath(endpoint, "api", "pull")
	if err != nil {
		return fmt.Errorf("invalid Ollama endpoint: %w", err)
	}

	// Check Ollama is reachable first
	client := &http.Client{Timeout: 3 * time.Second}
	healthResp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("Ollama is not running at %s — start it first", endpoint)
	}
	healthResp.Body.Close()

	// POST /api/pull with streaming response
	body, err := json.Marshal(map[string]interface{}{
		"name":   name,
		"stream": true,
	})
	if err != nil {
		return err
	}

	// Use a long timeout — model downloads can take a while
	pullClient := &http.Client{Timeout: 0}
	resp, err := pullClient.Post(pullURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to start pull: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errBody); err == nil && errBody.Error != "" {
			return fmt.Errorf("pull failed: %s", errBody.Error)
		}
		return fmt.Errorf("pull failed with status %d", resp.StatusCode)
	}

	// Stream NDJSON progress lines
	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for potentially large lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastStatus string
	for scanner.Scan() {
		var progress struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &progress); err != nil {
			continue
		}

		if progress.Error != "" {
			return fmt.Errorf("pull failed: %s", progress.Error)
		}

		if progress.Total > 0 && progress.Completed > 0 {
			pct := float64(progress.Completed) / float64(progress.Total) * 100
			fmt.Printf("\r  %s: %.0f%% (%s / %s)",
				progress.Status, pct,
				FormatBytes(progress.Completed), FormatBytes(progress.Total))
		} else if progress.Status != lastStatus {
			if lastStatus != "" {
				fmt.Println()
			}
			fmt.Printf("  %s", progress.Status)
			lastStatus = progress.Status
		}
	}
	fmt.Println()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading pull stream: %w", err)
	}

	return nil
}

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
