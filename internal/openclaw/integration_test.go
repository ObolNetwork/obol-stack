//go:build integration

package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ---------------------------------------------------------------------------
// Test setup
// ---------------------------------------------------------------------------

// TestMain loads .env from the repo root before any test runs, so API keys
// can be stored in a gitignored file rather than exported in the shell.
func TestMain(m *testing.M) {
	loadDotEnv()
	os.Exit(m.Run())
}

// loadDotEnv reads KEY=value pairs from .env at the module root and sets
// them as environment variables. Existing env vars are not overwritten, so
// explicit exports always take precedence. Missing or unreadable .env is
// silently ignored.
func loadDotEnv() {
	root := findModuleRoot()
	if root == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return // .env doesn't exist — that's fine
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if os.Getenv(key) == "" { // don't override explicit exports
			os.Setenv(key, val) //nolint:errcheck
		}
	}
}

// findModuleRoot walks up from the test's working directory to find the
// directory containing go.mod.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Helpers — obol CLI runner
// ---------------------------------------------------------------------------

// obolRun executes the obol binary with the given arguments and returns
// combined stdout/stderr. It fatals the test on failure.
func obolRun(t *testing.T, cfg *config.Config, args ...string) string {
	t.Helper()
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("obol %v failed: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

// obolRunErr executes the obol binary and returns output + error (no fatal).
func obolRunErr(cfg *config.Config, args ...string) (string, error) {
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("obol %v: %w\n%s", args, err, buf.String())
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// Helpers — prerequisites
// ---------------------------------------------------------------------------

// requireCluster skips the test if no k3d cluster is running.
func requireCluster(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Load()
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		t.Skip("no kubeconfig found — cluster not running")
	}
	if _, err := obolRunErr(cfg, "kubectl", "cluster-info"); err != nil {
		t.Skipf("cluster not reachable: %v", err)
	}
	return cfg
}

// requireOllama skips if host Ollama is unreachable, returns available models.
func requireOllama(t *testing.T) []string {
	t.Helper()
	if !detectOllama() {
		t.Skip("host Ollama not reachable")
	}
	models := listOllamaModels()
	if len(models) == 0 {
		t.Skip("Ollama has no models pulled")
	}
	return models
}

// requireEnvKey returns the value of an env var or skips with instructions.
func requireEnvKey(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set — add it to a .env file in the repo root:\n\n"+
			"  echo '%s=<your-key>' >> .env\n\n"+
			"See .env.example for all required keys.", key, key)
	}
	return v
}

// ---------------------------------------------------------------------------
// Helpers — deployment scaffolding
// ---------------------------------------------------------------------------

// scaffoldInstance creates the deployment directory with config files for a
// given instance. This is the non-interactive equivalent of the config
// generation half of `obol openclaw onboard --id <id>`. Deployment to the
// cluster is done separately via `obol openclaw sync`.
func scaffoldInstance(t *testing.T, cfg *config.Config, id string, ollamaModels []string) {
	t.Helper()
	deploymentDir := deploymentPath(cfg, id)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		t.Fatalf("failed to create deployment dir: %v", err)
	}

	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)

	overlay := generateOverlayValues(hostname, nil, false, ollamaModels)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		t.Fatalf("failed to write overlay: %v", err)
	}

	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		t.Fatalf("failed to write helmfile: %v", err)
	}
}

// scaffoldCloudInstance creates the deployment directory with a cloud-provider
// overlay routed through llmspy.
func scaffoldCloudInstance(t *testing.T, cfg *config.Config, id string, cloud *CloudProviderInfo) {
	t.Helper()
	imported := buildLLMSpyRoutedOverlay(cloud)
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)

	deploymentDir := deploymentPath(cfg, id)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		t.Fatalf("failed to create deployment dir: %v", err)
	}

	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		t.Fatalf("failed to write secrets: %v", err)
	}

	overlay := generateOverlayValues(hostname, imported, len(secretData) > 0, nil)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		t.Fatalf("failed to write overlay: %v", err)
	}

	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		t.Fatalf("failed to write helmfile: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers — cluster interaction (all through obol verbs)
// ---------------------------------------------------------------------------

// getGatewayToken retrieves the gateway token via `obol openclaw token <id>`.
func getGatewayToken(t *testing.T, cfg *config.Config, id string) string {
	t.Helper()
	output := obolRun(t, cfg, "openclaw", "token", id)
	token := strings.TrimSpace(output)
	if token == "" {
		t.Fatalf("empty gateway token for instance %s", id)
	}
	return token
}

// waitForPodReady waits for at least one OpenClaw pod to be ready via
// `obol kubectl wait`.
func waitForPodReady(t *testing.T, cfg *config.Config, namespace string) {
	t.Helper()
	obolRun(t, cfg, "kubectl",
		"wait", "--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/instance=openclaw",
		"-n", namespace,
		"--timeout=180s",
	)
}

// freePort returns an available ephemeral port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// portForward starts `obol kubectl port-forward` in the background, waits for
// it to become ready, and registers cleanup. Returns "http://localhost:<port>".
func portForward(t *testing.T, cfg *config.Config, namespace string) string {
	t.Helper()
	localPort := freePort(t)
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, obolBinary,
		"kubectl", "-n", namespace, "port-forward", "svc/openclaw",
		fmt.Sprintf("%d:18789", localPort),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("port-forward start failed: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitDone)
		}()
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	})

	// Wait for the forwarded port to accept connections and respond to HTTP
	// requests (up to 30s). The dev-mode obol wrapper runs go-run which may
	// accept TCP before the actual kubectl port-forward is wired up.
	baseURL := fmt.Sprintf("http://localhost:%d", localPort)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", localPort), 500*time.Millisecond)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		conn.Close()
		// Verify the forwarded port responds to HTTP (not just TCP).
		hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
		hreq, _ := http.NewRequestWithContext(hctx, http.MethodGet, baseURL+"/", nil)
		resp, herr := http.DefaultClient.Do(hreq)
		hcancel()
		if herr == nil {
			resp.Body.Close()
			return baseURL
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("port-forward not ready at %s after 30s\nstderr: %s", baseURL, stderr.String())
	return ""
}

// chatCompletion sends a chat completion request with the gateway Bearer token
// and returns the assistant response.
func chatCompletion(t *testing.T, baseURL, modelName, token string) string {
	t.Helper()
	body := fmt.Sprintf(`{
		"model": "%s",
		"messages": [{"role":"user","content":"Reply with exactly one word: hello"}],
		"max_tokens": 32
	}`, modelName)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/chat/completions",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat completion request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat completion returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("failed to parse response: %v\nbody: %s", err, string(respBody))
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		t.Fatalf("empty response from chat completion: %s", string(respBody))
	}
	return result.Choices[0].Message.Content
}

// cleanupInstance deletes an OpenClaw instance via `obol openclaw delete --force`.
func cleanupInstance(t *testing.T, cfg *config.Config, id string) {
	t.Helper()
	output, err := obolRunErr(cfg, "openclaw", "delete", "--force", id)
	if err != nil {
		t.Logf("cleanup %s failed (non-fatal): %v\n%s", id, err, output)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_OllamaInference(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-ollama"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	// Scaffold deployment config (non-interactive config generation)
	t.Logf("scaffolding OpenClaw instance %q with Ollama models: %v", id, models)
	scaffoldInstance(t, cfg, id, models)

	// Deploy via obol openclaw sync (tests CLI → helmfile → helm chart path)
	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	// Get gateway token via obol openclaw token
	token := getGatewayToken(t, cfg, id)
	t.Logf("retrieved gateway token (%d chars)", len(token))

	baseURL := portForward(t, cfg, namespace)
	agentModel := fmt.Sprintf("ollama/%s", models[0])
	t.Logf("testing inference with model %s at %s", agentModel, baseURL)

	reply := chatCompletion(t, baseURL, agentModel, token)
	t.Logf("Ollama response: %s", reply)
}

func TestIntegration_AnthropicInference(t *testing.T) {
	cfg := requireCluster(t)
	apiKey := requireEnvKey(t, "ANTHROPIC_API_KEY")

	const id = "test-anthropic"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	// Configure llmspy gateway via obol model setup
	t.Log("configuring llmspy via: obol model setup --provider anthropic")
	obolRun(t, cfg, "model", "setup", "--provider", "anthropic", "--api-key", apiKey)

	cloud := &CloudProviderInfo{
		Name:    "anthropic",
		APIKey:  apiKey,
		ModelID: "claude-sonnet-4-5-20250929",
		Display: "Claude Sonnet 4.5",
	}

	// Scaffold cloud overlay + deploy via obol openclaw sync
	t.Logf("scaffolding OpenClaw instance %q with Anthropic via llmspy", id)
	scaffoldCloudInstance(t, cfg, id, cloud)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	token := getGatewayToken(t, cfg, id)
	t.Logf("retrieved gateway token (%d chars)", len(token))

	baseURL := portForward(t, cfg, namespace)
	agentModel := "ollama/claude-sonnet-4-5-20250929" // routed through llmspy
	t.Logf("testing inference with model %s at %s", agentModel, baseURL)

	reply := chatCompletion(t, baseURL, agentModel, token)
	t.Logf("Anthropic response: %s", reply)
}

func TestIntegration_OpenAIInference(t *testing.T) {
	cfg := requireCluster(t)
	apiKey := requireEnvKey(t, "OPENAI_API_KEY")

	const id = "test-openai"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	// Configure llmspy gateway via obol model setup
	t.Log("configuring llmspy via: obol model setup --provider openai")
	obolRun(t, cfg, "model", "setup", "--provider", "openai", "--api-key", apiKey)

	cloud := &CloudProviderInfo{
		Name:    "openai",
		APIKey:  apiKey,
		ModelID: "gpt-4o-mini",
		Display: "GPT-4o Mini",
	}

	// Scaffold cloud overlay + deploy via obol openclaw sync
	t.Logf("scaffolding OpenClaw instance %q with OpenAI via llmspy", id)
	scaffoldCloudInstance(t, cfg, id, cloud)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	token := getGatewayToken(t, cfg, id)
	t.Logf("retrieved gateway token (%d chars)", len(token))

	baseURL := portForward(t, cfg, namespace)
	agentModel := "ollama/gpt-4o-mini" // routed through llmspy
	t.Logf("testing inference with model %s at %s", agentModel, baseURL)

	reply := chatCompletion(t, baseURL, agentModel, token)
	t.Logf("OpenAI response: %s", reply)
}

func TestIntegration_MultiInstance(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	ids := []string{"test-multi-1", "test-multi-2", "test-multi-3"}
	for _, id := range ids {
		t.Cleanup(func() { cleanupInstance(t, cfg, id) })
	}

	// Scaffold and deploy all three instances via obol openclaw sync
	for _, id := range ids {
		t.Logf("scaffolding instance %q", id)
		scaffoldInstance(t, cfg, id, models)

		t.Logf("deploying via: obol openclaw sync %s", id)
		obolRun(t, cfg, "openclaw", "sync", id)
	}

	// Wait for all pods
	for _, id := range ids {
		namespace := fmt.Sprintf("%s-%s", appName, id)
		t.Logf("waiting for pod in %s", namespace)
		waitForPodReady(t, cfg, namespace)
	}

	// Verify all instances appear in obol openclaw list
	listOutput := obolRun(t, cfg, "openclaw", "list")
	for _, id := range ids {
		if !strings.Contains(listOutput, id) {
			t.Errorf("obol openclaw list missing instance %s, got:\n%s", id, listOutput)
		}
	}

	// Hit inference on each instance
	agentModel := fmt.Sprintf("ollama/%s", models[0])
	for _, id := range ids {
		namespace := fmt.Sprintf("%s-%s", appName, id)
		token := getGatewayToken(t, cfg, id)
		baseURL := portForward(t, cfg, namespace)
		t.Logf("testing inference on %s at %s", id, baseURL)
		reply := chatCompletion(t, baseURL, agentModel, token)
		t.Logf("instance %s replied: %s", id, reply)
	}
}
