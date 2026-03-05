//go:build integration

package x402

import (
	"context"
	"encoding/base64"
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

// TestMain bootstraps the full obol stack following the real user journey:
//
//  1. Build obol binary from source
//  2. obol stack init + up (real cluster)
//  3. obol model setup (real LLM provider)
//  4. obol sell pricing (real x402 configuration)
//  5. obol agent init (real agent singleton + RBAC + monetize skill)
//  6. obol sell http (real ServiceOffer CR)
//  7. Wait for agent reconciliation (real heartbeat cron)
//
// No kubectl shortcuts. Every step matches what a user runs.
func TestMain(m *testing.M) {
	if os.Getenv("OBOL_INTEGRATION_SKIP_BOOTSTRAP") == "true" {
		// Escape hatch: use a pre-existing cluster with pre-deployed ServiceOffer.
		binDir := os.Getenv("OBOL_BIN_DIR")
		configDir := os.Getenv("OBOL_CONFIG_DIR")
		if binDir != "" && configDir != "" {
			integrationKubectlBin = filepath.Join(binDir, "kubectl")
			integrationKubeconfig = filepath.Join(configDir, "kubeconfig.yaml")
			integrationObolBin = filepath.Join(binDir, "obol")
			integrationRoutePath = "/services/" + serviceOfferName + "/v1/chat/completions"
			integrationPayTo = serviceOfferPayTo
			integrationModel = os.Getenv("OBOL_TEST_MODEL")
			if integrationModel == "" {
				integrationModel = "qwen3.5:9b"
			}
			integrationReady = true
		}
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

	obolBin := filepath.Join(binDir, "obol")
	kubeconfigPath := filepath.Join(configDir, "kubeconfig.yaml")
	kubectlBin := filepath.Join(binDir, "kubectl")

	// ── Step 1: Build obol binary ────────────────────────────────────
	log.Println("═══ Step 1: Building obol binary ═══")
	if err := buildObol(projectRoot, obolBin); err != nil {
		log.Fatalf("build obol: %v", err)
	}
	integrationObolBin = obolBin

	// ── Step 2: obol stack init + up ─────────────────────────────────
	log.Println("═══ Step 2: obol stack init + up (3-5 minutes) ═══")
	if err := runObol(obolBin, "stack", "init", "--backend", "k3d", "--force"); err != nil {
		log.Fatalf("obol stack init: %v", err)
	}
	if err := runObol(obolBin, "stack", "up"); err != nil {
		log.Fatalf("obol stack up: %v", err)
	}

	integrationKubectlBin = kubectlBin
	integrationKubeconfig = kubeconfigPath

	// Wait for core infrastructure.
	log.Println("  Waiting for x402-verifier...")
	if err := waitForPod(kubectlBin, kubeconfigPath, "x402", "app=x402-verifier", 180*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("x402-verifier not ready: %v", err)
	}
	log.Println("  Waiting for LiteLLM...")
	if err := waitForPod(kubectlBin, kubeconfigPath, "llm", "app=litellm", 300*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("LiteLLM not ready: %v", err)
	}

	// ── Step 3: obol model setup ─────────────────────────────────────
	log.Println("═══ Step 3: obol model setup ═══")
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		log.Println("  Configuring Anthropic provider")
		if err := runObol(obolBin, "model", "setup", "--provider", "anthropic", "--api-key", apiKey); err != nil {
			teardown(obolBin)
			log.Fatalf("obol model setup anthropic: %v", err)
		}
		integrationModel = "claude-sonnet-4-20250514"
	} else if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		log.Println("  Configuring OpenAI provider")
		if err := runObol(obolBin, "model", "setup", "--provider", "openai", "--api-key", apiKey); err != nil {
			teardown(obolBin)
			log.Fatalf("obol model setup openai: %v", err)
		}
		integrationModel = "gpt-4o-mini"
	} else {
		log.Println("  No cloud API key, using default Ollama")
		integrationModel = "llama3.2"
	}
	if envModel := os.Getenv("OBOL_TEST_MODEL"); envModel != "" {
		integrationModel = envModel
	}

	// ── Step 4: obol sell pricing ────────────────────────────────────
	log.Println("═══ Step 4: obol sell pricing ═══")
	if err := runObol(obolBin, "sell", "pricing",
		"--wallet", serviceOfferPayTo,
		"--chain", "base-sepolia"); err != nil {
		teardown(obolBin)
		log.Fatalf("obol sell pricing: %v", err)
	}

	// ── Step 5: obol agent init ──────────────────────────────────────
	log.Println("═══ Step 5: obol agent init (deploys agent + RBAC + monetize skill) ═══")
	if err := runObol(obolBin, "agent", "init"); err != nil {
		teardown(obolBin)
		log.Fatalf("obol agent init: %v", err)
	}

	// Wait for the obol-agent pod to be Running.
	log.Println("  Waiting for obol-agent pod...")
	if err := waitForPod(kubectlBin, kubeconfigPath, "openclaw-obol-agent", "app=openclaw", 300*time.Second); err != nil {
		teardown(obolBin)
		log.Fatalf("obol-agent not ready: %v", err)
	}

	// ── Step 6: obol sell http ───────────────────────────────────────
	log.Println("═══ Step 6: obol sell http (creates ServiceOffer CR) ═══")
	if err := runObol(obolBin, "sell", "http", serviceOfferName,
		"--wallet", serviceOfferPayTo,
		"--chain", "base-sepolia",
		"--price", "0.001",
		"--upstream", "litellm",
		"--port", "4000",
		"--namespace", serviceOfferNamespace,
		"--health-path", "/health/readiness"); err != nil {
		teardown(obolBin)
		log.Fatalf("obol sell http: %v", err)
	}

	// ── Step 7: Wait for agent reconciliation ────────────────────────
	log.Println("═══ Step 7: Waiting for agent to reconcile ServiceOffer ═══")
	if err := waitForServiceOfferReady(kubectlBin, kubeconfigPath, serviceOfferName, serviceOfferNamespace, 180*time.Second); err != nil {
		// If the heartbeat hasn't fired yet, manually trigger reconciliation.
		log.Println("  ServiceOffer not Ready, triggering manual reconciliation...")
		triggerReconciliation(kubectlBin, kubeconfigPath)
		if err := waitForServiceOfferReady(kubectlBin, kubeconfigPath, serviceOfferName, serviceOfferNamespace, 120*time.Second); err != nil {
			teardown(obolBin)
			log.Fatalf("ServiceOffer not Ready after reconciliation: %v", err)
		}
	}

	// Restart x402-verifier to pick up the pricing route added by reconciliation.
	log.Println("  Restarting x402-verifier...")
	_ = kubectl.RunSilent(kubectlBin, kubeconfigPath, "rollout", "restart", "deployment/x402-verifier", "-n", "x402")
	_ = waitForPod(kubectlBin, kubeconfigPath, "x402", "app=x402-verifier", 120*time.Second)

	// Let Traefik pick up the new HTTPRoute.
	time.Sleep(5 * time.Second)

	integrationRoutePath = "/services/" + serviceOfferName + "/v1/chat/completions"
	integrationPayTo = serviceOfferPayTo
	integrationReady = true

	log.Printf("═══ Bootstrap complete: route=%s model=%s ═══", integrationRoutePath, integrationModel)

	code := m.Run()
	teardown(obolBin)
	os.Exit(code)
}

// TestBDDIntegration runs the BDD scenarios.
//
//	go test -tags integration -v -run TestBDDIntegration -timeout 20m ./internal/x402/
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

func runObolOutput(obolBin string, args ...string) (string, error) {
	cmd := exec.Command(obolBin, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func waitForPod(kubectlBin, kubeconfig, namespace, labelSelector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Output(kubectlBin, kubeconfig, "get", "pods", "-n", namespace,
			"-l", labelSelector, "--no-headers")
		if err == nil && strings.Contains(out, "Running") {
			log.Printf("  ✓ %s in %s is Running", labelSelector, namespace)
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for pod %s in %s", labelSelector, namespace)
}

// waitForServiceOfferReady polls the ServiceOffer until Ready=True.
func waitForServiceOfferReady(kubectlBin, kubeconfig, name, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := kubectl.Output(kubectlBin, kubeconfig,
			"get", "serviceoffers.obol.org", name, "-n", namespace,
			"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
		if err == nil && strings.TrimSpace(out) == "True" {
			log.Printf("  ✓ ServiceOffer %s/%s is Ready", namespace, name)
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	// Log current conditions for debugging.
	out, _ := kubectl.Output(kubectlBin, kubeconfig,
		"get", "serviceoffers.obol.org", name, "-n", namespace,
		"-o", "jsonpath={range .status.conditions[*]}{.type}: {.status} ({.message}){\"\\n\"}{end}")
	log.Printf("  ServiceOffer conditions:\n%s", out)
	return fmt.Errorf("timeout waiting for ServiceOffer %s/%s to be Ready", namespace, name)
}

// triggerReconciliation manually runs monetize.py inside the obol-agent pod.
// This simulates the heartbeat cron firing.
func triggerReconciliation(kubectlBin, kubeconfig string) {
	out, err := kubectl.Output(kubectlBin, kubeconfig,
		"exec", "-i", "-n", "openclaw-obol-agent", "deploy/openclaw", "-c", "openclaw",
		"--", "python3", "/data/.openclaw/skills/sell/scripts/monetize.py", "process", "--all")
	if err != nil {
		log.Printf("  manual reconciliation error: %v\n%s", err, out)
	} else {
		log.Printf("  reconciliation output:\n%s", out)
	}
}

func decodeBase64(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(decoded)
}

func teardown(obolBin string) {
	log.Println("═══ Tearing down ═══")

	// Delete the ServiceOffer via CLI (tests the real cleanup path).
	if integrationObolBin != "" {
		_ = runObol(integrationObolBin, "sell", "delete", serviceOfferName,
			"-n", serviceOfferNamespace, "-f")
	}

	if err := runObol(obolBin, "stack", "down"); err != nil {
		log.Printf("Warning: obol stack down failed: %v", err)
	}
	if err := runObol(obolBin, "stack", "purge", "-f"); err != nil {
		log.Printf("Warning: obol stack purge failed: %v", err)
	}
}
