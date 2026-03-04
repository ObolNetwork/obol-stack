//go:build integration

package x402

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/testutil"
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
	out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "pods", "-n", "x402",
		"-l", "app=x402-verifier", "--no-headers")
	if err != nil {
		t.Fatalf("kubectl get pods: %v", err)
	}
	if !strings.Contains(out, "Running") {
		t.Skip("x402-verifier not running")
	}

	// Check that a pricing route exists (from monetize.py reconciliation).
	cmYAML, err := kubectl.Output(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
		"-n", "x402", "-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		t.Fatalf("kubectl get cm: %v", err)
	}
	if !strings.Contains(cmYAML, "pattern:") {
		t.Skip("no pricing routes configured — run: obol sell http + monetize.py process first")
	}

	// Extract the route pattern to know which path to hit.
	routePath := extractRoutePath(cmYAML)
	if routePath == "" {
		t.Fatal("could not extract route path from pricing config")
	}
	t.Logf("Testing route: %s", routePath)

	// ── Step 1: Start mock facilitator on host ──────────────────────────
	mockFac := testutil.StartMockFacilitator(t)
	t.Logf("Mock facilitator running on port %d (cluster URL: %s)", mockFac.Port, mockFac.ClusterURL)

	// ── Step 2: Patch ConfigMap to use mock facilitator ─────────────────
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, mockFac.ClusterURL)

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
	paymentHeader := testutil.TestPaymentHeader(t, payReq.Accepts[0].PayTo)
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
	if mockFac.VerifyCalls.Load() == 0 {
		t.Error("mock facilitator /verify was never called")
	}
	if mockFac.SettleCalls.Load() == 0 {
		t.Error("mock facilitator /settle was never called")
	}
	t.Logf("Facilitator calls: verify=%d, settle=%d",
		mockFac.VerifyCalls.Load(), mockFac.SettleCalls.Load())

	t.Log("Full sell-side lifecycle complete: offer → 402 → payment → 200 (inference)")
}

// ── Test infrastructure (kept: test-specific helpers) ────────────────────────

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

// httpPost and mustQuoteJSON are in helpers_test.go (shared with non-integration tests).
