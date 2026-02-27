package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
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
	if err := kubectl(kubectlBinary, kubeconfigPath,
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
	if err := kubectl(kubectlBinary, kubeconfigPath,
		"rollout", "restart", fmt.Sprintf("deployment/%s", deployName), "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart llmspy: %w", err)
	}

	// 4. Wait for rollout to complete
	if err := kubectl(kubectlBinary, kubeconfigPath,
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

	output, err := kubectlOutput(kubectlBinary, kubeconfigPath,
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
	output, err := kubectlOutput(kubectlBinary, kubeconfigPath,
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
	llmsRaw, err := kubectlOutput(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.llms\\.json}")
	if err != nil {
		return nil, err
	}

	// Read Secret to check which API keys are set
	secretRaw, err := kubectlOutput(kubectlBinary, kubeconfigPath,
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
	raw, err := kubectlOutput(kubectlBinary, kubeconfigPath,
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

	return kubectl(kubectlBinary, kubeconfigPath,
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

// kubectl runs a kubectl command with the given kubeconfig and returns any error.
func kubectl(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

func kubectlOutput(binary, kubeconfig string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%w: %s", err, errMsg)
		}
		return "", err
	}
	return stdout.String(), nil
}
