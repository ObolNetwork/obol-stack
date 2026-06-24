package model

import (
	"bufio"
	"bytes"
	"context"
	encoding_base64 "encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

// apiMode selects how a provider's LiteLLM model_list entries are shaped.
type apiMode string

const (
	// modeAnthropic: native LiteLLM anthropic routing + prompt-cache markers
	// + an anthropic/* wildcard. Key read from EnvVar.
	modeAnthropic apiMode = "anthropic"
	// modeOpenAI: native LiteLLM openai/ routing + an openai/* wildcard.
	modeOpenAI apiMode = "openai"
	// modeOllama: local ollama_chat/ entries pointed at the in-cluster Ollama.
	modeOllama apiMode = "ollama"
	// modeOpenAICompatible: any OpenAI-compatible BYOK aggregator (OpenRouter,
	// Venice, NVIDIA, …). Explicit entries only, Model="openai/<id>" with an
	// explicit api_base = BaseURL and key read from EnvVar. No wildcard:
	// aggregator namespaces are huge and overlapping, so we register only the
	// models the operator asked for.
	modeOpenAICompatible apiMode = "openai-compatible"
)

// ProviderInfo describes an LLM provider. knownProviders is the single
// source of truth: adding a provider is one row here, and every layer (the
// setup CLI, default-model selection, LiteLLM entry shaping, status, and
// the persisted record) reads from this struct instead of a per-provider
// switch.
type ProviderInfo struct {
	ID         string   // provider id (e.g. "anthropic", "openai", "venice")
	Name       string   // display name
	EnvVar     string   // primary env var for API key (empty for Ollama)
	AltEnvVars []string // fallback env vars checked in order (e.g. CLAUDE_CODE_OAUTH_TOKEN)
	Mode       apiMode  // how model_list entries are shaped
	BaseURL    string   // OpenAI-compatible base_url (modeOpenAICompatible only)
	Default    string   // default chat model when --model is omitted ("" = ask/require)
	KeyURL     string   // where to create an API key (assumes existing account)
	JoinURL    string   // optional new-user landing page (may carry a referral
	// tag). When set, `obol model setup` opens this in preference to KeyURL
	// (browser open) and surfaces it as a "new to X? sign up" Dim hint above
	// the keys-dashboard hint.
	Free []string // curated zero-marginal-cost model ids (seeded by --free)
}

// IsBYOK reports whether the provider is a BYOK OpenAI-compatible
// aggregator reached over the public internet (as opposed to a native
// provider or the local Ollama).
func (p ProviderInfo) IsBYOK() bool { return p.Mode == modeOpenAICompatible }

// knownProviders is the registry of supported LLM providers. The first
// three are native/local; the rest are BYOK OpenAI-compatible aggregators —
// each is pure data, no bespoke wiring. base_url values are intentionally
// without a trailing /v1 where LiteLLM appends it; aggregator paths that
// already include /v1 keep it (LiteLLM only auto-appends for bare hosts).
var knownProviders = []ProviderInfo{
	{
		ID: ProviderAnthropic, Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY",
		AltEnvVars: []string{"CLAUDE_CODE_OAUTH_TOKEN"}, Mode: modeAnthropic,
		Default: "claude-sonnet-4-6", KeyURL: "https://console.anthropic.com/settings/keys",
	},
	{
		ID: ProviderOpenAI, Name: "OpenAI", EnvVar: "OPENAI_API_KEY", Mode: modeOpenAI,
		Default: "gpt-5.5", KeyURL: "https://platform.openai.com/api-keys",
	},
	{
		ID: ProviderOllama, Name: "Ollama (local)", EnvVar: "", Mode: modeOllama,
	},
	// ── BYOK OpenAI-compatible aggregators (the easy getting-started path) ──
	// model_list entries are pure data: Model="openai/<id>", api_base=BaseURL,
	// key from EnvVar. Default models that can't be statically pinned (the
	// aggregator's catalog rotates) are left blank — setup then resolves a
	// model from the live /v1/models list or --model.
	{
		ID: "venice", Name: "Venice", EnvVar: "VENICE_API_KEY", Mode: modeOpenAICompatible,
		BaseURL: "https://api.venice.ai/api/v1",
		KeyURL:  "https://venice.ai/settings/api",
		JoinURL: "https://venice.ai/chat?ref=ZynMuD",
	},
	{
		ID: "openrouter", Name: "OpenRouter", EnvVar: "OPENROUTER_API_KEY", Mode: modeOpenAICompatible,
		BaseURL: "https://openrouter.ai/api/v1", Default: "openrouter/auto",
		KeyURL: "https://openrouter.ai/keys",
		// Curated zero-cost models (snapshot — OpenRouter's free roster
		// rotates; pass --model for any other). Seeded by `--free`.
		Free: []string{
			"openrouter/elephant-alpha",
			"openrouter/owl-alpha",
			"poolside/laguna-m.1:free",
			"tencent/hy3-preview:free",
			"nvidia/nemotron-3-super-120b-a12b:free",
			"nvidia/nemotron-3-ultra-550b-a55b:free",
			"inclusionai/ring-2.6-1t:free",
		},
	},
	{
		ID: "nvidia", Name: "NVIDIA NIM", EnvVar: "NVIDIA_API_KEY", Mode: modeOpenAICompatible,
		BaseURL: "https://integrate.api.nvidia.com/v1", KeyURL: "https://build.nvidia.com",
	},
	{
		ID: "gmi", Name: "GMI Cloud", EnvVar: "GMI_API_KEY", Mode: modeOpenAICompatible,
		BaseURL: "https://api.gmi-serving.com/v1", KeyURL: "https://console.gmicloud.ai",
	},
	{
		ID: "novita", Name: "Novita", EnvVar: "NOVITA_API_KEY", Mode: modeOpenAICompatible,
		BaseURL: "https://api.novita.ai/openai/v1", KeyURL: "https://novita.ai/settings/key-management",
	},
	{
		ID: "huggingface", Name: "Hugging Face Router", EnvVar: "HF_TOKEN", Mode: modeOpenAICompatible,
		BaseURL: "https://router.huggingface.co/v1", KeyURL: "https://huggingface.co/settings/tokens",
	},
}

// ProviderByID returns the registry entry for id and whether it was found.
func ProviderByID(id string) (ProviderInfo, bool) {
	for _, p := range knownProviders {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// FetchOpenAICompatibleModels lists model ids from a provider's
// OpenAI-compatible GET <baseURL>/models endpoint. Used at setup time to
// resolve a real model id when an aggregator has no statically-pinnable
// default (its catalog rotates). Best-effort: a non-200, a network error,
// or an unparseable body returns an error the caller falls back from
// (prompt for / require --model). The just-entered apiKey authenticates
// the call from the host.
func FetchOpenAICompatibleModels(baseURL, apiKey string) ([]string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}

	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("models endpoint returned no models")
	}
	return ids, nil
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
	// ExtraBody is merged by LiteLLM into every upstream request for this
	// model. It is intentionally opt-in because many OpenAI-compatible servers
	// reject unknown provider-specific fields.
	ExtraBody map[string]any `yaml:"extra_body,omitempty"`
	// CacheControlInjectionPoints is a LiteLLM directive that tells the proxy
	// to attach Anthropic-style `cache_control: {type: ephemeral}` markers to
	// specific messages on every request to this model. We pin the system
	// message for Anthropic entries so prompt caching is on by default.
	CacheControlInjectionPoints []CacheControlInjection `yaml:"cache_control_injection_points,omitempty"`
}

// CustomEndpointOptions controls optional per-request behavior for custom
// OpenAI-compatible endpoints.
type CustomEndpointOptions struct {
	DisableThinking bool
}

func (o CustomEndpointOptions) extraBody() map[string]any {
	if !o.DisableThinking {
		return nil
	}

	return map[string]any{
		"chat_template_kwargs": map[string]any{
			"enable_thinking": false,
		},
	}
}

// CacheControlInjection is one entry in LiteLLM's
// cache_control_injection_points list. Either Role or Index narrows which
// message in the request gets the cache_control marker.
type CacheControlInjection struct {
	Location string `yaml:"location"`
	Role     string `yaml:"role,omitempty"`
	Index    *int   `yaml:"index,omitempty"`
}

// anthropicCacheControlPoints is the default cache_control_injection_points
// applied to every Anthropic model entry. Pinning the system message makes
// LiteLLM auto-attach cache_control to the largest stable prefix of the
// prompt — the canonical "prompt caching by default" pattern.
func anthropicCacheControlPoints() []CacheControlInjection {
	return []CacheControlInjection{{Location: "message", Role: "system"}}
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
	return PatchLiteLLMEntries(cfg, u, entries)
}

// PatchLiteLLMEntries merges precomputed ModelEntry values into the
// LiteLLM ConfigMap without touching Secrets and without restarting.
// Caller is responsible for restarting LiteLLM once after batching all
// patches when an upstream Secret/ConfigMap value actually changed.
func PatchLiteLLMEntries(cfg *config.Config, u *ui.UI, entries []ModelEntry) error {
	if len(entries) == 0 {
		return nil
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

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

// litellmAPICall calls a LiteLLM HTTP endpoint on every running litellm pod
// using a short-lived per-pod `kubectl port-forward`. With replicas>1, this
// fans out to each pod so every router is updated immediately.
//
// We use port-forward instead of `kubectl exec <pod> -- wget` because the
// LiteLLM container is distroless and ships without wget, curl, or a shell,
// so the exec-based path fails with "executable file not found" on current
// images. Port-forward has no such dependency.
func litellmAPICall(kubectlBinary, kubeconfigPath, masterKey, path string, body []byte) error {
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "pods", "-n", namespace, "-l", "app=litellm",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return fmt.Errorf("list litellm pods: %w", err)
	}

	podNames := strings.Fields(strings.TrimSpace(raw))
	if len(podNames) == 0 {
		return fmt.Errorf("no running litellm pods in %s namespace", namespace)
	}

	var firstErr error
	for _, pod := range podNames {
		if err := litellmPodAPICall(kubectlBinary, kubeconfigPath, pod, masterKey, path, body); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("pod %s: %w", pod, err)
		}
	}

	return firstErr
}

// litellmPodAPICall opens a per-pod port-forward on an OS-chosen local port
// and POSTs the payload to the LiteLLM admin API on that pod.
func litellmPodAPICall(kubectlBinary, kubeconfigPath, pod, masterKey, path string, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, kubectlBinary, "port-forward",
		"-n", namespace, "pod/"+pod, ":4000")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start port-forward: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Parse "Forwarding from 127.0.0.1:<port> -> 4000" from stdout.
	localPort, err := parseForwardedPort(stdout, 15*time.Second)
	if err != nil {
		return fmt.Errorf("port-forward: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	reqURL := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+masterKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("litellm %s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}

// litellmGETViaPortForward GETs a LiteLLM admin endpoint on one Running
// litellm pod via a short-lived kubectl port-forward. Used for endpoints
// that are pod-agnostic (e.g. /model/info) where one pod is enough.
func litellmGETViaPortForward(kubectlBinary, kubeconfigPath, masterKey, path string) ([]byte, error) {
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "pods", "-n", namespace, "-l", "app=litellm",
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return nil, fmt.Errorf("list litellm pods: %w", err)
	}
	pod := strings.TrimSpace(raw)
	if pod == "" {
		return nil, fmt.Errorf("no running litellm pods in %s namespace", namespace)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, kubectlBinary, "port-forward",
		"-n", namespace, "pod/"+pod, ":4000")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start port-forward: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	localPort, err := parseForwardedPort(stdout, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("port-forward: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("litellm GET %s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

// parseForwardedPort reads lines from r until it finds kubectl's
// "Forwarding from 127.0.0.1:<port> -> 4000" and returns <port>.
func parseForwardedPort(r io.Reader, timeout time.Duration) (int, error) {
	type result struct {
		port int
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.Contains(line, "Forwarding from") {
				continue
			}
			// "Forwarding from 127.0.0.1:54321 -> 4000"
			colonIdx := strings.LastIndex(line, ":")
			arrowIdx := strings.Index(line, " -> ")
			if colonIdx < 0 || arrowIdx < 0 || colonIdx >= arrowIdx {
				continue
			}
			portStr := line[colonIdx+1 : arrowIdx]
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err != nil {
				continue
			}
			ch <- result{port: port}
			return
		}
		if err := scanner.Err(); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{err: errors.New("port-forward exited before reporting local port")}
	}()

	select {
	case res := <-ch:
		return res.port, res.err
	case <-time.After(timeout):
		return 0, errors.New("timed out waiting for kubectl port-forward to bind")
	}
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

	for _, entry := range entries {
		params := map[string]any{
			"model":    entry.LiteLLMParams.Model,
			"api_base": entry.LiteLLMParams.APIBase,
			"api_key":  entry.LiteLLMParams.APIKey,
		}
		if len(entry.LiteLLMParams.ExtraBody) > 0 {
			params["extra_body"] = entry.LiteLLMParams.ExtraBody
		}

		body := map[string]any{
			"model_name":     entry.ModelName,
			"litellm_params": params,
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			continue
		}

		if err := litellmAPICall(kubectlBinary, kubeconfigPath, masterKey, "/model/new", bodyJSON); err != nil {
			u.Warnf("Hot-add %s failed: %v", entry.ModelName, err)
			return fmt.Errorf("hot-add %s: %w", entry.ModelName, err)
		}
	}

	return nil
}

// hotDeleteModel removes a model from the running LiteLLM router(s) via the
// /model/delete API. It first queries /model/info to resolve model IDs.
func hotDeleteModel(cfg *config.Config, u *ui.UI, modelName string) error {
	masterKey, err := GetMasterKey(cfg)
	if err != nil {
		return fmt.Errorf("get master key: %w", err)
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Query /model/info on one pod to get model IDs (via port-forward; the
	// LiteLLM container is distroless and has no wget/curl).
	raw, err := litellmGETViaPortForward(kubectlBinary, kubeconfigPath, masterKey, "/model/info")
	if err != nil {
		return fmt.Errorf("query /model/info: %w", err)
	}

	var infoResp struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				ID string `json:"id"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &infoResp); err != nil {
		return fmt.Errorf("parse /model/info: %w", err)
	}

	deleted := 0
	for _, m := range infoResp.Data {
		if m.ModelName != modelName || m.ModelInfo.ID == "" {
			continue
		}

		deleteBody, _ := json.Marshal(map[string]any{"id": m.ModelInfo.ID})
		if err := litellmAPICall(kubectlBinary, kubeconfigPath, masterKey, "/model/delete", deleteBody); err != nil {
			u.Warnf("Hot-delete model %s (id=%s) failed: %v", modelName, m.ModelInfo.ID, err)
		} else {
			deleted++
		}
	}

	if deleted == 0 {
		return fmt.Errorf("model %q not found in LiteLLM router", modelName)
	}

	return nil
}

// reorderModelList is the pure-function core of PreferModels. It moves the
// named entries to the head of the list (in the order given) and returns
// the new slice, plus a boolean indicating whether the input was already in
// the requested order (the caller should treat that as a no-op so it can
// skip the kubectl patch + LiteLLM rollout). Unknown or duplicate names
// produce an error so typos surface loudly.
func reorderModelList(entries []ModelEntry, names []string) ([]ModelEntry, bool, error) {
	indexByName := make(map[string]int, len(entries))
	for i, entry := range entries {
		indexByName[entry.ModelName] = i
	}

	var missing []string
	picked := make(map[string]bool, len(names))
	for _, name := range names {
		if _, ok := indexByName[name]; !ok {
			missing = append(missing, name)
			continue
		}
		if picked[name] {
			return nil, false, fmt.Errorf("duplicate model in prefer args: %q", name)
		}
		picked[name] = true
	}
	if len(missing) > 0 {
		return nil, false, fmt.Errorf("model(s) not found in LiteLLM config: %s\n  Run 'obol model list' to see available entries", strings.Join(missing, ", "))
	}

	alreadyAtHead := true
	for i, name := range names {
		if i >= len(entries) || entries[i].ModelName != name {
			alreadyAtHead = false
			break
		}
	}

	reordered := make([]ModelEntry, 0, len(entries))
	for _, name := range names {
		reordered = append(reordered, entries[indexByName[name]])
	}
	for _, entry := range entries {
		if picked[entry.ModelName] {
			continue
		}
		reordered = append(reordered, entry)
	}
	return reordered, alreadyAtHead, nil
}

// PreferModels reorders LiteLLM's model_list so the named entries appear at
// the head, in the order given. Remaining entries keep their original
// relative order. This is the operator-facing primitive that lets
// model.Rank's "first chat-capable wins" rule pick a specific primary
// without a remove/re-add cycle.
//
// Returns an error if any of the requested names is not present in the
// current model_list — typos should be loud, not silent no-ops.
//
// LiteLLM has no model_list reorder API, so after the ConfigMap patch this
// rolls the LiteLLM Deployment so the new order takes effect (the
// /v1/models listing follows model_list order, and hermes/openclaw read
// the ConfigMap directly via GetConfiguredModels for the agent primary).
func PreferModels(cfg *config.Config, u *ui.UI, names []string) error {
	if len(names) == 0 {
		return errors.New("at least one model name is required")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return fmt.Errorf("failed to read LiteLLM config: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return fmt.Errorf("failed to parse config.yaml: %w", err)
	}

	reordered, alreadyAtHead, err := reorderModelList(litellmConfig.ModelList, names)
	if err != nil {
		return err
	}
	if alreadyAtHead {
		u.Infof("Model(s) already at the head of the model_list, no change")
		return nil
	}
	litellmConfig.ModelList = reordered

	updated, err := yaml.Marshal(&litellmConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	escapedYAML, err := json.Marshal(string(updated))
	if err != nil {
		return fmt.Errorf("failed to escape YAML: %w", err)
	}
	patchJSON := fmt.Sprintf(`{"data":{"config.yaml":%s}}`, escapedYAML)

	u.Infof("Promoting %s to head of LiteLLM model_list", strings.Join(names, ", "))
	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", patchJSON, "--type=merge", "--field-manager=helm"); err != nil {
		return fmt.Errorf("failed to patch ConfigMap: %w", err)
	}

	// LiteLLM has no reorder API; restart the deployment so the new order
	// takes effect (mostly cosmetic for /v1/models listings — agent primary
	// is read from the ConfigMap directly via GetConfiguredModels, which is
	// already correct after the patch above).
	if err := RestartLiteLLM(cfg, u, "prefer"); err != nil {
		u.Warnf("LiteLLM rollout failed: %v", err)
		u.Dim("  The ConfigMap is updated; agent will pick up the new primary on next sync.")
	}

	return nil
}

// RemoveModel removes a model entry from the LiteLLM ConfigMap (persistence)
// and hot-deletes it from the running router via the API (immediate effect).
// No pod restart is required.
func RemoveModel(cfg *config.Config, u *ui.UI, modelName string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	// 1. Patch ConfigMap for persistence (survives pod restarts).
	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return fmt.Errorf("failed to read LiteLLM config: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return fmt.Errorf("failed to parse config.yaml: %w", err)
	}

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

	updated, err := yaml.Marshal(&litellmConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	escapedYAML, err := json.Marshal(string(updated))
	if err != nil {
		return fmt.Errorf("failed to escape YAML: %w", err)
	}

	patchJSON := fmt.Sprintf(`{"data":{"config.yaml":%s}}`, escapedYAML)

	u.Infof("Removing model %q from LiteLLM config", modelName)

	if err := kubectl.Run(kubectlBinary, kubeconfigPath,
		"patch", "configmap", configMapName, "-n", namespace,
		"-p", patchJSON, "--type=merge", "--field-manager=helm"); err != nil {
		return fmt.Errorf("failed to patch ConfigMap: %w", err)
	}

	// 2. Hot-delete from running router via API (immediate, no restart).
	if err := hotDeleteModel(cfg, u, modelName); err != nil {
		u.Warnf("Hot-remove from LiteLLM router failed: %v", err)
		u.Dim("  The model is removed from config; it will disappear after next restart.")
	} else {
		u.Successf("Model %q removed (live + config)", modelName)
	}

	return nil
}

// AddCustomEndpoint adds a custom OpenAI-compatible endpoint to LiteLLM
// after validating it works.
//
// LiteLLM `model_name` contract — the canonical identifier is the bare
// `modelName`. Same convention every other code path in this stack uses:
// Ollama writes `qwen3.5:9b`, Anthropic writes `claude-opus-4-7`, OpenAI
// writes `gpt-5.4`. The agent (Hermes / OpenClaw) reads `model_name` straight
// back as the `model` field on chat-completion calls — any provider-prefix
// namespacing (`custom/<name>/<model>`) on this side breaks that round-trip
// because the agent then strips it and calls LiteLLM with a key that doesn't
// match.
//
// Two custom endpoints that publish the same `modelName` will overwrite
// each other in the LiteLLM ConfigMap; that is the natural "repoint my
// model" behavior an operator running `obol model setup custom` wants when
// they re-run the command.
func AddCustomEndpoint(cfg *config.Config, u *ui.UI, endpoint, modelName, apiKey string) error {
	return AddCustomEndpointWithOptions(cfg, u, endpoint, modelName, apiKey, CustomEndpointOptions{})
}

func AddCustomEndpointWithOptions(cfg *config.Config, u *ui.UI, endpoint, modelName, apiKey string, options CustomEndpointOptions) error {
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
	if err := ValidateCustomEndpointWithOptions(validationEndpoint, modelName, apiKey, options); err != nil {
		return fmt.Errorf("endpoint validation failed: %w", err)
	}

	u.Success("Endpoint validated successfully")

	// For the cluster ConfigMap, translate localhost to k3d-internal
	clusterEndpoint := localhostToClusterEndpoint(endpoint)
	if clusterEndpoint != endpoint {
		u.Infof("Cluster endpoint: %s (translated from %s)", clusterEndpoint, endpoint)
	}

	entry := buildCustomEndpointEntryWithOptions(modelName, clusterEndpoint, apiKey, options)

	u.Infof("Adding custom endpoint (model: %s) to LiteLLM config", modelName)

	if err := patchLiteLLMConfig(kubectlBinary, kubeconfigPath, []ModelEntry{entry}); err != nil {
		return fmt.Errorf("failed to update LiteLLM config: %w", err)
	}

	// Hot-add via API (no restart needed).
	if err := hotAddModels(cfg, u, []ModelEntry{entry}); err != nil {
		u.Warnf("Hot-add failed, falling back to restart: %v", err)
		return RestartLiteLLM(cfg, u, modelName)
	}

	u.Successf("Custom endpoint added (model: %s)", modelName)

	return nil
}

// probeBackoffSleep is the sleep used between ValidateCustomEndpoint inference-probe
// retries. Overridable in tests to keep them fast.
var probeBackoffSleep = time.Sleep

// ValidateCustomEndpoint validates that a custom OpenAI-compatible endpoint works.
// It runs a 2-step validation: reachability check, then inference probe.
// The inference probe is the definitive test — some servers (e.g., mlx-lm) don't
// list the loaded model in /models but accept it for inference.
func ValidateCustomEndpoint(endpoint, modelName, apiKey string) error {
	return ValidateCustomEndpointWithOptions(endpoint, modelName, apiKey, CustomEndpointOptions{})
}

// ValidateCustomEndpointWithOptions validates that a custom OpenAI-compatible
// endpoint works. It runs a 2-step validation: reachability check, then
// inference probe. The inference probe is the definitive test — some servers
// (e.g., mlx-lm) don't list the loaded model in /models but accept it for
// inference.
func ValidateCustomEndpointWithOptions(endpoint, modelName, apiKey string, options CustomEndpointOptions) error {
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
	probe := map[string]any{
		"model":      modelName,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}
	for k, v := range options.extraBody() {
		probe[k] = v
	}
	probePayload, _ := json.Marshal(probe) //nolint:errchkjson // map[string]any is safe, keys/values are controlled
	completionsURL := strings.TrimRight(endpoint, "/") + "/chat/completions"

	probeReq, err := http.NewRequest(http.MethodPost, completionsURL, bytes.NewReader(probePayload))
	if err != nil {
		return fmt.Errorf("failed to build inference probe: %w", err)
	}

	probeReq.Header.Set("Content-Type", "application/json")

	if authHeader != "" {
		probeReq.Header.Set("Authorization", authHeader)
	}

	// Retry on transient network errors (DNS flake, TCP reset, route loss).
	// Only client.Do errors are retried — non-200 HTTP responses are real
	// upstream signals (4xx = config bug, 5xx = upstream broken) and fail fast.
	const probeMaxAttempts = 3
	probeBackoffs := []time.Duration{
		250 * time.Millisecond,
		1 * time.Second,
		4 * time.Second,
	}

	var probeResp *http.Response
	var probeErr error
	for attempt := 0; attempt < probeMaxAttempts; attempt++ {
		// Bodies are single-use; re-attach the payload for each attempt.
		attemptReq := probeReq.Clone(probeReq.Context())
		attemptReq.Body = io.NopCloser(bytes.NewReader(probePayload))

		probeResp, probeErr = client.Do(attemptReq)
		if probeErr == nil {
			break
		}
		if attempt < probeMaxAttempts-1 {
			probeBackoffSleep(probeBackoffs[attempt])
		}
	}
	if probeErr != nil {
		return fmt.Errorf("inference probe failed after %d attempts — cannot reach %s: %w",
			probeMaxAttempts, completionsURL, probeErr)
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

// buildModelEntries creates LiteLLM model_list entries for a provider,
// shaped by its registry Mode:
//   - anthropic/openai: explicit entries (so the chosen model wins Rank's
//     "first chat-capable" rule) followed by a <provider>/* wildcard.
//   - ollama: explicit ollama_chat/ entries only (wildcards are broken).
//   - openai-compatible: explicit openai/<id> entries with an explicit
//     api_base = BaseURL and key from EnvVar — no wildcard.
//
// A provider not in the registry falls back to the generic openai/<id>
// shape keyed on <PROVIDER>_API_KEY (legacy `setup custom` behavior).
func buildModelEntries(provider string, models []string) []ModelEntry {
	p, ok := ProviderByID(provider)
	if !ok {
		// Unknown provider: legacy generic shape (no api_base).
		var entries []ModelEntry
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:  provider + "/" + m,
					APIKey: fmt.Sprintf("os.environ/%s_API_KEY", strings.ToUpper(provider)),
				},
			})
		}
		return entries
	}

	keyRef := ""
	if p.EnvVar != "" {
		keyRef = "os.environ/" + p.EnvVar
	}

	var entries []ModelEntry
	switch p.Mode {
	case modeOllama:
		// Explicit entries — ollama_chat/* wildcards are broken in LiteLLM.
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:   "ollama_chat/" + m,
					APIBase: "http://ollama.llm.svc.cluster.local:11434",
				},
			})
		}
	case modeAnthropic:
		cachePoints := anthropicCacheControlPoints()
		// Explicit entries first so the user-selected model is the primary
		// under model.Rank's "first chat-capable wins" rule. Hermes cannot
		// send `model: anthropic/*` literally (LiteLLM doesn't resolve a
		// wildcard to a default), so the wildcard must never sit at index 0.
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:                       m,
					APIKey:                      keyRef,
					CacheControlInjectionPoints: cachePoints,
				},
			})
		}
		entries = append(entries, ModelEntry{
			ModelName: "anthropic/*",
			LiteLLMParams: LiteLLMParams{
				Model:                       "anthropic/*",
				APIKey:                      keyRef,
				CacheControlInjectionPoints: cachePoints,
			},
		})
	case modeOpenAI:
		// Explicit-before-wildcard, same rationale as Anthropic above.
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName:     m,
				LiteLLMParams: LiteLLMParams{Model: "openai/" + m, APIKey: keyRef},
			})
		}
		entries = append(entries, ModelEntry{
			ModelName:     "openai/*",
			LiteLLMParams: LiteLLMParams{Model: "openai/*", APIKey: keyRef},
		})
	case modeOpenAICompatible:
		// Explicit openai-shaped entries with an explicit api_base. No
		// wildcard — the aggregator's catalog is huge and overlaps others.
		for _, m := range models {
			entries = append(entries, ModelEntry{
				ModelName: m,
				LiteLLMParams: LiteLLMParams{
					Model:   "openai/" + m,
					APIBase: p.BaseURL,
					APIKey:  keyRef,
				},
			})
		}
	}

	return entries
}

// buildCustomEndpointEntry constructs the LiteLLM ModelEntry for a custom
// OpenAI-compatible endpoint added via `obol model setup custom`. The
// `model_name` is the bare `modelName` — see the AddCustomEndpoint doc
// comment for the round-trip contract this enforces. Extracted as a
// standalone helper so the entry shape is unit-testable without going
// through the full kubectl-driven AddCustomEndpoint path.
func buildCustomEndpointEntry(modelName, clusterEndpoint, apiKey string) ModelEntry {
	return buildCustomEndpointEntryWithOptions(modelName, clusterEndpoint, apiKey, CustomEndpointOptions{})
}

func buildCustomEndpointEntryWithOptions(modelName, clusterEndpoint, apiKey string, options CustomEndpointOptions) ModelEntry {
	entry := ModelEntry{
		ModelName: modelName,
		LiteLLMParams: LiteLLMParams{
			Model:     "openai/" + modelName,
			APIBase:   clusterEndpoint,
			APIKey:    apiKey,
			ExtraBody: options.extraBody(),
		},
	}
	if apiKey == "" {
		entry.LiteLLMParams.APIKey = "none"
	}
	return entry
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
		"-p", patchJSON, "--type=merge", "--field-manager=helm")
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
	// BYOK aggregator entries are openai-shaped (openai/<id>) but carry an
	// explicit api_base — match it back to the registry so status groups
	// them under their real provider (venice, openrouter, …) rather than
	// "openai". Checked before the bare openai/ prefix below.
	if base := entry.LiteLLMParams.APIBase; base != "" && strings.HasPrefix(model, ProviderOpenAI+"/") {
		for _, p := range knownProviders {
			if p.Mode == modeOpenAICompatible && p.BaseURL == base {
				return p.ID
			}
		}
	}

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

// PreferredDefaultOllamaModel is the model we *recommend* operators pull when
// they're starting from an empty Ollama inventory or have only cloud-aliased
// entries. Picked as a reasonable balance between capability and CPU footprint
// on developer machines without a discrete GPU.
//
// Note: we do NOT bump this to the front of an existing `/api/tags` ordering.
// On hosts that already have local chat models, the ordering Ollama returns
// (modified-time) is treated as the operator's preference signal — overriding
// it would silently demote a model the user just pulled and intends to use.
// The stack-up auto-config only suggests this name when Ollama has nothing
// usable; once any local chat model is configured, `obol model prefer ...`
// is the explicit reorder path.
const PreferredDefaultOllamaModel = "qwen3.5:4b"

// AutoConfigOllamaModelNames converts the raw /api/tags inventory into the
// ordered model-name list we auto-write into LiteLLM and agent configs.
//
// Policy:
//   - strip the cosmetic `:latest` tag suffix
//   - ignore empty names
//   - keep local chat-capable models ahead of Ollama cloud aliases that would
//     require extra credentials to work (mitigates the rc8 regression where a
//     `:cloud` alias landed at index 0 and became Hermes' unusable default)
//   - keep embedding-only models last so they never become the default chat model
//   - within each tier, preserve Ollama's own ordering — that's the operator's
//     pull-history preference signal, and overriding it would silently demote
//     a model the user just pulled
//
// This only affects auto-generated defaults. Operators can still reorder the
// resulting LiteLLM model_list later with `obol model prefer ...`.
func AutoConfigOllamaModelNames(models []OllamaModel) []string {
	localChat := make([]string, 0, len(models))
	credentialRequired := make([]string, 0)
	embeddingOnly := make([]string, 0)

	for _, m := range models {
		name := normalizeOllamaModelName(m.Name)
		if name == "" {
			continue
		}
		if isEmbeddingOnlyModel(name) {
			embeddingOnly = append(embeddingOnly, name)
			continue
		}
		if isCredentialRequiringOllamaModel(name) {
			credentialRequired = append(credentialRequired, name)
			continue
		}
		localChat = append(localChat, name)
	}

	ordered := make([]string, 0, len(localChat)+len(credentialRequired)+len(embeddingOnly))
	ordered = append(ordered, localChat...)
	ordered = append(ordered, credentialRequired...)
	ordered = append(ordered, embeddingOnly...)
	return ordered
}

func normalizeOllamaModelName(name string) string {
	name = strings.TrimSpace(name)
	if before, ok := strings.CutSuffix(name, ":latest"); ok {
		name = before
	}
	return strings.TrimSpace(name)
}

func isCredentialRequiringOllamaModel(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ":cloud")
}

// IsCredentialRequiringOllamaModel reports whether an Ollama model name is one
// of the cloud-aliased entries that needs an API key to actually serve
// requests (e.g. `deepseek-v4-pro:cloud`). Exported so stack-up can warn when
// the auto-picked primary would land on one of these.
func IsCredentialRequiringOllamaModel(name string) bool {
	return isCredentialRequiringOllamaModel(name)
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

// preventSleep asks the OS to keep the Mac awake while we run a long download.
// On macOS, spawns `caffeinate -i -w <pid>` which auto-exits when our process does.
// No-op on other platforms. Returns a cleanup func that stops caffeinate early
// (best-effort) — safe to call even if no helper was started.
func preventSleep() func() {
	if runtime.GOOS != "darwin" {
		return func() {}
	}

	cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return func() {}
	}

	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
}

// PullOllamaModel pulls a model from the Ollama registry.
// It streams progress to stdout, matching the UX of `ollama pull`.
func PullOllamaModel(name string) error {
	stopCaffeinate := preventSleep()
	defer stopCaffeinate()

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
