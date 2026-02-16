package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const (
	namespace     = "llm"
	secretName    = "llms-secrets"
	configMapName = "llmspy-config"
	deployName    = "llmspy"
)

// providerEnvKeys maps provider names to their Secret key names.
var providerEnvKeys = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
}

// ProviderStatus captures effective global llmspy provider state.
type ProviderStatus struct {
	Enabled   bool
	HasAPIKey bool
	APIKeyEnv string
}

// ConfigureLLMSpy enables a cloud provider in the llmspy gateway.
// It patches the llms-secrets Secret with the API key, enables the provider
// in the llmspy-config ConfigMap, and restarts the deployment.
func ConfigureLLMSpy(cfg *config.Config, provider, apiKey string) error {
	envKey, ok := providerEnvKeys[provider]
	if !ok {
		return fmt.Errorf("unsupported llmspy provider: %s (supported: anthropic, openai)", provider)
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// 1. Patch the Secret with the API key
	fmt.Printf("Configuring llmspy: setting %s key...\n", provider)
	patchJSON := fmt.Sprintf(`{"stringData":{"%s":"%s"}}`, envKey, apiKey)
	if err := kubectl(kubectlBinary, kubeconfigPath,
		"patch", "secret", secretName, "-n", namespace,
		"-p", patchJSON, "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch llmspy secret: %w", err)
	}

	// 2. Read current ConfigMap, enable the provider in llms.json
	fmt.Printf("Enabling %s provider in llmspy config...\n", provider)
	if err := enableProviderInConfigMap(kubectlBinary, kubeconfigPath, provider); err != nil {
		return fmt.Errorf("failed to update llmspy config: %w", err)
	}

	// 3. Restart the deployment so it picks up new Secret + ConfigMap
	fmt.Printf("Restarting llmspy deployment...\n")
	if err := kubectl(kubectlBinary, kubeconfigPath,
		"rollout", "restart", fmt.Sprintf("deployment/%s", deployName), "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart llmspy: %w", err)
	}

	// 4. Wait for rollout to complete
	if err := kubectl(kubectlBinary, kubeconfigPath,
		"rollout", "status", fmt.Sprintf("deployment/%s", deployName), "-n", namespace,
		"--timeout=60s"); err != nil {
		fmt.Printf("Warning: llmspy rollout not confirmed: %v\n", err)
		fmt.Println("The deployment may still be rolling out.")
	} else {
		fmt.Printf("llmspy restarted with %s provider enabled.\n", provider)
	}

	return nil
}

// GetProviderStatus reads llmspy ConfigMap + Secret and returns global provider status.
func GetProviderStatus(cfg *config.Config) (map[string]ProviderStatus, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	llmsRaw, err := kubectlOutput(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.llms\\.json}")
	if err != nil {
		return nil, err
	}
	var llmsConfig map[string]interface{}
	if err := json.Unmarshal([]byte(llmsRaw), &llmsConfig); err != nil {
		return nil, fmt.Errorf("failed to parse llms.json from ConfigMap: %w", err)
	}

	status := make(map[string]ProviderStatus)
	if providers, ok := llmsConfig["providers"].(map[string]interface{}); ok {
		for name, raw := range providers {
			enabled := false
			if p, ok := raw.(map[string]interface{}); ok {
				if v, ok := p["enabled"].(bool); ok {
					enabled = v
				}
			}
			keyEnv := providerEnvKeys[name]
			status[name] = ProviderStatus{
				Enabled: enabled,
				// Ollama needs no API key, so it's always considered "has key".
				// Cloud providers are updated below from the actual K8s Secret.
				HasAPIKey: name == "ollama",
				APIKeyEnv: keyEnv,
			}
		}
	}

	secretRaw, err := kubectlOutput(kubectlBinary, kubeconfigPath,
		"get", "secret", secretName, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(secretRaw), &secret); err != nil {
		return nil, fmt.Errorf("failed to parse llms secret: %w", err)
	}

	for provider, envKey := range providerEnvKeys {
		st := status[provider]
		st.APIKeyEnv = envKey
		if v, ok := secret.Data[envKey]; ok && strings.TrimSpace(v) != "" {
			st.HasAPIKey = true
		}
		status[provider] = st
	}

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
	var stdout bytes.Buffer
	cmd := exec.Command(kubectlBinary, "get", "configmap", configMapName,
		"-n", namespace, "-o", "jsonpath={.data.llms\\.json}")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to read ConfigMap: %w\n%s", err, stderr.String())
	}

	// Parse JSON
	var llmsConfig map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &llmsConfig); err != nil {
		return fmt.Errorf("failed to parse llms.json: %w", err)
	}

	// Set providers.<name>.enabled = true
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

	// Marshal back to JSON
	updated, err := json.Marshal(llmsConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal llms.json: %w", err)
	}

	// Patch ConfigMap
	// Use strategic merge patch with the new llms.json
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
