package model

import (
	"bufio"
	"bytes"
	encoding_base64 "encoding/base64"
	"encoding/json"
	"errors"
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
	"gopkg.in/yaml.v3"
)

const (
	namespace     = "llm"
	secretName    = "litellm-secrets"
	configMapName = "litellm-config"
	deployName    = "litellm"

	// Provider name constants used in model routing and configuration.
	ProviderOllama    = "ollama"
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// Known provider definitions — no need to query the running pod.
var knownProviders = []ProviderInfo{
	{ID: ProviderAnthropic, Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY", AltEnvVars: []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
	{ID: ProviderOpenAI, Name: "OpenAI", EnvVar: "OPENAI_API_KEY"},
	{ID: ProviderOllama, Name: "Ollama (local)", EnvVar: ""},
}

// ProviderInfo describes an LLM provider.
type ProviderInfo struct {
	ID         string   // provider id (e.g. "anthropic", "openai", "ollama")
	Name       string   // display name
	EnvVar     string   // primary env var for API key (empty for Ollama)
	AltEnvVars []string // fallback env vars checked in order (e.g. CLAUDE_CODE_OAUTH_TOKEN)
}

// ProviderStatus captures effective global LiteLLM provider state.
type ProviderStatus struct {
	Enabled   bool
	HasAPIKey bool
	EnvVar    string // environment variable name (e.g. ANTHROPIC_API_KEY)
	Models    []string
}

// LiteLLMConfig represents the LiteLLM proxy config.yaml structure.
type LiteLLMConfig struct {
	ModelList       []ModelEntry   `yaml:"model_list"`
	GeneralSettings map[string]any `yaml:"general_settings,omitempty"`
	LiteLLMSettings map[string]any `yaml:"litellm_settings,omitempty"`
}

// ModelEntry is a single entry in model_list.
type ModelEntry struct {
	ModelName     string        `yaml:"model_name"`
	LiteLLMParams LiteLLMParams `yaml:"litellm_params"`
}

// LiteLLMParams holds the routing parameters for a model.
type LiteLLMParams struct {
	Model   string `yaml:"model"`
	APIBase string `yaml:"api_base,omitempty"`
	APIKey  string `yaml:"api_key,omitempty"`
}

// HasConfiguredModels returns true if LiteLLM has at least one non-catch-all
// model configured (i.e., something other than the "paid/*" route).
func HasConfiguredModels(cfg *config.Config) bool {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return false
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return false
	}

	for _, entry := range litellmConfig.ModelList {
		if !strings.Contains(entry.ModelName, "*") {
			return true
		}
	}

	return false
}

// HasProviderConfigured returns true if LiteLLM already has at least one
// model entry for the given provider (e.g., "anthropic", "openai").
func HasProviderConfigured(cfg *config.Config, provider string) bool {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return false
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return false
	}

	for _, entry := range litellmConfig.ModelList {
		// Check wildcard entries like "anthropic/*"
		if entry.ModelName == provider+"/*" {
			return true
		}
		// Check if the model's litellm_params.model starts with "provider/"
		if strings.HasPrefix(entry.LiteLLMParams.Model, provider+"/") {
			return true
		}
		// Check via model name inference
		if ProviderFromModelName(entry.ModelName) == provider {
			return true
		}
	}

	return false
}

// LoadDotEnv reads KEY=value pairs from a .env file.
// Returns an empty map if the file doesn't exist or is unreadable.
// Skips comments (#) and blank lines. Does not call os.Setenv.
func LoadDotEnv(path string) map[string]string {
	result := make(map[string]string)

	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		result[key] = val
	}

	return result
}

// ConfigureLiteLLM adds a provider to the LiteLLM gateway.
// For cloud providers, it patches the Secret with the API key and adds
// the model to config.yaml. For Ollama, it discovers local models and adds them.
//
// When only models change (no API key), models are hot-added via the
// /model/new API — no restart required. When API keys change, a rolling
// restart is triggered so the new Secret values are picked up.
func ConfigureLiteLLM(cfg *config.Config, u *ui.UI, provider, apiKey string, models []string) error {
	if err := PatchLiteLLMProvider(cfg, u, provider, apiKey, models); err != nil {
		return err
	}

	// API key changes require a restart (Secret mounted as envFrom).
	// Model-only changes can be hot-added via the /model/new API.
	needsRestart := apiKey != ""
	if needsRestart {
		return RestartLiteLLM(cfg, u, provider)
	}

	entries := buildModelEntries(provider, models)
	if err := hotAddModels(cfg, u, entries); err != nil {
		u.Warnf("Hot-add failed, falling back to restart: %v", err)
		return RestartLiteLLM(cfg, u, provider)
	}

	u.Successf("LiteLLM configured with %s provider", provider)
	return nil
}

// PatchLiteLLMProvider patches the LiteLLM Secret (API key) and ConfigMap
// (model_list) for a provider without restarting the deployment. Call
// RestartLiteLLM afterwards (once, after batching multiple providers).
func PatchLiteLLMProvider(cfg *config.Config, u *ui.UI, provider, apiKey string, models []string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	// 1. Patch Secret with API key (if cloud provider)
	envVar := ProviderEnvVar(provider)
	if envVar != "" && apiKey != "" {
		u.Infof("Setting %s API key", provider)

		patchJSON := fmt.Sprintf(`{"stringData":{"%s":"%s"}}`, envVar, apiKey)
		if err := kubectl.Run(kubectlBinary, kubeconfigPath,
			"patch", "secret", secretName, "-n", namespace,
			"-p", patchJSON, "--type=merge"); err != nil {
			return fmt.Errorf("failed to patch secret: %w", err)
		}
	}

	// 2. Build model entries
	entries := buildModelEntries(provider, models)
	if len(entries) == 0 {
		return fmt.Errorf("no models to configure for provider %q", provider)
	}

	// 3. Patch ConfigMap with new model_list entries
	u.Infof("Adding %d model(s) to LiteLLM config", len(entries))

	if err := patchLiteLLMConfig(kubectlBinary, kubeconfigPath, entries); err != nil {
		return fmt.Errorf("failed to update LiteLLM config: %w", err)
	}

	return nil
}

// RestartLiteLLM restarts the LiteLLM deployment and waits for rollout.
func RestartLiteLLM(cfg *config.Config, u *ui.UI, provider string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	u.Info("Restarting LiteLLM")

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "restart", "deployment/"+deployName, "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart LiteLLM: %w", err)
	}

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "status", "deployment/"+deployName, "-n", namespace,
		"--timeout=90s"); err != nil {
		u.Warnf("LiteLLM rollout not confirmed: %v", err)
		u.Print("The deployment may still be rolling out.")
	} else {
		u.Successf("LiteLLM configured with %s provider", provider)
	}

	return nil
}

// hotAddModels uses the LiteLLM /model/new API to add models to the running
// router without a restart. The ConfigMap is already patched by
// PatchLiteLLMProvider for persistence across restarts.
func hotAddModels(cfg *config.Config, u *ui.UI, entries []ModelEntry) error {
	masterKey, err := GetMasterKey(cfg)
	if err != nil {
		return fmt.Errorf("get master key: %w", err)
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Get the LiteLLM ClusterIP for direct access.
	svcIP, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "svc", deployName, "-n", namespace,
		"-o", "jsonpath={.spec.clusterIP}")
	if err != nil || strings.TrimSpace(svcIP) == "" {
		return fmt.Errorf("get litellm service IP: %w", err)
	}

	// Use kubectl exec to call the API from inside the cluster (avoids
	// port-forward complexity and works on any host OS).
	for _, entry := range entries {
		body := map[string]any{
			"model_name": entry.ModelName,
			"litellm_params": map[string]any{
				"model":    entry.LiteLLMParams.Model,
				"api_base": entry.LiteLLMParams.APIBase,
				"api_key":  entry.LiteLLMParams.APIKey,
			},
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			continue
		}

		// POST /model/new via kubectl exec on a running litellm pod, invoking wget directly.
		out, err := kubectl.Output(kubectlBinary, kubeconfigPath,
			"exec", "-n", namespace, "deployment/"+deployName, "-c", "litellm",
			"--",
			"wget", "-qO-",
			"--post-data="+string(bodyJSON),
			"--header=Content-Type: application/json",
			"--header=Authorization: Bearer "+masterKey,
			"http://localhost:4000/model/new",
		)
		if err != nil {
			u.Warnf("Hot-add %s failed: %v (%s)", entry.ModelName, err, strings.TrimSpace(out))
			return fmt.Errorf("hot-add %s: %w", entry.ModelName, err)
		}
	}

	return nil
}

// RemoveModel removes a model entry from the LiteLLM ConfigMap and restarts the deployment.
func RemoveModel(cfg *config.Config, u *ui.UI, modelName string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	// Read current config
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return fmt.Errorf("failed to read LiteLLM config: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return fmt.Errorf("failed to parse config.yaml: %w", err)
	}

	// Find and remove matching entries
	var kept []ModelEntry

	removed := 0

	for _, entry := range litellmConfig.ModelList {
		if entry.ModelName == modelName {
			removed++
			continue
		}

		kept = append(kept, entry)
	}

	if removed == 0 {
		return fmt.Errorf("model %q not found in LiteLLM config", modelName)
	}

	litellmConfig.ModelList = kept

	// Marshal back to YAML
	updated, err := yaml.Marshal(&litellmConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Build ConfigMap patch
	escapedYAML, err := json.Marshal(string(updated))
	if err != nil {
		return fmt.Errorf("failed to escape YAML: %w", err)
	}

	patchJSON := fmt.Sprintf(`{"data":{"config.yaml":%s}}`, escapedYAML)

	u.Infof("Removing model %q from LiteLLM config", modelName)

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", patchJSON, "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch ConfigMap: %w", err)
	}

	// Restart deployment
	u.Info("Restarting LiteLLM")

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "restart", "deployment/"+deployName, "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart LiteLLM: %w", err)
	}

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "status", "deployment/"+deployName, "-n", namespace,
		"--timeout=90s"); err != nil {
		u.Warnf("LiteLLM rollout not confirmed: %v", err)
	} else {
		u.Successf("Model %q removed", modelName)
	}

	return nil
}

// AddCustomEndpoint adds a custom OpenAI-compatible endpoint to LiteLLM
// after validating it works.
func AddCustomEndpoint(cfg *config.Config, u *ui.UI, name, endpoint, modelName, apiKey string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	// Validate the endpoint from the host (use localhost-reachable URL)
	u.Info("Validating custom endpoint...")

	validationEndpoint := endpoint
	// If the user gave a k3d-internal URL, translate for host validation
	validationEndpoint = strings.Replace(validationEndpoint, "host.k3d.internal", "localhost", 1)

	validationEndpoint = strings.Replace(validationEndpoint, "host.docker.internal", "localhost", 1)
	if err := ValidateCustomEndpoint(validationEndpoint, modelName, apiKey); err != nil {
		return fmt.Errorf("endpoint validation failed: %w", err)
	}

	u.Success("Endpoint validated successfully")

	// For the cluster ConfigMap, translate localhost to k3d-internal
	clusterEndpoint := localhostToClusterEndpoint(endpoint)
	if clusterEndpoint != endpoint {
		u.Infof("Cluster endpoint: %s (translated from %s)", clusterEndpoint, endpoint)
	}

	// Build model entry
	litellmModel := "openai/" + modelName
	modelID := fmt.Sprintf("custom/%s/%s", name, modelName)

	entry := ModelEntry{
		ModelName: modelID,
		LiteLLMParams: LiteLLMParams{
			Model:   litellmModel,
			APIBase: clusterEndpoint,
			APIKey:  apiKey,
		},
	}
	if apiKey == "" {
		entry.LiteLLMParams.APIKey = "none"
	}

	// Patch config
	u.Infof("Adding custom endpoint %q to LiteLLM config", name)

	if err := patchLiteLLMConfig(kubectlBinary, kubeconfigPath, []ModelEntry{entry}); err != nil {
		return fmt.Errorf("failed to update LiteLLM config: %w", err)
	}

	// Restart
	u.Info("Restarting LiteLLM")

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "restart", "deployment/"+deployName, "-n", namespace); err != nil {
		return fmt.Errorf("failed to restart LiteLLM: %w", err)
	}

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"rollout", "status", "deployment/"+deployName, "-n", namespace,
		"--timeout=90s"); err != nil {
		u.Warnf("LiteLLM rollout not confirmed: %v", err)
	} else {
		u.Successf("Custom endpoint %q added (model: %s)", name, modelID)
	}

	return nil
}

// ValidateCustomEndpoint validates that a custom OpenAI-compatible endpoint works.
// It runs a 2-step validation: reachability check, then inference probe.
// The inference probe is the definitive test — some servers (e.g., mlx-lm) don't
// list the loaded model in /models but accept it for inference.
func ValidateCustomEndpoint(endpoint, modelName, apiKey string) error {
	client := &http.Client{Timeout: 60 * time.Second}

	authHeader := ""
	if apiKey != "" {
		authHeader = "Bearer " + apiKey
	}

	// Step 1: Reachability check — try /models, /health, or / (in that order)
	base := strings.TrimRight(endpoint, "/")
	reachable := false

	for _, path := range []string{"/models", "/health", ""} {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			continue
		}

		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		resp.Body.Close()

		if resp.StatusCode < 500 {
			reachable = true
			break
		}
	}

	if !reachable {
		return fmt.Errorf("endpoint unreachable — cannot connect to %s", base)
	}

	// Step 2: Inference probe — the definitive test
	probePayload, _ := json.Marshal(map[string]any{ //nolint:errchkjson // map[string]any is safe, keys/values are controlled
		"model":      modelName,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	})
	completionsURL := strings.TrimRight(endpoint, "/") + "/chat/completions"

	probeReq, err := http.NewRequest(http.MethodPost, completionsURL, bytes.NewReader(probePayload))
	if err != nil {
		return fmt.Errorf("failed to build inference probe: %w", err)
	}

	probeReq.Header.Set("Content-Type", "application/json")

	if authHeader != "" {
		probeReq.Header.Set("Authorization", authHeader)
	}

	probeResp, err := client.Do(probeReq)
	if err != nil {
		return fmt.Errorf("inference probe failed — cannot reach %s: %w", completionsURL, err)
	}
	defer probeResp.Body.Close()

	if probeResp.StatusCode != http.StatusOK {
		return fmt.Errorf("inference probe failed — %s returned %d", completionsURL, probeResp.StatusCode)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(probeResp.Body).Decode(&chatResp); err != nil {
		return fmt.Errorf("inference probe returned invalid response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return errors.New("inference probe returned empty choices array")
	}

	return nil
}

// GetAvailableProviders returns the known provider list (static, no pod query needed).
func GetAvailableProviders(_ *config.Config) ([]ProviderInfo, error) {
	return knownProviders, nil
}

// GetProviderStatus reads LiteLLM config and returns provider status.
func GetProviderStatus(cfg *config.Config) (map[string]ProviderStatus, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("cluster not running. Run 'obol stack up' first")
	}

	// Read config.yaml from ConfigMap
	configRaw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return nil, fmt.Errorf("failed to read LiteLLM config: %w", err)
	}

	// Read Secret
	secretRaw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "secret", secretName, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to read LiteLLM secret: %w", err)
	}

	return buildProviderStatus([]byte(configRaw), []byte(secretRaw))
}

// buildProviderStatus constructs provider status from raw config.yaml and secret JSON.
func buildProviderStatus(configYAML, secretJSON []byte) (map[string]ProviderStatus, error) {
	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal(configYAML, &litellmConfig); err != nil {
		return nil, fmt.Errorf("failed to parse LiteLLM config: %w", err)
	}

	// Parse Secret
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(secretJSON, &secret); err != nil {
		return nil, fmt.Errorf("failed to parse secret: %w", err)
	}

	secretKeys := make(map[string]bool)

	for k, v := range secret.Data {
		if strings.TrimSpace(v) != "" {
			secretKeys[k] = true
		}
	}

	// Build status from model_list
	status := make(map[string]ProviderStatus)

	for _, entry := range litellmConfig.ModelList {
		provider := detectProvider(entry)
		st := status[provider]
		st.Enabled = true
		st.Models = append(st.Models, entry.ModelName)
		status[provider] = st
	}

	// Add env var info and API key status
	for _, p := range knownProviders {
		st := status[p.ID]

		st.EnvVar = p.EnvVar
		if p.EnvVar != "" && secretKeys[p.EnvVar] {
			st.HasAPIKey = true
		}

		if p.ID == ProviderOllama {
			st.HasAPIKey = true // Ollama doesn't need a key
		}

		status[p.ID] = st
	}

	return status, nil
}

// GetMasterKey reads the LiteLLM master key from the cluster Secret.
func GetMasterKey(cfg *config.Config) (string, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("cluster not running")
	}

	key, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "secret", secretName, "-n", namespace,
		"-o", "jsonpath={.data.LITELLM_MASTER_KEY}")
	if err != nil {
		return "", err
	}
	// The value is base64-encoded in .data
	decoded, err := decodeBase64(strings.TrimSpace(key))
	if err != nil {
		return key, nil //nolint:nilerr // secret may be stored unencoded; fall back to raw value
	}

	return decoded, nil
}

// GetConfiguredModels returns the model names available in LiteLLM.
// Wildcard entries (e.g. anthropic/*) are expanded: first by querying
// the running LiteLLM pod's /v1/models endpoint, falling back to the
// baked-in WellKnownModels list if the cluster is unreachable.
func GetConfiguredModels(cfg *config.Config) ([]string, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("cluster not running")
	}

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return nil, fmt.Errorf("failed to read LiteLLM config: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Try live query first for accurate model list
	liveModels := queryLiteLLMModels(kubectlBinary, kubeconfigPath)

	var models []string

	seen := make(map[string]bool)

	for _, entry := range litellmConfig.ModelList {
		name := entry.ModelName
		if before, ok := strings.CutSuffix(name, "/*"); ok {
			// Expand wildcard: prefer live models, fall back to well-known
			provider := before

			expanded := expandWildcard(provider, liveModels)
			for _, m := range expanded {
				if !seen[m] {
					models = append(models, m)
					seen[m] = true
				}
			}

			continue
		}

		if !seen[name] {
			models = append(models, name)
			seen[name] = true
		}
	}

	return models, nil
}

// queryLiteLLMModels fetches the model list from the running LiteLLM pod
// via kubectl port-forward. Returns nil if unavailable.
func queryLiteLLMModels(kubectlBinary, kubeconfigPath string) []string {
	// Use kubectl exec to query from inside the cluster (avoids port-forward)
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"exec", "-n", namespace, "deployment/"+deployName, "--",
		"curl", "-sf", "http://localhost:4000/v1/models")
	if err != nil {
		return nil
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil
	}

	var models []string
	for _, m := range resp.Data {
		models = append(models, m.ID)
	}

	return models
}

// expandWildcard returns model names for a provider wildcard.
// Uses live models if available, otherwise falls back to WellKnownModels.
func expandWildcard(provider string, liveModels []string) []string {
	// Filter live models that match this provider
	if len(liveModels) > 0 {
		var matched []string

		for _, m := range liveModels {
			p := ProviderFromModelName(m)
			if p == provider {
				matched = append(matched, m)
			}
		}

		if len(matched) > 0 {
			return matched
		}
	}
	// Fallback to well-known list
	if known, ok := WellKnownModels[provider]; ok {
		return known
	}

	return nil
}

// ProviderFromModelName infers the provider from a model name string.
func ProviderFromModelName(name string) string {
	if strings.Contains(name, "claude") {
		return ProviderAnthropic
	}

	if strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") || strings.HasPrefix(name, "o4") {
		return ProviderOpenAI
	}

	return ""
}

// --- Internal helpers ---

// ResolveAPIKey checks the primary env var and each AltEnvVar in order for
// the given provider. Returns the key value and the env var it was found in.
// Both are empty if no key is available.
func ResolveAPIKey(provider string) (key, envVarUsed string) {
	for _, p := range knownProviders {
		if p.ID != provider {
			continue
		}

		if p.EnvVar != "" {
			if v := os.Getenv(p.EnvVar); v != "" {
				return v, p.EnvVar
			}
		}

		for _, alt := range p.AltEnvVars {
			if v := os.Getenv(alt); v != "" {
				return v, alt
			}
		}

		return "", ""
	}

	return "", ""
}

// ProviderEnvVar returns the env var name for a provider's API key.
func ProviderEnvVar(provider string) string {
	for _, p := range knownProviders {
		if p.ID == provider {
			return p.EnvVar
		}
	}

	return strings.ToUpper(provider) + "_API_KEY"
}

// WellKnownModels maps provider names to their commonly-used model IDs.
// Used to populate OpenClaw's model allowlist when a wildcard is configured
// and the LiteLLM pod is not reachable for a live /v1/models query.
var WellKnownModels = map[string][]string{
	ProviderAnthropic: {
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		"claude-sonnet-4-5-20250929",
	},
	ProviderOpenAI: {
		"gpt-5.4",
		"gpt-4.1",
		"gpt-4.1-mini",
		"o4-mini",
		"o3",
	},
}

// buildModelEntries creates LiteLLM model_list entries for a provider.
// Cloud providers (anthropic, openai) get a wildcard entry plus explicit
// entries for the requested models. Ollama gets explicit entries only
// (wildcards are broken for ollama_chat/).
func buildModelEntries(provider string, models []string) []ModelEntry {
	var entries []ModelEntry

	switch provider {
	case ProviderOllama:
		// Explicit entries — ollama_chat/* wildcards are broken in LiteLLM
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:   "ollama_chat/" + m,
					APIBase: "http://ollama.llm.svc.cluster.local:11434",
				},
			})
		}
	case ProviderAnthropic:
		// Wildcard: routes any anthropic model without explicit registration
		entries = append(entries, ModelEntry{
			ModelName:     "anthropic/*",
			LiteLLMParams: LiteLLMParams{Model: "anthropic/*", APIKey: "os.environ/ANTHROPIC_API_KEY"},
		})
		// Explicit entries for requested models (better /v1/models listing)
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName:     m,
				LiteLLMParams: LiteLLMParams{Model: m, APIKey: "os.environ/ANTHROPIC_API_KEY"},
			})
		}
	case ProviderOpenAI:
		entries = append(entries, ModelEntry{
			ModelName:     "openai/*",
			LiteLLMParams: LiteLLMParams{Model: "openai/*", APIKey: "os.environ/OPENAI_API_KEY"},
		})
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName:     m,
				LiteLLMParams: LiteLLMParams{Model: "openai/" + m, APIKey: "os.environ/OPENAI_API_KEY"},
			})
		}
	default:
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:  provider + "/" + m,
					APIKey: fmt.Sprintf("os.environ/%s_API_KEY", strings.ToUpper(provider)),
				},
			})
		}
	}

	return entries
}

// patchLiteLLMConfig reads the current config.yaml from the ConfigMap,
// merges new model entries (replacing existing by model_name), and patches back.
func patchLiteLLMConfig(kubectlBinary, kubeconfigPath string, entries []ModelEntry) error {
	// Read current config
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return fmt.Errorf("failed to read ConfigMap: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return fmt.Errorf("failed to parse config.yaml: %w", err)
	}

	// Merge: replace existing entries by model_name, append new ones
	existing := make(map[string]int) // model_name → index
	for i, e := range litellmConfig.ModelList {
		existing[e.ModelName] = i
	}

	for _, entry := range entries {
		if idx, ok := existing[entry.ModelName]; ok {
			litellmConfig.ModelList[idx] = entry
		} else {
			litellmConfig.ModelList = append(litellmConfig.ModelList, entry)
		}
	}

	// Marshal back to YAML
	updated, err := yaml.Marshal(&litellmConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Build ConfigMap patch — escape the YAML for JSON embedding
	escapedYAML, err := json.Marshal(string(updated))
	if err != nil {
		return fmt.Errorf("failed to escape YAML: %w", err)
	}

	patchJSON := fmt.Sprintf(`{"data":{"config.yaml":%s}}`, escapedYAML)

	return kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", patchJSON, "--type=merge")
}

// detectProvider infers the provider name from a model_list entry.
func detectProvider(entry ModelEntry) string {
	if strings.HasPrefix(entry.ModelName, "custom/") {
		return "custom"
	}

	if strings.HasPrefix(entry.ModelName, "paid/") {
		return "paid"
	}

	model := entry.LiteLLMParams.Model
	// Wildcard entries
	if strings.HasPrefix(model, ProviderAnthropic+"/") {
		return ProviderAnthropic
	}

	if strings.HasPrefix(model, ProviderOllama+"/") || strings.HasPrefix(model, "ollama_chat/") {
		return ProviderOllama
	}

	if strings.HasPrefix(model, ProviderOpenAI+"/") {
		return ProviderOpenAI
	}
	// Anthropic models without prefix
	if strings.Contains(model, "claude") {
		return ProviderAnthropic
	}

	if strings.HasPrefix(model, "gpt") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") {
		return ProviderOpenAI
	}

	return "unknown"
}

func decodeBase64(s string) (string, error) {
	decoded := make([]byte, len(s))

	n, err := encoding_base64.StdEncoding.Decode(decoded, []byte(s))
	if err != nil {
		return "", err
	}

	return string(decoded[:n]), nil
}

// WarnAndStripV1Suffix checks if an endpoint URL has a trailing /v1 suffix,
// warns the user, and returns the stripped URL. For OpenAI-compatible providers,
// LiteLLM auto-appends /v1, causing double /v1/v1 if the user includes it.
func WarnAndStripV1Suffix(endpoint string) string {
	trimmed := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		fmt.Printf("  Warning: stripping trailing /v1 from endpoint URL (LiteLLM adds it automatically)\n")
		fmt.Printf("  %s → %s\n", trimmed, strings.TrimSuffix(trimmed, "/v1"))

		return strings.TrimSuffix(trimmed, "/v1")
	}

	return endpoint
}

// localhostToClusterEndpoint translates localhost URLs to k3d-internal URLs
// so that services running on the host are reachable from inside the k3d cluster.
func localhostToClusterEndpoint(endpoint string) string {
	for _, local := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if strings.Contains(endpoint, local) {
			return strings.Replace(endpoint, local, "host.k3d.internal", 1)
		}
	}

	return endpoint
}

// --- Ollama helpers (unchanged) ---

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
		return nil, fmt.Errorf("ollama is not running at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
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
		return fmt.Errorf("ollama is not running at %s — start it first", endpoint)
	}

	healthResp.Body.Close()

	// POST /api/pull with streaming response
	body, err := json.Marshal(map[string]any{
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
