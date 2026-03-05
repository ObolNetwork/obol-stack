//go:build integration

package x402

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/cucumber/godog"
)

// ── Package-level state set by TestMain ──────────────────────────────────────

var (
	integrationKubectlBin string
	integrationKubeconfig string
	integrationRoutePath  string
	integrationPayTo      string
	integrationObolBin    string
	integrationModel      string

	// Set to true when TestMain successfully bootstraps the cluster.
	integrationReady bool
)

const (
	serviceOfferName      = "bdd-test"
	serviceOfferNamespace = "llm"
	serviceOfferPayTo     = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
)

// TestMain bootstraps the full obol stack, deploys a ServiceOffer, waits for
// reconciliation, runs the BDD suite, and tears everything down.
func TestMain(m *testing.M) {
	if os.Getenv("OBOL_INTEGRATION_SKIP_BOOTSTRAP") == "true" {
		// Escape hatch: use a pre-existing cluster (original behavior).
		os.Exit(m.Run())
	}

	projectRoot := findProjectRoot()
	workspaceDir := filepath.Join(projectRoot, ".workspace")

	// Set environment for development mode.
	os.Setenv("OBOL_DEVELOPMENT", "true")
	os.Setenv("OBOL_CONFIG_DIR", filepath.Join(workspaceDir, "config"))
	os.Setenv("OBOL_BIN_DIR", filepath.Join(workspaceDir, "bin"))
	os.Setenv("OBOL_DATA_DIR", filepath.Join(workspaceDir, "data"))

	configDir := os.Getenv("OBOL_CONFIG_DIR")
	binDir := os.Getenv("OBOL_BIN_DIR")

	// 1. Build obol binary.
	obolBin := filepath.Join(binDir, "obol")
	log.Println("=== Building obol binary ===")
	if err := buildObol(projectRoot, obolBin); err != nil {
		log.Fatalf("build obol: %v", err)
	}
	integrationObolBin = obolBin

	// 2. Stack init + up.
	log.Println("=== Initializing stack ===")
	if err := runObol(obolBin, "stack", "init", "--backend", "k3d", "--force"); err != nil {
		log.Fatalf("obol stack init: %v", err)
	}

	log.Println("=== Starting stack (this takes 3-5 minutes) ===")
	if err := runObol(obolBin, "stack", "up"); err != nil {
		log.Fatalf("obol stack up: %v", err)
	}

	kubeconfigPath := filepath.Join(configDir, "kubeconfig.yaml")
	kubectlBin := filepath.Join(binDir, "kubectl")

	integrationKubectlBin = kubectlBin
	integrationKubeconfig = kubeconfigPath

	// 3. Wait for x402-verifier pods to be Running.
	log.Println("=== Waiting for x402-verifier to be ready ===")
	if err := waitForVerifier(kubectlBin, kubeconfigPath, 180*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("x402-verifier not ready: %v", err)
	}

	// 4. Configure LLM provider (Anthropic if key available, else Ollama default).
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		log.Println("=== Configuring Anthropic provider in llmspy ===")
		if err := runObol(obolBin, "model", "setup", "--provider", "anthropic", "--api-key", apiKey); err != nil {
			teardown(obolBin)
			log.Fatalf("obol model setup anthropic: %v", err)
		}
	} else {
		log.Println("=== No ANTHROPIC_API_KEY set, using default Ollama provider ===")
	}

	// 5. Set up pricing route, ForwardAuth middleware, and HTTPRoute directly.
	//    This bypasses the obol-agent reconciler (which requires `obol agent init`)
	//    and makes the test fully self-contained.
	log.Println("=== Setting up x402 pricing route and HTTPRoute ===")
	if err := setupPricingAndRoute(kubectlBin, kubeconfigPath); err != nil {
		teardown(obolBin)
		log.Fatalf("setup pricing/route: %v", err)
	}

	// 5. Wait for llmspy to be ready (upstream for the priced route).
	//    The llmspy image (~370MB) can take 3-5 minutes to pull on first run.
	log.Println("=== Waiting for llmspy upstream ===")
	if err := waitForPod(kubectlBin, kubeconfigPath, "llm", "app=llmspy", 300*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("llmspy not ready: %v", err)
	}

	// 6. Restart x402-verifier to pick up new pricing config.
	log.Println("=== Restarting x402-verifier to load pricing config ===")
	_ = kubectl.RunSilent(kubectlBin, kubeconfigPath, "rollout", "restart", "deployment/x402-verifier", "-n", "x402")
	if err := waitForVerifier(kubectlBin, kubeconfigPath, 120*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("x402-verifier not ready after restart: %v", err)
	}

	integrationRoutePath = "/services/" + serviceOfferName + "/v1/chat/completions"
	integrationPayTo = serviceOfferPayTo

	// Determine inference model.
	integrationModel = os.Getenv("OBOL_TEST_MODEL")
	if integrationModel == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			integrationModel = "claude-sonnet-4-20250514"
		} else {
			integrationModel = "llama3.2"
		}
	}

	integrationReady = true
	log.Printf("=== Bootstrap complete: route=%s payTo=%s model=%s ===",
		integrationRoutePath, serviceOfferPayTo, integrationModel)

	// 6. Run tests.
	code := m.Run()

	// 7. Teardown.
	teardown(obolBin)
	os.Exit(code)
}

// TestBDDIntegration runs the integration-tier BDD scenarios.
//
//	go test -tags integration -v -run TestBDDIntegration -timeout 15m ./internal/x402/
func TestBDDIntegration(t *testing.T) {
	if !integrationReady {
		t.Skip("integration bootstrap did not complete")
	}

	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			w := newIntegrationWorld(t)

			ctx.After(func(gctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
				w.cleanup()
				return gctx, nil
			})

			registerIntegrationSteps(ctx, w)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/integration_payment_flow.feature"},
			Tags:     "@integration",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("integration BDD scenarios failed")
	}
}

// ── Bootstrap helpers ────────────────────────────────────────────────────────

func findProjectRoot() string {
	// Walk up from cwd looking for go.mod.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func buildObol(projectRoot, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	// Remove any existing file (e.g., shell wrapper from obolup.sh)
	// so go build -o can write a fresh binary.
	os.Remove(outputPath)

	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/obol")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runObol(obolBin string, args ...string) error {
	cmd := exec.Command(obolBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func waitForVerifier(kubectlBin, kubeconfig string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "pods", "-n", "x402",
			"-l", "app=x402-verifier", "--no-headers")
		if err == nil && strings.Contains(out, "Running") {
			log.Println("x402-verifier is Running")
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for x402-verifier pods to be Running")
}

// setupPricingAndRoute directly patches the x402-pricing ConfigMap and creates
// the ForwardAuth Middleware + HTTPRoute. This replaces the agent reconciler
// flow, making the test independent of the obol-agent singleton.
func setupPricingAndRoute(kubectlBin, kubeconfig string) error {
	routePattern := "/services/" + serviceOfferName + "/*"

	// 1. Patch x402-pricing ConfigMap with the route.
	pricingYAML := fmt.Sprintf(`wallet: "%s"
chain: "base-sepolia"
facilitatorURL: "https://facilitator.x402.rs"
verifyOnly: false
routes:
  - pattern: "%s"
    price: "1000"
    description: "BDD test route"
`, serviceOfferPayTo, routePattern)

	patchJSON := fmt.Sprintf(`{"data":{"pricing.yaml":%s}}`, mustQuoteJSON(pricingYAML))
	if err := kubectl.RunSilent(kubectlBin, kubeconfig, "patch", "cm", "x402-pricing",
		"-n", "x402", "--type=merge", "-p", patchJSON); err != nil {
		return fmt.Errorf("patch pricing ConfigMap: %w", err)
	}
	log.Printf("Patched x402-pricing ConfigMap with route %s", routePattern)

	// 2. Create ForwardAuth Middleware + HTTPRoute.
	manifest := fmt.Sprintf(`apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: x402-bdd-test
  namespace: %s
spec:
  forwardAuth:
    address: http://x402-verifier.x402.svc.cluster.local:8080/verify
    authResponseHeaders:
      - X-Payment-Response
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: bdd-test
  namespace: %s
spec:
  parentRefs:
    - name: traefik-gateway
      namespace: traefik
      sectionName: web
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /services/%s
      filters:
        - type: ExtensionRef
          extensionRef:
            group: traefik.io
            kind: Middleware
            name: x402-bdd-test
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: llmspy
          namespace: llm
          port: 8000
`, serviceOfferNamespace, serviceOfferNamespace, serviceOfferName)

	if err := kubectl.Apply(kubectlBin, kubeconfig, []byte(manifest)); err != nil {
		return fmt.Errorf("apply middleware + HTTPRoute: %w", err)
	}
	log.Println("Created ForwardAuth middleware and HTTPRoute for bdd-test")

	return nil
}

func waitForPod(kubectlBin, kubeconfig, namespace, labelSelector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "pods", "-n", namespace,
			"-l", labelSelector, "--no-headers")
		if err == nil && strings.Contains(out, "Running") {
			log.Printf("Pod %s in %s is Running", labelSelector, namespace)
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for pod %s in %s", labelSelector, namespace)
}


func teardown(obolBin string) {
	log.Println("=== Tearing down stack ===")

	// Clean up test resources before tearing down the cluster.
	if integrationKubectlBin != "" && integrationKubeconfig != "" {
		_ = kubectl.RunSilent(integrationKubectlBin, integrationKubeconfig,
			"delete", "httproute", "bdd-test", "-n", serviceOfferNamespace, "--ignore-not-found")
		_ = kubectl.RunSilent(integrationKubectlBin, integrationKubeconfig,
			"delete", "middleware", "x402-bdd-test", "-n", serviceOfferNamespace, "--ignore-not-found")
	}

	if err := runObol(obolBin, "stack", "down"); err != nil {
		log.Printf("Warning: obol stack down failed: %v", err)
	}
	if err := runObol(obolBin, "stack", "purge", "-f"); err != nil {
		log.Printf("Warning: obol stack purge failed: %v", err)
	}
}

