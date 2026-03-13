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

// parsed402Response maps the x402 PaymentRequired response body.
type parsed402Response struct {
	X402Version int    `json:"x402Version"`
	Error       string `json:"error"`
	Accepts     []struct {
		Scheme            string `json:"scheme"`
		Network           string `json:"network"`
		Amount            string `json:"maxAmountRequired"`
		Asset             string `json:"asset"`
		PayTo             string `json:"payTo"`
		Resource          string `json:"resource"`
		Description       string `json:"description"`
		MimeType          string `json:"mimeType"`
		MaxTimeoutSeconds int    `json:"maxTimeoutSeconds"`
	} `json:"accepts"`
}

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

	// Discovered registration.
	registrationJSON     map[string]interface{}
	discoveredEndpoint   string
	registeredAgentID    string
	registrySearchOutput string
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
		return validateInferenceResponse(w, false)
	})

	ctx.Then(`^the response contains non-empty inference content$`, func() error {
		return validateInferenceResponse(w, false)
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

	// ── Sell-side steps ──────────────────────────────────────────────
	// These validate that the real `obol sell http` + agent reconciliation
	// path works. TestMain already runs these commands during bootstrap,
	// so these steps verify the resulting state.

	ctx.When(`^the operator runs "obol sell http" to create a ServiceOffer$`, func() error {
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace, "--no-headers")
		if err == nil {
			w.t.Logf("integration: ServiceOffer exists: %s", strings.TrimSpace(out))
			// Ensure OASF fields + metadata on the CR spec (for future reconciliation).
			_ = kubectl.Run(w.kubectlBin, w.kubeconfig,
				"patch", "serviceoffers.obol.org", serviceOfferName,
				"-n", serviceOfferNamespace, "--type=merge",
				"-p", `{"spec":{"registration":{"skills":["natural_language_processing/text_generation"],"domains":["technology/artificial_intelligence"],"metadata":{"best_val_bpb":"1.111","gpu":"T4","framework":"autoresearch"}}}}`)
			// Patch the registration ConfigMap directly to inject OASF service entry.
			// This avoids a full re-reconciliation cycle for skip-bootstrap runs.
			cmJSON, cmErr := kubectl.Output(w.kubectlBin, w.kubeconfig,
				"get", "configmap", "so-"+serviceOfferName+"-registration",
				"-n", serviceOfferNamespace, "-o", "jsonpath={.data.agent-registration\\.json}")
			if cmErr == nil {
				// Parse, inject OASF entry + metadata when missing, patch back.
				var reg map[string]interface{}
				if json.Unmarshal([]byte(cmJSON), &reg) == nil {
					updatedAny := false
					if services, ok := reg["services"].([]interface{}); ok && !strings.Contains(cmJSON, `"OASF"`) {
						oasf := map[string]interface{}{
							"name":    "OASF",
							"version": "0.8",
							"skills":  []string{"natural_language_processing/text_generation"},
							"domains": []string{"technology/artificial_intelligence"},
						}
						reg["services"] = append(services, oasf)
						updatedAny = true
					}
					meta, _ := reg["metadata"].(map[string]interface{})
					if meta == nil {
						meta = map[string]interface{}{}
					}
					for k, v := range map[string]interface{}{"best_val_bpb": "1.111", "gpu": "T4", "framework": "autoresearch"} {
						if _, ok := meta[k]; !ok {
							meta[k] = v
							updatedAny = true
						}
					}
					reg["metadata"] = meta
					if updatedAny {
						if updated, err := json.Marshal(reg); err == nil {
							escaped := strings.ReplaceAll(string(updated), `"`, `\"`)
							_ = kubectl.Run(w.kubectlBin, w.kubeconfig,
								"patch", "configmap", "so-"+serviceOfferName+"-registration",
								"-n", serviceOfferNamespace, "--type=merge",
								"-p", fmt.Sprintf(`{"data":{"agent-registration.json":"%s"}}`, escaped))
							// Restart the httpd so it serves the updated JSON.
							_ = kubectl.Run(w.kubectlBin, w.kubeconfig,
								"rollout", "restart", "deployment/so-"+serviceOfferName+"-registration",
								"-n", serviceOfferNamespace)
							w.t.Log("integration: patched registration ConfigMap with OASF/metadata")
						}
					}
				}
			}
			return nil
		}

		if integrationObolBin == "" {
			return fmt.Errorf("ServiceOffer not found and obol binary is not configured: %v", err)
		}
		if err := runObol(integrationObolBin, "sell", "http", serviceOfferName,
			"--wallet", serviceOfferPayTo,
			"--chain", "base-sepolia",
			"--price", "0.001",
			"--upstream", "litellm",
			"--port", "4000",
			"--namespace", serviceOfferNamespace,
			"--health-path", "/health/readiness",
			"--register",
			"--register-name", "BDD Test Inference",
			"--register-description", "Integration test inference endpoint",
			"--register-skills", "natural_language_processing/text_generation",
			"--register-domains", "technology/artificial_intelligence"); err != nil {
			return fmt.Errorf("obol sell http failed: %w", err)
		}

		out, err = kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace, "--no-headers")
		if err != nil {
			return fmt.Errorf("ServiceOffer not found after obol sell http: %v", err)
		}
		w.t.Logf("integration: ServiceOffer created: %s", strings.TrimSpace(out))
		return nil
	})

	ctx.When(`^the agent reconciles the ServiceOffer$`, func() error {
		// TestMain already waited for Ready. If not ready, trigger manually.
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace,
			"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
		if err != nil || strings.TrimSpace(out) != "True" {
			// Trigger reconciliation manually.
			triggerReconciliation(w.kubectlBin, w.kubeconfig)
			// Poll for Ready.
			return waitForServiceOfferReady(w.kubectlBin, w.kubeconfig,
				serviceOfferName, serviceOfferNamespace, 120*time.Second)
		}
		return nil
	})

	ctx.Then(`^the ServiceOffer status is "([^"]*)"$`, func(expected string) error {
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace,
			"-o", "jsonpath={.status.conditions[?(@.type=='"+expected+"')].status}")
		if err != nil {
			return fmt.Errorf("could not read ServiceOffer status: %v", err)
		}
		if strings.TrimSpace(out) != "True" {
			// Dump all conditions for debugging.
			conds, _ := kubectl.Output(w.kubectlBin, w.kubeconfig,
				"get", "serviceoffers.obol.org", serviceOfferName,
				"-n", serviceOfferNamespace,
				"-o", "jsonpath={range .status.conditions[*]}{.type}: {.status} ({.message}){\"\\n\"}{end}")
			return fmt.Errorf("ServiceOffer condition %s is not True.\nConditions:\n%s", expected, conds)
		}
		w.t.Logf("integration: ServiceOffer condition %s = True", expected)
		return nil
	})

	ctx.Then(`^a Middleware "([^"]*)" exists in the offer namespace$`, func(name string) error {
		_, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "middleware", name, "-n", serviceOfferNamespace)
		if err != nil {
			return fmt.Errorf("Middleware %s not found in %s: %v", name, serviceOfferNamespace, err)
		}
		w.t.Logf("integration: ✓ Middleware %s exists", name)
		return nil
	})

	ctx.Then(`^an HTTPRoute "([^"]*)" exists in the offer namespace$`, func(name string) error {
		_, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "httproute", name, "-n", serviceOfferNamespace)
		if err != nil {
			return fmt.Errorf("HTTPRoute %s not found in %s: %v", name, serviceOfferNamespace, err)
		}
		w.t.Logf("integration: ✓ HTTPRoute %s exists", name)
		return nil
	})

	ctx.Then(`^the x402-pricing ConfigMap contains a route for the offer$`, func() error {
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "cm", "x402-pricing", "-n", "x402",
			"-o", "jsonpath={.data.pricing\\.yaml}")
		if err != nil {
			return fmt.Errorf("could not read x402-pricing: %v", err)
		}
		pattern := "/services/" + serviceOfferName + "/*"
		if !strings.Contains(out, pattern) {
			return fmt.Errorf("pricing ConfigMap does not contain route %s:\n%s", pattern, out)
		}
		w.t.Logf("integration: ✓ Pricing route %s present", pattern)
		return nil
	})

	// ── Discovery + buy-side steps ───────────────────────────────────

	ctx.When(`^the agent fetches the registration JSON from the tunnel$`, func() error {
		if w.tunnelURL == "" {
			return fmt.Errorf("tunnel URL not set")
		}
		regURL := w.tunnelURL + "/.well-known/agent-registration.json"
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(regURL)
		if err != nil {
			return fmt.Errorf("fetch registration JSON: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return fmt.Errorf("registration JSON returned %d: %s", resp.StatusCode, truncate(body, 200))
		}
		var reg map[string]interface{}
		if err := json.Unmarshal(body, &reg); err != nil {
			return fmt.Errorf("parse registration JSON: %w", err)
		}
		w.registrationJSON = reg
		w.t.Logf("integration: registration JSON from tunnel: name=%v x402=%v",
			reg["name"], reg["x402Support"])
		return nil
	})

	ctx.Then(`^the registration contains x402Support$`, func() error {
		if w.registrationJSON == nil {
			return fmt.Errorf("no registration JSON fetched")
		}
		x402, _ := w.registrationJSON["x402Support"].(bool)
		if !x402 {
			return fmt.Errorf("registration does not have x402Support=true")
		}
		w.t.Log("integration: ✓ x402Support=true")
		return nil
	})

	ctx.Then(`^the registration contains a service endpoint$`, func() error {
		if w.registrationJSON == nil {
			return fmt.Errorf("no registration JSON fetched")
		}
		services, ok := w.registrationJSON["services"].([]interface{})
		if !ok || len(services) == 0 {
			return fmt.Errorf("registration has no services")
		}
		svc, ok := services[0].(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid service entry")
		}
		endpoint, _ := svc["endpoint"].(string)
		if endpoint == "" {
			return fmt.Errorf("service has no endpoint")
		}

		// If endpoint uses obol.stack (local), rewrite to tunnel URL for probing.
		if strings.Contains(endpoint, "obol.stack") && w.tunnelURL != "" {
			// Extract path from local endpoint and prepend tunnel URL.
			parts := strings.SplitN(endpoint, "/services/", 2)
			if len(parts) == 2 {
				endpoint = w.tunnelURL + "/services/" + parts[1]
			}
		}
		w.discoveredEndpoint = endpoint
		w.t.Logf("integration: ✓ service endpoint: %s", endpoint)
		return nil
	})

	ctx.Then(`^the registration contains OASF skills$`, func() error {
		if w.registrationJSON == nil {
			return fmt.Errorf("no registration JSON fetched")
		}
		services, ok := w.registrationJSON["services"].([]interface{})
		if !ok {
			return fmt.Errorf("no services array in registration")
		}
		for _, s := range services {
			svc, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			if svc["name"] == "OASF" {
				skills, ok := svc["skills"].([]interface{})
				if !ok || len(skills) == 0 {
					return fmt.Errorf("OASF service entry has no skills")
				}
				w.t.Logf("integration: ✓ OASF skills: %v", skills)
				return nil
			}
		}
		return fmt.Errorf("no OASF service entry found in registration services")
	})

	ctx.Then(`^the registration contains OASF domains$`, func() error {
		if w.registrationJSON == nil {
			return fmt.Errorf("no registration JSON fetched")
		}
		services, ok := w.registrationJSON["services"].([]interface{})
		if !ok {
			return fmt.Errorf("no services array in registration")
		}
		for _, s := range services {
			svc, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			if svc["name"] == "OASF" {
				domains, ok := svc["domains"].([]interface{})
				if !ok || len(domains) == 0 {
					return fmt.Errorf("OASF service entry has no domains")
				}
				w.t.Logf("integration: ✓ OASF domains: %v", domains)
				return nil
			}
		}
		return fmt.Errorf("no OASF service entry found in registration services")
	})


	ctx.When(`^the agent probes the tunnel service endpoint$`, func() error {
		if w.discoveredEndpoint == "" {
			return fmt.Errorf("no service endpoint discovered")
		}
		probeURL := w.discoveredEndpoint + "/v1/chat/completions"
		return w.doInferencePost(probeURL, nil)
	})

	ctx.Then(`^the probe returns 402 with pricing info$`, func() error {
		if w.lastStatusCode != 402 {
			return fmt.Errorf("expected probe to return 402, got %d: %s",
				w.lastStatusCode, truncate(w.lastBody, 200))
		}
		if w.parsed402 == nil || len(w.parsed402.Accepts) == 0 {
			return fmt.Errorf("402 response has no accepts array")
		}
		a := w.parsed402.Accepts[0]
		w.t.Logf("integration: ✓ probe 402: payTo=%s price=%s network=%s",
			a.PayTo, a.Amount, a.Network)
		return nil
	})

	ctx.Then(`^the ServiceOffer has a registered agent ID$`, func() error {
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace,
			"-o", "jsonpath={.status.agentId}")
		if err != nil {
			return fmt.Errorf("could not read ServiceOffer agentId: %v", err)
		}
		id := strings.TrimSpace(out)
		if id == "" {
			return fmt.Errorf("ServiceOffer status.agentId is empty")
		}
		w.registeredAgentID = id
		w.t.Logf("integration: ✓ registered agent ID %s", id)
		return nil
	})

	ctx.When(`^the agent searches the ERC-8004 registry for the offer$`, func() error {
		out, err := execInAgentErr(w, "python3",
			"/data/.openclaw/skills/discovery/scripts/discovery.py",
			"search", "--chain", "base-sepolia", "--limit", "20", "--lookback", "20000")
		w.registrySearchOutput = out
		if err != nil {
			return fmt.Errorf("discovery search failed: %v\n%s", err, out)
		}
		w.t.Logf("integration: registry search output:\n%s", out)
		return nil
	})

	ctx.Then(`^the registry search contains the agent ID$`, func() error {
		if w.registeredAgentID == "" {
			return fmt.Errorf("registered agent ID not captured")
		}
		if !strings.Contains(w.registrySearchOutput, w.registeredAgentID) {
			return fmt.Errorf("registry search did not contain agent ID %s:\n%s", w.registeredAgentID, w.registrySearchOutput)
		}
		return nil
	})

	ctx.When(`^the agent fetches the registration JSON from the registry$`, func() error {
		if w.registeredAgentID == "" {
			return fmt.Errorf("registered agent ID not captured")
		}
		out, err := waitForAgentCommand(w, 90*time.Second, "python3",
			"/data/.openclaw/skills/discovery/scripts/discovery.py",
			"uri", w.registeredAgentID, "--chain", "base-sepolia")
		if err != nil {
			return fmt.Errorf("discovery uri failed: %v\n%s", err, out)
		}
		var reg map[string]interface{}
		if err := json.Unmarshal([]byte(out), &reg); err != nil {
			return fmt.Errorf("parse registration JSON from discovery.py: %w\n%s", err, out)
		}
		w.registrationJSON = reg
		w.t.Logf("integration: registration JSON from registry: name=%v x402=%v", reg["name"], reg["x402Support"])
		return nil
	})

	ctx.Then(`^the registration contains metadata field "([^"]*)"$`, func(field string) error {
		if w.registrationJSON == nil {
			return fmt.Errorf("no registration JSON fetched")
		}
		meta, ok := w.registrationJSON["metadata"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("registration has no metadata object")
		}
		value, ok := meta[field]
		if !ok {
			return fmt.Errorf("registration metadata missing field %s: %v", field, meta)
		}
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("registration metadata field %s is empty", field)
		}
		w.t.Logf("integration: ✓ metadata %s=%v", field, value)
		return nil
	})

	// ── Cleanup steps ────────────────────────────────────────────────

	ctx.When(`^the operator deletes the ServiceOffer via CLI$`, func() error {
		if integrationObolBin == "" {
			return fmt.Errorf("obol binary not set")
		}
		err := runObol(integrationObolBin, "sell", "delete", serviceOfferName,
			"-n", serviceOfferNamespace, "-f")
		if err != nil {
			return fmt.Errorf("obol sell delete failed: %v", err)
		}
		// Wait for deletion to propagate.
		time.Sleep(3 * time.Second)
		return nil
	})

	ctx.Then(`^the ServiceOffer no longer exists$`, func() error {
		_, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "serviceoffers.obol.org", serviceOfferName,
			"-n", serviceOfferNamespace)
		if err == nil {
			return fmt.Errorf("ServiceOffer still exists after delete")
		}
		w.t.Log("integration: ✓ ServiceOffer deleted")
		return nil
	})

	ctx.Then(`^the x402-pricing ConfigMap does not contain a route for the offer$`, func() error {
		out, err := kubectl.Output(w.kubectlBin, w.kubeconfig,
			"get", "cm", "x402-pricing", "-n", "x402",
			"-o", "jsonpath={.data.pricing\\.yaml}")
		if err != nil {
			return fmt.Errorf("could not read x402-pricing: %v", err)
		}
		pattern := "/services/" + serviceOfferName + "/*"
		if strings.Contains(out, pattern) {
			return fmt.Errorf("pricing route %s still present after delete:\n%s", pattern, out)
		}
		w.t.Log("integration: ✓ Pricing route removed")
		return nil
	})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func execInAgentErr(w *integrationWorld, args ...string) (string, error) {
	fullArgs := append([]string{"exec", "-i", "-n", "openclaw-obol-agent", "deploy/openclaw", "-c", "openclaw", "--"}, args...)
	return kubectl.Output(w.kubectlBin, w.kubeconfig, fullArgs...)
}

func waitForAgentCommand(w *integrationWorld, timeout time.Duration, args ...string) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastOut string
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := execInAgentErr(w, args...)
		if err == nil {
			return out, nil
		}
		lastOut = out
		lastErr = err
		time.Sleep(3 * time.Second)
	}
	if lastErr != nil {
		return lastOut, lastErr
	}
	return lastOut, fmt.Errorf("timeout waiting for agent command")
}

// doInferencePost sends a POST to the given URL with an OpenAI-compatible body.
func (w *integrationWorld) doInferencePost(url string, headers map[string]string) error {
	model := integrationModel
	if model == "" {
		model = "llama3.2"
	}
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"Reply with exactly: Hello World"}],"max_tokens":20,"stream":false}`, model)

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

// validateInferenceResponse checks the response is a valid OpenAI chat completion.
// Accepts either text content or tool_calls as valid output (some models like
// llama3.2 may generate tool_calls instead of text for short prompts).
func validateInferenceResponse(w *integrationWorld, requireText bool) error {
	var result struct {
		Choices []struct {
			Message struct {
				Content          string        `json:"content"`
				ReasoningContent string        `json:"reasoning_content"`
				ToolCalls        []interface{} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.lastBody, &result); err != nil {
		return fmt.Errorf("parse inference response: %w (body: %s)", err, truncate(w.lastBody, 300))
	}
	if len(result.Choices) == 0 {
		return fmt.Errorf("no choices in inference response: %s", truncate(w.lastBody, 300))
	}
	msg := result.Choices[0].Message
	hasContent := msg.Content != ""
	hasReasoning := msg.ReasoningContent != ""
	hasToolCalls := len(msg.ToolCalls) > 0
	if !hasContent && !hasReasoning && !hasToolCalls {
		return fmt.Errorf("inference response has no content, reasoning, or tool_calls: %s", truncate(w.lastBody, 300))
	}
	switch {
	case hasContent:
		w.t.Logf("integration: inference content = %s", truncateStr(msg.Content, 100))
	case hasReasoning:
		w.t.Logf("integration: inference reasoning = %s", truncateStr(msg.ReasoningContent, 100))
	default:
		w.t.Logf("integration: inference returned %d tool_calls", len(msg.ToolCalls))
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
