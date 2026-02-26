//go:build integration

package x402

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	x402lib "github.com/mark3labs/x402-go"
)

// TestIntegration_PaymentGate_FullLifecycle tests the complete sell-side
// monetize journey: 402 without payment → 200 with mock payment → actual
// inference response from Ollama through the x402 payment gate.
//
// Prerequisites:
//   - Running cluster (obol stack up)
//   - x402-verifier deployed and healthy
//   - Ollama running on the host with at least one model
//   - ServiceOffer "qwen35" created and reconciled (or any model via --model flag)
//
// The test:
//  1. Starts a mock facilitator on the host
//  2. Patches x402-pricing ConfigMap to point at mock facilitator
//  3. Sends request WITHOUT payment → expects 402
//  4. Sends request WITH payment → expects 200 + inference response
//  5. Restores original ConfigMap on cleanup
func TestIntegration_PaymentGate_FullLifecycle(t *testing.T) {
	cfg := requireClusterConfig(t)
	kubectlBin := filepath.Join(cfg.binDir, "kubectl")
	kubeconfig := filepath.Join(cfg.configDir, "kubeconfig.yaml")

	// Verify x402-verifier is running.
	out, err := kubectlOutput(kubectlBin, kubeconfig, "get", "pods", "-n", "x402",
		"-l", "app=x402-verifier", "--no-headers")
	if err != nil {
		t.Fatalf("kubectl get pods: %v", err)
	}
	if !strings.Contains(out, "Running") {
		t.Skip("x402-verifier not running")
	}

	// Check that a pricing route exists (from monetize.py reconciliation).
	cmYAML, err := kubectlOutput(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		t.Fatalf("kubectl get cm: %v", err)
	}
	if !strings.Contains(cmYAML, "pattern:") {
		t.Skip("no pricing routes configured — run: obol monetize offer + monetize.py process first")
	}

	// Extract the route pattern to know which path to hit.
	routePath := extractRoutePath(cmYAML)
	if routePath == "" {
		t.Fatal("could not extract route path from pricing config")
	}
	t.Logf("Testing route: %s", routePath)

	// ── Step 1: Start mock facilitator on host ──────────────────────────
	mockFac := startHostMockFacilitator(t)
	t.Logf("Mock facilitator running on port %d (cluster URL: %s)", mockFac.port, mockFac.clusterURL)

	// ── Step 2: Patch ConfigMap to use mock facilitator ─────────────────
	// Save original for restore.
	originalCM, err := kubectlOutput(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", "json")
	if err != nil {
		t.Fatalf("kubectl get cm (json): %v", err)
	}

	patchFacilitatorURL(t, kubectlBin, kubeconfig, cmYAML, mockFac.clusterURL)
	t.Cleanup(func() {
		restoreConfigMap(t, kubectlBin, kubeconfig, originalCM)
	})

	// Wait for x402-verifier to reload the config (poll-based watcher, ~5s interval).
	t.Log("Waiting for x402-verifier config reload...")
	waitForVerifierReload(t, kubectlBin, kubeconfig, mockFac.clusterURL)

	// ── Step 3: Request WITHOUT payment → 402 ──────────────────────────
	t.Log("Step 3: Request without payment (expect 402)")
	resp402 := httpPost(t, fmt.Sprintf("http://obol.stack:8080%s", routePath),
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"say hello"}],"stream":false}`,
		nil)
	defer resp402.Body.Close()

	if resp402.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp402.Body)
		t.Fatalf("expected 402 without payment, got %d: %s", resp402.StatusCode, string(body))
	}

	// Parse 402 body to verify payment requirements.
	body402, _ := io.ReadAll(resp402.Body)
	var payReq struct {
		X402Version int `json:"x402Version"`
		Accepts     []struct {
			PayTo   string `json:"payTo"`
			Network string `json:"network"`
			Amount  string `json:"maxAmountRequired"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(body402, &payReq); err != nil {
		t.Fatalf("failed to parse 402 body: %v\nbody: %s", err, string(body402))
	}
	t.Logf("402 response: version=%d, accepts=%d, payTo=%s, amount=%s",
		payReq.X402Version, len(payReq.Accepts),
		payReq.Accepts[0].PayTo, payReq.Accepts[0].Amount)

	// ── Step 4: Request WITH payment → 200 + inference ─────────────────
	t.Log("Step 4: Request with mock payment (expect 200 + inference)")
	paymentHeader := buildTestPaymentHeader(t)
	resp200 := httpPost(t, fmt.Sprintf("http://obol.stack:8080%s", routePath),
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"Say exactly: hello world"}],"stream":false}`,
		map[string]string{"X-PAYMENT": paymentHeader})
	defer resp200.Body.Close()

	body200, _ := io.ReadAll(resp200.Body)

	if resp200.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid payment, got %d: %s", resp200.StatusCode, string(body200))
	}

	// Verify we got an actual Ollama inference response.
	var ollamaResp struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body200, &ollamaResp); err != nil {
		t.Logf("Response body: %s", string(body200))
		t.Fatalf("failed to parse Ollama response: %v", err)
	}

	if ollamaResp.Model == "" && len(ollamaResp.Choices) == 0 {
		t.Logf("Response body: %s", string(body200))
		t.Fatal("expected Ollama inference response with model and choices")
	}

	t.Logf("Inference response: model=%s", ollamaResp.Model)
	if len(ollamaResp.Choices) > 0 {
		content := ollamaResp.Choices[0].Message.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		t.Logf("Response content: %s", content)
	}

	// ── Step 5: Verify mock facilitator was called ──────────────────────
	if mockFac.verifyCalls() == 0 {
		t.Error("mock facilitator /verify was never called")
	}
	if mockFac.settleCalls() == 0 {
		t.Error("mock facilitator /settle was never called")
	}
	t.Logf("Facilitator calls: verify=%d, settle=%d",
		mockFac.verifyCalls(), mockFac.settleCalls())

	t.Log("Full sell-side lifecycle complete: offer → 402 → payment → 200 (inference)")
}

// ── Test infrastructure ─────────────────────────────────────────────────────

type clusterConfig struct {
	configDir string
	binDir    string
	dataDir   string
}

func requireClusterConfig(t *testing.T) clusterConfig {
	t.Helper()
	cfg := clusterConfig{
		configDir: os.Getenv("OBOL_CONFIG_DIR"),
		binDir:    os.Getenv("OBOL_BIN_DIR"),
		dataDir:   os.Getenv("OBOL_DATA_DIR"),
	}
	if cfg.configDir == "" || cfg.binDir == "" {
		t.Skip("OBOL_CONFIG_DIR and OBOL_BIN_DIR must be set")
	}
	kubeconfigPath := filepath.Join(cfg.configDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err != nil {
		t.Skipf("kubeconfig not found: %v", err)
	}
	return cfg
}

type hostMockFacilitator struct {
	port       int
	clusterURL string
	_verify    int32
	_settle    int32
}

func (f *hostMockFacilitator) verifyCalls() int32 {
	return f._verify
}

func (f *hostMockFacilitator) settleCalls() int32 {
	return f._settle
}

func startHostMockFacilitator(t *testing.T) *hostMockFacilitator {
	t.Helper()

	// Find a free port.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	fac := &hostMockFacilitator{port: port}
	// On macOS with k3d, the host is reachable as host.docker.internal.
	fac.clusterURL = fmt.Sprintf("http://host.docker.internal:%d", port)

	mux := http.NewServeMux()

	mux.HandleFunc("/supported", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"kinds":[{"x402Version":1,"scheme":"exact","network":"base-sepolia"}]}`)
	})

	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		fac._verify++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isValid":true,"payer":"0xmockpayer"}`)
	})

	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		fac._settle++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"transaction":"0xmocktxhash","network":"base-sepolia"}`)
	})

	// Use a specific listener on the chosen port.
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("listen on port %d: %v", port, err)
	}

	server := &http.Server{Handler: mux}
	go server.Serve(listener)

	t.Cleanup(func() {
		server.Close()
	})

	// Wait for server to be ready.
	for i := 0; i < 10; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/supported", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fac
}

func buildTestPaymentHeader(t *testing.T) string {
	t.Helper()
	p := x402lib.PaymentPayload{
		X402Version: 1,
		Scheme:      "exact",
		Network:     x402lib.BaseSepolia.NetworkID,
		Payload: map[string]any{
			"signature": "0xmocksignature",
			"authorization": map[string]any{
				"from":        "0x1234567890123456789012345678901234567890",
				"to":          "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				"value":       "1000",
				"validAfter":  "0",
				"validBefore": "9999999999",
				"nonce":       "0xabcdef",
			},
		},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payment: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func extractRoutePath(pricingYAML string) string {
	// Extract the first route pattern and convert from glob to path.
	// Pattern format: "/services/qwen35/*"  → path: "/services/qwen35/v1/chat/completions"
	for _, line := range strings.Split(pricingYAML, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- pattern:") || strings.HasPrefix(line, "pattern:") {
			pattern := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "pattern:"), " \"'")
			// Convert glob pattern to a concrete path for testing.
			// "/services/qwen35/*" → "/services/qwen35/v1/chat/completions"
			path := strings.TrimSuffix(pattern, "/*")
			path = strings.TrimSuffix(path, "/*")
			return path + "/v1/chat/completions"
		}
	}
	return ""
}

func patchFacilitatorURL(t *testing.T, kubectlBin, kubeconfig, currentYAML, newURL string) {
	t.Helper()

	// Replace the facilitatorURL in the pricing YAML.
	updated := currentYAML
	for _, line := range strings.Split(currentYAML, "\n") {
		if strings.Contains(line, "facilitatorURL:") {
			updated = strings.Replace(updated, line, fmt.Sprintf(`facilitatorURL: "%s"`, newURL), 1)
			break
		}
	}

	// Patch the ConfigMap.
	patchJSON, _ := json.Marshal(map[string]any{
		"data": map[string]string{
			"pricing.yaml": updated,
		},
	})

	if err := kubectlRun(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", fmt.Sprintf("-p=%s", string(patchJSON))); err != nil {
		t.Fatalf("patch ConfigMap: %v", err)
	}
	t.Logf("Patched x402-pricing facilitatorURL to %s", newURL)
}

func restoreConfigMap(t *testing.T, kubectlBin, kubeconfig, originalJSON string) {
	// Extract original data from the saved JSON.
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(originalJSON), &cm); err != nil {
		t.Logf("Warning: could not restore ConfigMap: %v", err)
		return
	}

	patchJSON, _ := json.Marshal(map[string]any{
		"data": cm.Data,
	})

	if err := kubectlRun(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing", "-n", "x402",
		"--type=merge", fmt.Sprintf("-p=%s", string(patchJSON))); err != nil {
		t.Logf("Warning: could not restore ConfigMap: %v", err)
	} else {
		t.Log("Restored original x402-pricing ConfigMap")
	}
}

func waitForVerifierReload(t *testing.T, kubectlBin, kubeconfig, expectedURL string) {
	t.Helper()

	// Force restart the verifier so it picks up the new ConfigMap immediately.
	// Stakater Reloader would do this eventually, but explicit restart is faster.
	if err := kubectlRun(kubectlBin, kubeconfig, "rollout", "restart",
		"deploy/x402-verifier", "-n", "x402"); err != nil {
		t.Fatalf("rollout restart x402-verifier: %v", err)
	}

	// Wait for rollout to complete (new pod up and ready).
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		// Check if the verifier startup log shows the expected facilitator URL.
		logs, err := kubectlOutput(kubectlBin, kubeconfig, "logs", "deploy/x402-verifier",
			"-n", "x402", "--tail=10")
		if err == nil && strings.Contains(logs, expectedURL) {
			t.Log("Verifier restarted with updated facilitator URL")
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Log("Warning: did not confirm verifier restart with new URL (continuing anyway)")
}

func httpPost(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP POST %s: %v", url, err)
	}
	return resp
}
