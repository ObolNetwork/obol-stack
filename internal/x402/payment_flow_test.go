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
	"time"

	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/testutil"
)

// TestIntegration_FullPaymentFlow tests the complete payment flow:
//   - ServiceOffer must already exist and be Ready (e.g. "test-qwen")
//   - Facilitator must be running on host:4040
//   - Anvil fork must be running on host:8545 with funded buyer
//
// Usage:
//
//	export OBOL_CONFIG_DIR=... OBOL_BIN_DIR=... OBOL_DATA_DIR=...
//	go test -tags integration -v -run TestIntegration_FullPaymentFlow -timeout 10m ./internal/x402/
func TestIntegration_FullPaymentFlow(t *testing.T) {
	cfg := requireClusterConfig(t)
	kubectlBin := filepath.Join(cfg.binDir, "kubectl")
	kubeconfig := filepath.Join(cfg.configDir, "kubeconfig.yaml")

	// Check x402-verifier pods are running.
	out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "pods", "-n", "x402",
		"-l", "app=x402-verifier", "--no-headers")
	if err != nil || !strings.Contains(out, "Running") {
		t.Skip("x402-verifier not running")
	}

	// Check ServiceOffer test-qwen exists and is Ready.
	soOut, err := kubectl.Output(kubectlBin, kubeconfig, "get", "serviceoffer", "test-qwen",
		"-n", "llm", "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
	if err != nil || soOut != "True" {
		t.Skipf("ServiceOffer test-qwen not Ready (status=%q, err=%v)", soOut, err)
	}

	// Check facilitator is running.
	facResp, err := http.Get("http://localhost:4040/supported")
	if err != nil {
		t.Skip("facilitator not running on localhost:4040")
	}
	facResp.Body.Close()

	// Verify Anvil fork is running.
	anvilResp, err := http.Post("http://localhost:8545", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`))
	if err != nil {
		t.Skip("Anvil fork not running on localhost:8545")
	}
	anvilResp.Body.Close()

	// Buyer: Anvil account #2
	buyerKey := "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"
	payTo := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	amount := "1000" // 0.001 USDC in micro-units (6 decimals)

	routePath := "/services/test-qwen/v1/chat/completions"

	// ── Step 1: Request WITHOUT payment → 402 ──────────────────────────
	t.Log("Step 1: Request without payment (expect 402)")
	resp402 := httpPost(t, fmt.Sprintf("http://obol.stack:8080%s", routePath),
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"say hello"}],"stream":false}`,
		nil)
	defer resp402.Body.Close()

	if resp402.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp402.Body)
		t.Fatalf("expected 402, got %d: %s", resp402.StatusCode, string(body))
	}
	t.Log("  ✓ Got 402 without payment")

	// ── Step 2: Sign real EIP-712 payment and send WITH header → 200 ───
	t.Log("Step 2: Request with real EIP-712 signed payment (expect 200)")
	paymentHeader := testutil.SignRealPaymentHeader(t, buyerKey, payTo, amount, 84532)

	client := &http.Client{Timeout: 180 * time.Second}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://obol.stack:8080%s", routePath), strings.NewReader(
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"Say exactly: payment verified"}],"max_tokens":50,"stream":false}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp200, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST with payment: %v", err)
	}
	defer resp200.Body.Close()

	body200, _ := io.ReadAll(resp200.Body)

	if resp200.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with payment, got %d: %s", resp200.StatusCode, string(body200))
	}

	// Verify inference response.
	var result struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body200, &result); err != nil {
		t.Logf("Response body: %s", string(body200))
		t.Fatalf("parse response: %v", err)
	}

	t.Logf("  ✓ Got 200 with inference: model=%s", result.Model)
	if len(result.Choices) > 0 {
		content := result.Choices[0].Message.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		t.Logf("  Response: %s", content)
	}

	t.Log("✓ Full payment flow verified: 402 → sign EIP-712 → 200 + inference")
}

// TestIntegration_FullPaymentFlow_CloudflareTunnel is the same as above
// but routes through the Cloudflare tunnel URL to prove public access works.
func TestIntegration_FullPaymentFlow_CloudflareTunnel(t *testing.T) {
	tunnelURL := os.Getenv("TUNNEL_URL")
	if tunnelURL == "" {
		t.Skip("TUNNEL_URL not set")
	}

	buyerKey := "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"
	payTo := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	amount := "1000"

	routePath := "/services/test-qwen/v1/chat/completions"
	url := fmt.Sprintf("%s%s", strings.TrimRight(tunnelURL, "/"), routePath)

	// Step 1: 402 through tunnel.
	t.Logf("Step 1: 402 through tunnel %s", tunnelURL)
	resp402 := httpPost(t, url,
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"say hello"}],"stream":false}`,
		nil)
	defer resp402.Body.Close()

	if resp402.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp402.Body)
		t.Fatalf("expected 402, got %d: %s", resp402.StatusCode, string(body))
	}
	t.Log("  ✓ Got 402 through tunnel")

	// Step 2: Paid request through tunnel.
	t.Log("Step 2: Paid request through tunnel (expect 200)")
	paymentHeader := testutil.SignRealPaymentHeader(t, buyerKey, payTo, amount, 84532)

	client := &http.Client{Timeout: 180 * time.Second}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(
		`{"model":"qwen3.5:35b","messages":[{"role":"user","content":"Say exactly: paid through tunnel"}],"max_tokens":50,"stream":false}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp200, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST with payment: %v", err)
	}
	defer resp200.Body.Close()

	body200, _ := io.ReadAll(resp200.Body)

	if resp200.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp200.StatusCode, string(body200))
	}

	t.Logf("  ✓ Got 200 through tunnel: %s", string(body200)[:min(100, len(body200))])
	t.Log("✓ Full payment flow through Cloudflare tunnel verified")
}
