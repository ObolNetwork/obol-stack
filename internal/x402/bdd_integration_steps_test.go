//go:build integration

package x402

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/testutil"
	"github.com/cucumber/godog"
)

// integrationWorld holds shared state for integration-tier BDD scenarios.
// Each scenario gets a fresh world. Background steps bootstrap Anvil,
// facilitator, and verifier patching per scenario.
type integrationWorld struct {
	t *testing.T

	// Cluster access (from package-level vars set by TestMain).
	kubectlBin string
	kubeconfig string

	// Per-scenario infrastructure.
	anvil       *testutil.AnvilFork
	facilitator *testutil.MockFacilitator

	// Tunnel.
	tunnelURL string

	// Pricing route (from TestMain bootstrap).
	routePath string
	payTo     string

	// Buyer.
	buyerKeyHex string

	// Request/Response state.
	lastResponse   *http.Response
	lastBody       []byte
	lastStatusCode int

	// Parsed 402.
	parsed402 *parsed402Response

	// Signed payment header.
	signedPaymentHeader string
}

func newIntegrationWorld(t *testing.T) *integrationWorld {
	return &integrationWorld{
		t:          t,
		kubectlBin: integrationKubectlBin,
		kubeconfig: integrationKubeconfig,
		routePath:  integrationRoutePath,
		payTo:      integrationPayTo,
	}
}

func (w *integrationWorld) cleanup() {
	// Anvil, facilitator, and verifier restore are handled by t.Cleanup.
}

// ── Step registration ────────────────────────────────────────────────────────

func registerIntegrationSteps(ctx *godog.ScenarioContext, w *integrationWorld) {
	// ── Background / Given ───────────────────────────────────────────

	ctx.Given(`^an Anvil fork of Base Sepolia is running$`, func() error {
		if !integrationReady {
			return godog.ErrPending
		}

		anvil := testutil.StartAnvilFork(w.t)
		w.anvil = anvil
		w.t.Logf("integration: Anvil fork on port %d", anvil.Port)
		return nil
	})

	ctx.Given(`^the buyer has (\d+) USDC on the fork$`, func(amount int) error {
		if w.anvil == nil {
			return fmt.Errorf("Anvil fork not running")
		}
		if w.buyerKeyHex == "" {
			// Use default Anvil account #2 for now; will be overridden by
			// "a buyer with Anvil key" step if present.
			w.buyerKeyHex = "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6"
		}

		buyerAddr := testutil.AnvilKeyAddress(w.t, w.buyerKeyHex)
		// amount is in USDC (6 decimals).
		microUnits := new(big.Int).Mul(big.NewInt(int64(amount)), big.NewInt(1_000_000))
		w.anvil.MintUSDC(w.t, buyerAddr.Hex(), microUnits)
		w.t.Logf("integration: minted %d USDC to %s", amount, buyerAddr.Hex())
		return nil
	})

	ctx.Given(`^a facilitator is running against the fork$`, func() error {
		w.facilitator = testutil.StartMockFacilitator(w.t)
		w.t.Logf("integration: mock facilitator on port %d, cluster URL %s",
			w.facilitator.Port, w.facilitator.ClusterURL)
		return nil
	})

	ctx.Given(`^the x402-verifier is patched to use the facilitator$`, func() error {
		if w.facilitator == nil {
			return fmt.Errorf("facilitator not running")
		}
		if w.kubectlBin == "" {
			return godog.ErrPending
		}

		testutil.PatchVerifierFacilitator(w.t, w.kubectlBin, w.kubeconfig, w.facilitator.ClusterURL)
		return nil
	})

	ctx.Given(`^a buyer with Anvil key "([^"]*)"$`, func(keyHex string) error {
		w.buyerKeyHex = keyHex
		return nil
	})

	ctx.Given(`^the Cloudflare tunnel is reachable$`, func() error {
		tunnelURL := os.Getenv("TUNNEL_URL")
		if tunnelURL == "" {
			// Try auto-detect from cloudflared logs.
			if w.kubectlBin != "" {
				tunnelURL = detectTunnelURL(w)
			}
		}
		if tunnelURL == "" {
			return godog.ErrPending
		}
		w.tunnelURL = strings.TrimRight(tunnelURL, "/")

		// Quick health check.
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Head(w.tunnelURL + "/")
		if err != nil {
			return fmt.Errorf("tunnel unreachable at %s: %w", w.tunnelURL, err)
		}
		resp.Body.Close()
		return nil
	})

	// ── When (local cluster) ─────────────────────────────────────────

	ctx.When(`^the buyer sends an unpaid POST to the priced route$`, func() error {
		url := fmt.Sprintf("http://obol.stack:8080%s", w.routePath)
		return w.doInferencePost(url, nil)
	})

	ctx.When(`^the buyer signs an EIP-712 payment from the 402 response$`, func() error {
		if w.parsed402 == nil || len(w.parsed402.Accepts) == 0 {
			return fmt.Errorf("must receive 402 before signing")
		}
		if w.buyerKeyHex == "" {
			return fmt.Errorf("buyer key not set")
		}

		accept := w.parsed402.Accepts[0]
		payTo := accept.PayTo
		amount := accept.Amount
		if payTo == "" {
			payTo = w.payTo
		}
		if amount == "" {
			return fmt.Errorf("no amount in 402 accepts")
		}

		w.signedPaymentHeader = testutil.SignRealPaymentHeader(
			w.t, w.buyerKeyHex, payTo, amount, 84532,
		)
		return nil
	})

	ctx.When(`^the buyer constructs payment from the discovered pricing$`, func() error {
		if w.parsed402 == nil || len(w.parsed402.Accepts) == 0 {
			return fmt.Errorf("must receive 402 before constructing payment")
		}

		accept := w.parsed402.Accepts[0]
		w.signedPaymentHeader = testutil.SignRealPaymentHeader(
			w.t, w.buyerKeyHex, accept.PayTo, accept.Amount, 84532,
		)
		return nil
	})

	ctx.When(`^the buyer sends the paid POST to the priced route$`, func() error {
		if w.signedPaymentHeader == "" {
			return fmt.Errorf("no signed payment header")
		}
		url := fmt.Sprintf("http://obol.stack:8080%s", w.routePath)
		return w.doInferencePost(url, map[string]string{"X-PAYMENT": w.signedPaymentHeader})
	})

	// ── When (tunnel) ────────────────────────────────────────────────

	ctx.When(`^the buyer sends an unpaid POST through the tunnel$`, func() error {
		if w.tunnelURL == "" {
			return fmt.Errorf("tunnel URL not set")
		}
		url := fmt.Sprintf("%s%s", w.tunnelURL, w.routePath)
		return w.doInferencePost(url, nil)
	})

	ctx.When(`^the buyer sends the paid POST through the tunnel$`, func() error {
		if w.signedPaymentHeader == "" {
			return fmt.Errorf("no signed payment header")
		}
		url := fmt.Sprintf("%s%s", w.tunnelURL, w.routePath)
		return w.doInferencePost(url, map[string]string{"X-PAYMENT": w.signedPaymentHeader})
	})

	// ── Then ─────────────────────────────────────────────────────────

	ctx.Then(`^the response status is (\d+)$`, func(expected int) error {
		if w.lastStatusCode != expected {
			return fmt.Errorf("expected status %d, got %d (body: %s)",
				expected, w.lastStatusCode, truncate(w.lastBody, 500))
		}
		return nil
	})

	ctx.Then(`^the response body contains x402Version (\d+)$`, func(version int) error {
		if !strings.Contains(string(w.lastBody), fmt.Sprintf(`"x402Version":%d`, version)) &&
			!strings.Contains(string(w.lastBody), fmt.Sprintf(`"x402Version": %d`, version)) {
			return fmt.Errorf("x402Version %d not found in response: %s", version, truncate(w.lastBody, 300))
		}
		return nil
	})

	ctx.Then(`^the response body contains a valid accepts array$`, func() error {
		if w.parsed402 == nil {
			return fmt.Errorf("no parsed 402 response")
		}
		if len(w.parsed402.Accepts) == 0 {
			return fmt.Errorf("empty accepts array in 402")
		}
		a := w.parsed402.Accepts[0]
		if a.PayTo == "" || a.Network == "" || a.Amount == "" {
			return fmt.Errorf("incomplete accepts entry: %+v", a)
		}
		return nil
	})

	ctx.Then(`^the response contains a real inference result$`, func() error {
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(w.lastBody, &result); err != nil {
			return fmt.Errorf("parse inference response: %w (body: %s)", err, truncate(w.lastBody, 300))
		}
		if len(result.Choices) == 0 {
			return fmt.Errorf("no choices in inference response: %s", truncate(w.lastBody, 300))
		}
		content := result.Choices[0].Message.Content
		if content == "" {
			return fmt.Errorf("empty inference content")
		}
		w.t.Logf("integration: inference content = %s", truncateStr(content, 100))
		return nil
	})

	ctx.Then(`^the response contains non-empty inference content$`, func() error {
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(w.lastBody, &result); err != nil {
			return fmt.Errorf("parse: %w (body: %s)", err, truncate(w.lastBody, 300))
		}
		if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			return fmt.Errorf("expected non-empty inference content: %s", truncate(w.lastBody, 300))
		}
		return nil
	})

	ctx.Then(`^the 402 response contains payTo and price and network$`, func() error {
		if w.parsed402 == nil || len(w.parsed402.Accepts) == 0 {
			return fmt.Errorf("no parsed 402 response with accepts")
		}
		a := w.parsed402.Accepts[0]
		if a.PayTo == "" {
			return fmt.Errorf("payTo is empty")
		}
		if a.Amount == "" {
			return fmt.Errorf("price (maxAmountRequired) is empty")
		}
		if a.Network == "" {
			return fmt.Errorf("network is empty")
		}
		w.t.Logf("integration: discovered payTo=%s price=%s network=%s", a.PayTo, a.Amount, a.Network)
		return nil
	})

	ctx.Then(`^the facilitator received at least (\d+) verify calls?$`, func(min int) error {
		if w.facilitator == nil {
			return fmt.Errorf("no facilitator running")
		}
		actual := int(w.facilitator.VerifyCalls.Load())
		if actual < min {
			return fmt.Errorf("expected at least %d verify calls, got %d", min, actual)
		}
		return nil
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// doInferencePost sends a POST to the given URL with an OpenAI-compatible body.
func (w *integrationWorld) doInferencePost(url string, headers map[string]string) error {
	body := `{"model":"test","messages":[{"role":"user","content":"Say hello in exactly 3 words"}],"max_tokens":50,"stream":false}`

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	w.lastResponse = resp
	w.lastBody = respBody
	w.lastStatusCode = resp.StatusCode

	// Auto-parse 402 responses.
	if resp.StatusCode == http.StatusPaymentRequired {
		var parsed parsed402Response
		if err := json.Unmarshal(respBody, &parsed); err == nil {
			w.parsed402 = &parsed
		}
	}

	return nil
}

// detectTunnelURL tries to extract the tunnel URL from cloudflared logs.
func detectTunnelURL(w *integrationWorld) string {
	out, err := kubectl.Output(w.kubectlBin, w.kubeconfig, "logs",
		"-n", "traefik", "-l", "app.kubernetes.io/name=cloudflared",
		"--tail=50")
	if err != nil {
		return ""
	}
	// Look for "https://<uuid>.cfargotunnel.com" or similar in logs.
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, "https://"); idx >= 0 {
			candidate := line[idx:]
			// Trim at first whitespace.
			if space := strings.IndexAny(candidate, " \t\n"); space > 0 {
				candidate = candidate[:space]
			}
			if strings.Contains(candidate, "trycloudflare.com") || strings.Contains(candidate, "cfargotunnel.com") {
				return strings.TrimRight(candidate, "/")
			}
		}
	}
	return ""
}

func truncate(b []byte, max int) string {
	return truncateStr(string(b), max)
}

func truncateStr(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
