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

// chatCompletionWithPrompt sends a chat completion with a custom user message.
func chatCompletionWithPrompt(t *testing.T, baseURL, modelName, token, prompt string, maxTokens int) string {
	t.Helper()
	reqBody := map[string]interface{}{
		"model":      modelName,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": maxTokens,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/v1/chat/completions",
		bytes.NewReader(bodyBytes),
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

// chatCompletion sends a chat completion request with the gateway Bearer token
// and returns the assistant response.
func chatCompletion(t *testing.T, baseURL, modelName, token string) string {
	t.Helper()
	return chatCompletionWithPrompt(t, baseURL, modelName, token, "Reply with exactly one word: hello", 32)
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

func TestIntegration_ZaiInference(t *testing.T) {
	cfg := requireCluster(t)
	apiKey := requireEnvKey(t, "ZHIPU_API_KEY")

	const id = "test-zai"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	// Configure llmspy gateway via obol model setup — this provider was NOT in
	// the old hardcoded map, so it only works with dynamic provider discovery.
	t.Log("configuring llmspy via: obol model setup --provider zai")
	obolRun(t, cfg, "model", "setup", "--provider", "zai", "--api-key", apiKey)

	cloud := &CloudProviderInfo{
		Name:    "zai",
		APIKey:  apiKey,
		ModelID: "glm-5",
		Display: "GLM-5",
	}

	// Scaffold cloud overlay + deploy via obol openclaw sync
	t.Logf("scaffolding OpenClaw instance %q with Z.AI via llmspy", id)
	scaffoldCloudInstance(t, cfg, id, cloud)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	token := getGatewayToken(t, cfg, id)
	t.Logf("retrieved gateway token (%d chars)", len(token))

	baseURL := portForward(t, cfg, namespace)
	agentModel := "ollama/glm-5" // routed through llmspy
	t.Logf("testing inference with model %s at %s", agentModel, baseURL)

	reply := chatCompletion(t, baseURL, agentModel, token)
	t.Logf("Z.AI response: %s", reply)
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

// ---------------------------------------------------------------------------
// Skills integration tests
// ---------------------------------------------------------------------------

// TestIntegration_SkillsStagedOnSync verifies that `obol openclaw sync`
// stages embedded skills into the deployment directory and injects them
// into the PVC volume path on the host filesystem.
func TestIntegration_SkillsStagedOnSync(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skills-stage"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q", id)
	scaffoldInstance(t, cfg, id, models)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	// 1. Verify skills were staged in the deployment directory
	deployDir := deploymentPath(cfg, id)
	skillsDir := filepath.Join(deployDir, "skills")
	expectedSkills := []string{"distributed-validators", "ethereum-networks", "local-wallet", "obol-stack"}

	for _, skill := range expectedSkills {
		skillMD := filepath.Join(skillsDir, skill, "SKILL.md")
		info, err := os.Stat(skillMD)
		if err != nil {
			t.Errorf("skill %q not staged in deployment dir: %v", skill, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("skill %q SKILL.md is empty", skill)
		}
		t.Logf("  staged: %s/SKILL.md (%d bytes)", skill, info.Size())
	}

	// Verify scripts and references were also staged
	for _, sub := range []string{
		"ethereum-networks/scripts/rpc.py",
		"obol-stack/scripts/kube.py",
		"distributed-validators/references/api-examples.md",
	} {
		if _, err := os.Stat(filepath.Join(skillsDir, sub)); err != nil {
			t.Errorf("missing staged file %s: %v", sub, err)
		}
	}

	// 2. Verify skills were injected into the PVC volume path
	volumePath := skillsVolumePath(cfg, id)
	for _, skill := range expectedSkills {
		skillMD := filepath.Join(volumePath, skill, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			t.Errorf("skill %q not injected to volume: %v", skill, err)
		} else {
			t.Logf("  injected: %s/SKILL.md in volume", skill)
		}
	}
}

// TestIntegration_SkillsVisibleInPod verifies that after deployment, skills
// are visible inside the running OpenClaw pod at /data/.openclaw/skills/.
func TestIntegration_SkillsVisibleInPod(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skills-pod"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q", id)
	scaffoldInstance(t, cfg, id, models)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	// List skills inside the pod via kubectl exec (without -it for non-interactive)
	output := obolRun(t, cfg, "kubectl",
		"exec", "-c", "openclaw",
		"-n", namespace, "deploy/openclaw", "--",
		"ls", "/data/.openclaw/skills/",
	)
	t.Logf("skills visible in pod:\n%s", output)

	expectedSkills := []string{"distributed-validators", "ethereum-networks", "local-wallet", "obol-stack"}
	for _, skill := range expectedSkills {
		if !strings.Contains(output, skill) {
			t.Errorf("skill %q not visible in pod; ls output:\n%s", skill, output)
		}
	}

	// Verify SKILL.md content is readable inside the pod for a representative skill
	mdContent := obolRun(t, cfg, "kubectl",
		"exec", "-c", "openclaw",
		"-n", namespace, "deploy/openclaw", "--",
		"head", "-5", "/data/.openclaw/skills/ethereum-networks/SKILL.md",
	)
	if !strings.Contains(mdContent, "ethereum-networks") && !strings.Contains(mdContent, "Ethereum") {
		t.Errorf("ethereum-networks SKILL.md not readable in pod; got:\n%s", mdContent)
	}
	t.Logf("ethereum-networks SKILL.md header in pod:\n%s", mdContent)
}

// TestIntegration_SkillsSync verifies that `obol openclaw skills sync --from`
// copies a local skills directory to the PVC volume path.
func TestIntegration_SkillsSync(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skills-sync"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q", id)
	scaffoldInstance(t, cfg, id, models)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	// Create a custom skill in a temporary directory
	customSkillsDir := t.TempDir()
	customSkillDir := filepath.Join(customSkillsDir, "test-custom")
	if err := os.MkdirAll(customSkillDir, 0755); err != nil {
		t.Fatalf("failed to create custom skill dir: %v", err)
	}
	customMD := "---\nmetadata: {}\n---\n# Test Custom Skill\nThis is a test skill for integration testing.\n"
	if err := os.WriteFile(filepath.Join(customSkillDir, "SKILL.md"), []byte(customMD), 0644); err != nil {
		t.Fatalf("failed to write custom SKILL.md: %v", err)
	}

	// Sync custom skills via obol openclaw skills sync (explicit instance ID
	// required when multiple instances exist, e.g. "default" + test instance).
	// Flags must precede the positional arg for urfave/cli.
	t.Log("syncing custom skills via: obol openclaw skills sync --from " + customSkillsDir + " " + id)
	obolRun(t, cfg, "openclaw", "skills", "sync", "--from", customSkillsDir, id)

	// Verify custom skill landed in the volume path
	volumePath := skillsVolumePath(cfg, id)
	customMDPath := filepath.Join(volumePath, "test-custom", "SKILL.md")
	data, err := os.ReadFile(customMDPath)
	if err != nil {
		t.Fatalf("custom skill not found in volume path: %v", err)
	}
	if !strings.Contains(string(data), "Test Custom Skill") {
		t.Errorf("custom SKILL.md content mismatch; got:\n%s", string(data))
	}
	t.Logf("custom skill synced to volume: %s", customMDPath)

	// Verify custom skill is visible inside the pod
	output := obolRun(t, cfg, "kubectl",
		"exec", "-c", "openclaw",
		"-n", namespace, "deploy/openclaw", "--",
		"ls", "/data/.openclaw/skills/",
	)
	if !strings.Contains(output, "test-custom") {
		t.Errorf("custom skill not visible in pod after sync; ls output:\n%s", output)
	}
	t.Logf("skills in pod after sync:\n%s", output)
}

// TestIntegration_SkillsIdempotentSync verifies that re-running sync does not
// overwrite user-customised skills in the deployment directory.
func TestIntegration_SkillsIdempotentSync(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skills-idem"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q", id)
	scaffoldInstance(t, cfg, id, models)

	// First sync — stages and injects default skills
	t.Log("first sync...")
	obolRun(t, cfg, "openclaw", "sync", id)

	// Add a custom file to the staged skills directory (simulating user customisation)
	deployDir := deploymentPath(cfg, id)
	marker := filepath.Join(deployDir, "skills", "custom-user-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(marker, []byte("# Custom User Skill"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// Second sync — stageDefaultSkills should skip (skills/ dir already exists)
	t.Log("second sync (idempotent)...")
	obolRun(t, cfg, "openclaw", "sync", id)

	// Custom marker should still be present
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("user-customised skill removed after re-sync: %v", err)
	}

	// Embedded skills should also still be present (from first sync)
	for _, skill := range []string{"ethereum-networks", "distributed-validators"} {
		skillMD := filepath.Join(deployDir, "skills", skill, "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			t.Errorf("embedded skill %q removed after re-sync: %v", skill, err)
		}
	}

	// Custom skill should also be injected to volume (injectSkillsToVolume always runs)
	volumePath := skillsVolumePath(cfg, id)
	if _, err := os.Stat(filepath.Join(volumePath, "custom-user-skill", "SKILL.md")); err != nil {
		t.Errorf("custom skill not injected to volume on re-sync: %v", err)
	}
}

// TestIntegration_SkillInference verifies that OpenClaw loads skills into the
// agent's context and uses them during inference. Deploys an instance, sends a
// prompt asking about available skills, and checks the response references our
// embedded skill names — proving skills flow from embed → staging → volume →
// pod file watcher → agent system prompt → inference response.
func TestIntegration_SkillInference(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skill-infer"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q with Ollama models: %v", id, models)
	scaffoldInstance(t, cfg, id, models)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	token := getGatewayToken(t, cfg, id)
	baseURL := portForward(t, cfg, namespace)
	agentModel := fmt.Sprintf("ollama/%s", models[0])

	// Ask the agent to list its skills. OpenClaw injects SKILL.md descriptions
	// into the system prompt, so the agent should know about them.
	prompt := "List every skill you have access to. For each skill, state its exact name. Be concise — just the names, one per line."
	t.Logf("sending skill-awareness prompt to %s", agentModel)
	reply := chatCompletionWithPrompt(t, baseURL, agentModel, token, prompt, 256)
	t.Logf("agent reply:\n%s", reply)

	replyLower := strings.ToLower(reply)

	// The agent must mention at least 2 of our 4 embedded skills.
	// We check for partial matches to be resilient to model output variations
	// (e.g., "ethereum-networks" vs "ethereum networks" vs "Ethereum Networks").
	skillHits := 0
	skillChecks := []struct {
		name     string
		patterns []string
	}{
		{"ethereum-networks", []string{"ethereum-networks", "ethereum networks", "blockchain"}},
		{"distributed-validators", []string{"distributed-validators", "distributed validator", "dvt"}},
		{"obol-stack", []string{"obol-stack", "obol stack", "kubernetes", "k8s"}},
		{"local-wallet", []string{"local-wallet", "ethereum wallet", "wallet"}},
	}

	for _, sc := range skillChecks {
		for _, pattern := range sc.patterns {
			if strings.Contains(replyLower, pattern) {
				t.Logf("  ✓ agent referenced skill: %s (matched %q)", sc.name, pattern)
				skillHits++
				break
			}
		}
	}

	if skillHits < 2 {
		t.Errorf("agent only referenced %d/4 skills — skills may not be loaded into context.\nFull reply:\n%s", skillHits, reply)
	}
}

// TestIntegration_SkillsSmokeTest runs the Python smoke tests inside the pod
// to verify that skill scripts (rpc.py, kube.py) actually work against live
// services (eRPC, Kubernetes API, Obol API).
func TestIntegration_SkillsSmokeTest(t *testing.T) {
	cfg := requireCluster(t)
	models := requireOllama(t)

	const id = "test-skills-smoke"
	t.Cleanup(func() { cleanupInstance(t, cfg, id) })

	t.Logf("scaffolding OpenClaw instance %q", id)
	scaffoldInstance(t, cfg, id, models)

	t.Log("deploying via: obol openclaw sync " + id)
	obolRun(t, cfg, "openclaw", "sync", id)

	namespace := fmt.Sprintf("%s-%s", appName, id)
	waitForPodReady(t, cfg, namespace)

	// Find the smoke test script relative to the module root
	moduleRoot := findModuleRoot()
	if moduleRoot == "" {
		t.Fatal("could not find module root")
	}
	smokeScript := filepath.Join(moduleRoot, "tests", "skills_smoke_test.py")
	scriptData, err := os.ReadFile(smokeScript)
	if err != nil {
		t.Fatalf("failed to read smoke test script: %v", err)
	}

	// Pipe the smoke test into the pod via kubectl exec
	t.Log("running skills smoke tests inside pod...")
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, "kubectl",
		"exec", "-i", "-c", "openclaw",
		"-n", namespace, "deploy/openclaw", "--",
		"python3", "-",
	)
	cmd.Stdin = strings.NewReader(string(scriptData))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("smoke tests failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}

	output := stdout.String()
	t.Logf("smoke test output:\n%s", output)

	// Verify all tests passed
	if !strings.Contains(output, "0 failed") {
		t.Errorf("some smoke tests failed:\n%s", output)
	}
}
