//go:build integration

package x402

import (
	"context"
	"encoding/json"
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

	// 4. Deploy ServiceOffer.
	log.Println("=== Deploying ServiceOffer ===")
	if err := deployServiceOffer(kubectlBin, kubeconfigPath); err != nil {
		teardown(obolBin)
		log.Fatalf("deploy ServiceOffer: %v", err)
	}

	// 5. Wait for pricing route to appear in ConfigMap.
	log.Println("=== Waiting for pricing route reconciliation ===")
	routePath, err := waitForPricingRoute(kubectlBin, kubeconfigPath, 180*time.Second)
	if err != nil {
		teardown(obolBin)
		log.Fatalf("pricing route not reconciled: %v", err)
	}
	integrationRoutePath = routePath
	integrationPayTo = serviceOfferPayTo
	integrationReady = true

	log.Printf("=== Bootstrap complete: route=%s payTo=%s ===", routePath, serviceOfferPayTo)

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

func deployServiceOffer(kubectlBin, kubeconfig string) error {
	manifest := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  type: http
  upstream:
    service: llmspy
    namespace: llm
    port: 8000
    healthPath: /health
  payment:
    scheme: exact
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
`, serviceOfferName, serviceOfferNamespace, serviceOfferPayTo)

	return kubectl.Apply(kubectlBin, kubeconfig, []byte(manifest))
}

func waitForPricingRoute(kubectlBin, kubeconfig string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmYAML, err := kubectl.Output(kubectlBin, kubeconfig, "get", "cm", "x402-pricing",
			"-n", "x402", "-o", `jsonpath={.data.pricing\.yaml}`)
		if err == nil && strings.Contains(cmYAML, "pattern:") {
			routePath := extractRoutePath(cmYAML)
			if routePath != "" {
				log.Printf("Pricing route found: %s", routePath)
				return routePath, nil
			}
		}
		log.Println("Waiting for pricing route in ConfigMap...")
		time.Sleep(10 * time.Second)
	}
	return "", fmt.Errorf("timeout waiting for pricing route in x402-pricing ConfigMap")
}

func teardown(obolBin string) {
	log.Println("=== Tearing down stack ===")

	// Delete the ServiceOffer first to clean up agent-managed resources.
	if integrationKubectlBin != "" && integrationKubeconfig != "" {
		_ = kubectl.RunSilent(integrationKubectlBin, integrationKubeconfig,
			"delete", "serviceoffer", serviceOfferName, "-n", serviceOfferNamespace, "--ignore-not-found")
	}

	if err := runObol(obolBin, "stack", "down"); err != nil {
		log.Printf("Warning: obol stack down failed: %v", err)
	}
	if err := runObol(obolBin, "stack", "purge", "-f"); err != nil {
		log.Printf("Warning: obol stack purge failed: %v", err)
	}
}

// ── ServiceOffer wait helper (JSON-based) ────────────────────────────────────

// waitForServiceOfferReady polls until the ServiceOffer has condition Ready=True.
// Not used in the current flow (we wait for pricing route instead) but available
// for future use.
func waitForServiceOfferReady(kubectlBin, kubeconfig string, timeout time.Duration) error {
	type condition struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	type soStatus struct {
		Conditions []condition `json:"conditions"`
	}
	type serviceOffer struct {
		Status soStatus `json:"status"`
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "serviceoffer",
			serviceOfferName, "-n", serviceOfferNamespace, "-o", "json")
		if err == nil {
			var so serviceOffer
			if json.Unmarshal([]byte(out), &so) == nil {
				for _, c := range so.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						return nil
					}
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for ServiceOffer %s/%s to be Ready", serviceOfferNamespace, serviceOfferName)
}
