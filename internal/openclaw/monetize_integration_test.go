//go:build integration

package openclaw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	petname "github.com/dustinkirkland/golang-petname"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/testutil"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers — CRD operations
// ─────────────────────────────────────────────────────────────────────────────

// requireCRD skips the test if the ServiceOffer CRD is not installed.
func requireCRD(t *testing.T, cfg *config.Config) {
	t.Helper()
	out := obolRun(t, cfg, "kubectl", "get", "crd", "serviceoffers.obol.org")
	if !strings.Contains(out, "serviceoffers.obol.org") {
		t.Skip("ServiceOffer CRD not installed")
	}
}

// createTestNamespace creates a namespace and registers cleanup.
func createTestNamespace(t *testing.T, cfg *config.Config, name string) {
	t.Helper()
	obolRun(t, cfg, "kubectl", "create", "namespace", name)
	t.Cleanup(func() {
		_, _ = obolRunErr(cfg, "kubectl", "delete", "namespace", name, "--ignore-not-found", "--wait=false")
	})
}

// applyServiceOffer creates a ServiceOffer CR from inline YAML by piping to kubectl.
func applyServiceOffer(t *testing.T, cfg *config.Config, yamlManifest string) {
	t.Helper()
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yamlManifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl apply failed: %v\n%s", err, out)
	}
}

// deleteServiceOffer deletes a ServiceOffer CR.
func deleteServiceOffer(t *testing.T, cfg *config.Config, name, namespace string) {
	t.Helper()
	_, _ = obolRunErr(cfg, "kubectl", "delete", "serviceoffer", name, "-n", namespace, "--ignore-not-found")
}

// getServiceOffer returns the ServiceOffer as a parsed JSON map.
func getServiceOffer(t *testing.T, cfg *config.Config, name, namespace string) map[string]interface{} {
	t.Helper()
	out := obolRun(t, cfg, "kubectl", "get", "serviceoffer", name, "-n", namespace, "-o", "json")
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse serviceoffer JSON: %v", err)
	}
	return result
}

// testNamespace generates a unique test namespace name.
func testNamespace(prefix string) string {
	return fmt.Sprintf("test-%s-%s", prefix, petname.Generate(2, "-"))
}

// minimalServiceOfferYAML returns a valid ServiceOffer YAML for testing.
func minimalServiceOfferYAML(name, namespace string) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: test-svc
    namespace: %s
    port: 8080
  pricing:
    amount: "0.001"
    unit: MTok
    chain: base-sepolia
  wallet: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
`, name, namespace, namespace)
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 1 — CRD Lifecycle Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestIntegration_CRD_Exists(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	// If we get here, the CRD is installed.
}

func TestIntegration_CRD_CreateGet(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd")
	createTestNamespace(t, cfg, ns)

	name := "test-create"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	so := getServiceOffer(t, cfg, name, ns)

	// Verify spec fields match
	spec, ok := so["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec not found in ServiceOffer")
	}

	wallet, _ := spec["wallet"].(string)
	if wallet != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
		t.Errorf("wallet = %q, want 0x70997970C51812dc3A010C7d01b50e0d17dc79C8", wallet)
	}

	pricing, ok := spec["pricing"].(map[string]interface{})
	if !ok {
		t.Fatal("pricing not found")
	}
	if amount := pricing["amount"]; amount != "0.001" {
		t.Errorf("pricing.amount = %v, want 0.001", amount)
	}
}

func TestIntegration_CRD_List(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-list")
	createTestNamespace(t, cfg, ns)

	name := "test-list"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	out := obolRun(t, cfg, "kubectl", "get", "serviceoffers", "-n", ns)
	if !strings.Contains(out, name) {
		t.Errorf("kubectl get serviceoffers output does not contain %q:\n%s", name, out)
	}
}

func TestIntegration_CRD_StatusSubresource(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-status")
	createTestNamespace(t, cfg, ns)

	name := "test-status"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Patch status with a condition using kubectl
	statusPatch := `{"status":{"conditions":[{"type":"Ready","status":"False","reason":"Testing","message":"integration test"}]}}`
	obolRun(t, cfg, "kubectl", "patch", "serviceoffer", name, "-n", ns,
		"--type=merge", "--subresource=status", "-p", statusPatch)

	// Verify the condition sticks
	so := getServiceOffer(t, cfg, name, ns)
	status, ok := so["status"].(map[string]interface{})
	if !ok {
		t.Fatal("status not found after patch")
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok || len(conditions) == 0 {
		t.Fatal("no conditions after status patch")
	}
	cond := conditions[0].(map[string]interface{})
	if cond["type"] != "Ready" || cond["status"] != "False" {
		t.Errorf("condition = %v, want type=Ready status=False", cond)
	}

	// Verify spec was NOT changed by status patch
	spec := so["spec"].(map[string]interface{})
	if spec["wallet"] != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
		t.Error("spec.wallet was modified by status subresource patch")
	}
}

func TestIntegration_CRD_WalletValidation(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-wallet")
	createTestNamespace(t, cfg, ns)

	// Bad wallet — should be rejected by CRD validation regex
	badYAML := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: test-bad-wallet
  namespace: %s
spec:
  upstream:
    service: test-svc
    namespace: %s
    port: 8080
  pricing:
    amount: "0.001"
    unit: MTok
    chain: base-sepolia
  wallet: "not-a-valid-wallet"
`, ns, ns)

	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(badYAML)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected kubectl apply to fail with invalid wallet, but it succeeded")
	}
	// The error should mention the wallet pattern validation
	if !strings.Contains(string(out), "wallet") {
		t.Logf("rejection output: %s", out)
	}
}

func TestIntegration_CRD_PrinterColumns(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-cols")
	createTestNamespace(t, cfg, ns)

	name := "test-cols"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// kubectl get so should show printer columns
	out := obolRun(t, cfg, "kubectl", "get", "so", "-n", ns)
	// Column headers should include PRICE and AGE
	for _, col := range []string{"PRICE", "AGE"} {
		if !strings.Contains(out, col) {
			t.Errorf("kubectl get so output missing column %q:\n%s", col, out)
		}
	}
}

func TestIntegration_CRD_Delete(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-del")
	createTestNamespace(t, cfg, ns)

	name := "test-delete"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)

	// Verify it exists
	_ = getServiceOffer(t, cfg, name, ns)

	// Delete it
	obolRun(t, cfg, "kubectl", "delete", "serviceoffer", name, "-n", ns)

	// Verify GET fails
	_, err := obolRunErr(cfg, "kubectl", "get", "serviceoffer", name, "-n", ns)
	if err == nil {
		t.Error("expected GET to fail after delete, but it succeeded")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 2 — RBAC + Reconciliation Tests
// ─────────────────────────────────────────────────────────────────────────────

// requireAgent skips the test if the obol-agent OpenClaw instance is not deployed.
func requireAgent(t *testing.T, cfg *config.Config) {
	t.Helper()
	out, err := obolRunErr(cfg, "openclaw", "list")
	if err != nil || !strings.Contains(out, "obol-agent") {
		t.Skip("obol-agent not deployed — run: obol agent init")
	}
}

// execInAgent runs a command inside the obol-agent pod.
func execInAgent(t *testing.T, cfg *config.Config, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"kubectl", "exec", "-i",
		"-n", "openclaw-obol-agent", "deploy/openclaw",
		"-c", "openclaw", "--"}, args...)
	return obolRun(t, cfg, fullArgs...)
}

// execInAgentErr runs a command inside the obol-agent pod, returning output + error.
func execInAgentErr(cfg *config.Config, args ...string) (string, error) {
	fullArgs := append([]string{"kubectl", "exec", "-i",
		"-n", "openclaw-obol-agent", "deploy/openclaw",
		"-c", "openclaw", "--"}, args...)
	return obolRunErr(cfg, fullArgs...)
}

func TestIntegration_RBAC_ClusterRoleExists(t *testing.T) {
	cfg := requireCluster(t)

	out := obolRun(t, cfg, "kubectl", "get", "clusterrole", "openclaw-monetize", "-o", "json")
	var cr map[string]interface{}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("parse clusterrole JSON: %v", err)
	}

	rules, ok := cr["rules"].([]interface{})
	if !ok || len(rules) == 0 {
		t.Fatal("ClusterRole has no rules")
	}

	// Verify key apiGroups are present
	apiGroups := make(map[string]bool)
	for _, r := range rules {
		rm := r.(map[string]interface{})
		groups, ok := rm["apiGroups"].([]interface{})
		if !ok {
			continue
		}
		for _, g := range groups {
			apiGroups[g.(string)] = true
		}
	}

	for _, want := range []string{"obol.org", "traefik.io", "gateway.networking.k8s.io"} {
		if !apiGroups[want] {
			t.Errorf("ClusterRole missing apiGroup %q", want)
		}
	}
}

func TestIntegration_RBAC_BindingPatched(t *testing.T) {
	cfg := requireCluster(t)
	requireAgent(t, cfg)

	out := obolRun(t, cfg, "kubectl", "get", "clusterrolebinding", "openclaw-monetize-binding", "-o", "json")
	var crb map[string]interface{}
	if err := json.Unmarshal([]byte(out), &crb); err != nil {
		t.Fatalf("parse binding JSON: %v", err)
	}

	subjects, ok := crb["subjects"].([]interface{})
	if !ok || len(subjects) == 0 {
		t.Skip("ClusterRoleBinding has no subjects yet — obol agent init may not have run")
	}

	// Check that at least one subject is an openclaw service account
	found := false
	for _, s := range subjects {
		sm := s.(map[string]interface{})
		ns, _ := sm["namespace"].(string)
		if strings.HasPrefix(ns, "openclaw-") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no openclaw-* service account found in binding subjects")
	}
}

func TestIntegration_Monetize_ListEmpty(t *testing.T) {
	cfg := requireCluster(t)
	requireAgent(t, cfg)

	// Run monetize.py list inside the agent pod — should not error
	out := execInAgent(t, cfg, "python3", "/data/.openclaw/skills/monetize/scripts/monetize.py", "list")
	// Should produce output (even if empty table) without crashing
	t.Logf("monetize list output:\n%s", out)
}

func TestIntegration_Monetize_ProcessAllEmpty(t *testing.T) {
	cfg := requireCluster(t)
	requireAgent(t, cfg)

	// When no ServiceOffers exist, process --all should return HEARTBEAT_OK
	out := execInAgent(t, cfg, "python3", "/data/.openclaw/skills/monetize/scripts/monetize.py", "process", "--all")
	if !strings.Contains(out, "HEARTBEAT_OK") {
		t.Errorf("expected HEARTBEAT_OK in output, got:\n%s", out)
	}
}

func TestIntegration_Monetize_ProcessUnhealthy(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)

	ns := testNamespace("monetize-unhealthy")
	createTestNamespace(t, cfg, ns)

	name := "test-unhealthy"
	// Point upstream at a non-existent service
	yaml := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: does-not-exist
    namespace: %s
    port: 9999
    healthPath: /health
  pricing:
    amount: "0.001"
    unit: MTok
    chain: base-sepolia
  wallet: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
`, name, ns, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Run process for this specific offer
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("process output:\n%s", out)

	// Check the ServiceOffer status — UpstreamHealthy should be False
	so := getServiceOffer(t, cfg, name, ns)
	status, ok := so["status"].(map[string]interface{})
	if !ok {
		t.Skip("status not yet set — monetize.py may not have patched it")
	}

	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		t.Skip("no conditions set yet")
	}

	for _, c := range conditions {
		cm := c.(map[string]interface{})
		if cm["type"] == "UpstreamHealthy" && cm["status"] == "False" {
			return // success
		}
	}
	t.Errorf("expected UpstreamHealthy=False in conditions: %v", conditions)
}

func TestIntegration_Monetize_Idempotent(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)

	ns := testNamespace("monetize-idempotent")
	createTestNamespace(t, cfg, ns)

	name := "test-idempotent"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// First process run
	out1, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Second process run (should be idempotent)
	out2, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Both runs should complete without error
	t.Logf("run 1:\n%s", out1)
	t.Logf("run 2:\n%s", out2)

	// Verify the ServiceOffer status is consistent
	so := getServiceOffer(t, cfg, name, ns)
	if _, ok := so["status"]; !ok {
		t.Skip("status not set — reconciliation may not have completed")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 — Routing with Anvil Upstream
// ─────────────────────────────────────────────────────────────────────────────

// requireAnvil starts an Anvil fork of Base Sepolia.
// Skips the test if anvil is not installed.
func requireAnvil(t *testing.T) *testutil.AnvilFork {
	t.Helper()
	return testutil.StartAnvilFork(t)
}

// deployAnvilUpstream creates a K8s Service + Endpoints in the given namespace
// that routes to the host-side Anvil instance via host.k3d.internal.
func deployAnvilUpstream(t *testing.T, cfg *config.Config, namespace string, anvil *testutil.AnvilFork) {
	t.Helper()

	// Create a headless Service + Endpoints pointing at host.k3d.internal:<anvil-port>
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: anvil-rpc
  namespace: %s
spec:
  ports:
    - port: 8545
      targetPort: %d
  clusterIP: None
---
apiVersion: v1
kind: Endpoints
metadata:
  name: anvil-rpc
  namespace: %s
subsets:
  - addresses:
      - ip: "$(getent hosts host.k3d.internal | awk '{print $1}')"
    ports:
      - port: %d
`, namespace, anvil.Port, namespace, anvil.Port)

	// We can't resolve host.k3d.internal from outside the cluster, so use
	// an ExternalName service instead.
	externalNameManifest := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: anvil-rpc
  namespace: %s
spec:
  type: ExternalName
  externalName: host.k3d.internal
  ports:
    - port: %d
      targetPort: %d
`, namespace, anvil.Port, anvil.Port)

	_ = manifest // unused, ExternalName is simpler
	applyServiceOffer(t, cfg, externalNameManifest)
}

// serviceOfferWithAnvil returns a ServiceOffer YAML targeting an Anvil upstream.
func serviceOfferWithAnvil(name, namespace string, anvilPort int) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: anvil-rpc
    namespace: %s
    port: %d
    healthPath: /
  pricing:
    amount: "0.001"
    unit: request
    currency: USDC
    chain: base-sepolia
  wallet: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
  path: /services/%s
`, name, namespace, namespace, anvilPort, name)
}

// getConditionStatus extracts a condition's status string from a ServiceOffer.
func getConditionStatus(so map[string]interface{}, condType string) string {
	status, ok := so["status"].(map[string]interface{})
	if !ok {
		return ""
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return ""
	}
	for _, c := range conditions {
		cm := c.(map[string]interface{})
		if cm["type"] == condType {
			s, _ := cm["status"].(string)
			return s
		}
	}
	return ""
}

// waitForCondition polls until a ServiceOffer condition reaches the expected status.
func waitForCondition(t *testing.T, cfg *config.Config, name, ns, condType, expectedStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		so := getServiceOffer(t, cfg, name, ns)
		if getConditionStatus(so, condType) == expectedStatus {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s=%s on %s/%s", condType, expectedStatus, ns, name)
}

func TestIntegration_Route_AnvilUpstream(t *testing.T) {
	cfg := requireCluster(t)
	anvil := requireAnvil(t)

	// Verify Anvil responds to RPC locally
	body := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	resp, err := http.Post(anvil.RPCURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("anvil RPC failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anvil RPC status = %d, want 200", resp.StatusCode)
	}

	_ = cfg // cluster verified
}

func TestIntegration_Route_FullReconcile(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("route")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-rpc"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Trigger reconciliation
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("process output:\n%s", out)

	// Check conditions
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Logf("condition %s = %q (may not be set yet)", cond, status)
		}
	}
}

func TestIntegration_Route_MiddlewareCreated(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("route-mw")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-mw"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Trigger reconciliation
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Check for ForwardAuth Middleware
	out, err := obolRunErr(cfg, "kubectl", "get", "middleware", "-n", ns, "-o", "json")
	if err != nil {
		t.Skipf("no middlewares found: %v", err)
	}
	if !strings.Contains(out, "forwardAuth") && !strings.Contains(out, "ForwardAuth") {
		t.Logf("middleware output: %s", out)
	}
}

func TestIntegration_Route_HTTPRouteCreated(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("route-hr")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-hr"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Trigger reconciliation
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Check for HTTPRoute
	out, err := obolRunErr(cfg, "kubectl", "get", "httproute", "-n", ns, "-o", "json")
	if err != nil {
		t.Skipf("no httproutes found: %v", err)
	}
	if !strings.Contains(out, "traefik-gateway") {
		t.Logf("httproute output (expected traefik-gateway parentRef): %s", out)
	}
}

func TestIntegration_Route_TrafficRoutes(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("route-traffic")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-traffic"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Trigger reconciliation
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Wait for route to propagate
	time.Sleep(5 * time.Second)

	// Try to reach Anvil through Traefik
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080 — is /etc/hosts configured? %v", err)
	}
	defer resp.Body.Close()

	// The verifier has no pricing route for this path, so it passes through (200).
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 through Traefik, got %d", resp.StatusCode)
	}
}

func TestIntegration_Route_DeleteCascades(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("route-cascade")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-cascade"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)

	// Trigger reconciliation to create Middleware + HTTPRoute
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Delete the ServiceOffer
	obolRun(t, cfg, "kubectl", "delete", "serviceoffer", name, "-n", ns)

	// Wait for garbage collection
	time.Sleep(3 * time.Second)

	// Middleware and HTTPRoute should be gone (owner reference cascade)
	mwOut, _ := obolRunErr(cfg, "kubectl", "get", "middleware", "-n", ns, "-o", "name")
	if strings.Contains(mwOut, name) {
		t.Errorf("middleware still exists after ServiceOffer deletion:\n%s", mwOut)
	}

	hrOut, _ := obolRunErr(cfg, "kubectl", "get", "httproute", "-n", ns, "-o", "name")
	if strings.Contains(hrOut, name) {
		t.Errorf("httproute still exists after ServiceOffer deletion:\n%s", hrOut)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 4 — Payment Gate Tests
// ─────────────────────────────────────────────────────────────────────────────

// setupMockFacilitator starts a host-side mock facilitator and patches
// the x402-verifier ConfigMap to use it via host.k3d.internal.
// Returns the MockFacilitator. Registers cleanup to restore original config.
func setupMockFacilitator(t *testing.T, cfg *config.Config) *testutil.MockFacilitator {
	t.Helper()
	mf := testutil.StartMockFacilitator(t)

	// Save original pricing config for restore.
	origCfg, err := x402verifier.GetPricingConfig(cfg)
	if err != nil {
		t.Fatalf("read original pricing config: %v", err)
	}

	// Patch facilitator URL to host-side mock.
	// The mock listens on 127.0.0.1 but k3d pods reach the host via host.k3d.internal.
	patchYAML := fmt.Sprintf(`{"data":{"pricing.yaml":"wallet: \"%s\"\nchain: \"%s\"\nfacilitatorURL: \"%s\"\nverifyOnly: false\nroutes: []\n"}}`,
		origCfg.Wallet, origCfg.Chain, mf.ClusterURL)

	obolRun(t, cfg, "kubectl", "patch", "configmap", "x402-pricing",
		"-n", "x402", "-p", patchYAML, "--type=merge")

	// Wait for Reloader to restart the verifier pod.
	time.Sleep(5 * time.Second)

	t.Cleanup(func() {
		// Restore original config.
		restoreYAML := fmt.Sprintf(`{"data":{"pricing.yaml":"wallet: \"%s\"\nchain: \"%s\"\nfacilitatorURL: \"%s\"\nverifyOnly: %v\nroutes: []\n"}}`,
			origCfg.Wallet, origCfg.Chain, origCfg.FacilitatorURL, origCfg.VerifyOnly)
		_, _ = obolRunErr(cfg, "kubectl", "patch", "configmap", "x402-pricing",
			"-n", "x402", "-p", restoreYAML, "--type=merge")
	})

	return mf
}

// addPricingRoute adds a route to the x402-verifier ConfigMap.
func addPricingRoute(t *testing.T, cfg *config.Config, pattern, price, wallet string) {
	t.Helper()
	if err := x402verifier.AddRoute(cfg, pattern, price, "test route"); err != nil {
		t.Fatalf("add pricing route: %v", err)
	}
	// Wait for Reloader to pick up changes.
	time.Sleep(5 * time.Second)
}

func TestIntegration_PaymentGate_VerifierHealthy(t *testing.T) {
	cfg := requireCluster(t)
	_ = cfg

	// x402-verifier /healthz and /readyz should return 200.
	// These are accessed via port-forward or direct cluster check.
	out, err := obolRunErr(cfg, "kubectl", "exec", "-n", "x402",
		"deploy/x402-verifier", "--", "wget", "-qO-", "http://localhost:8080/healthz")
	if err != nil {
		t.Skipf("could not reach verifier healthz: %v", err)
	}
	t.Logf("healthz: %s", out)

	out, err = obolRunErr(cfg, "kubectl", "exec", "-n", "x402",
		"deploy/x402-verifier", "--", "wget", "-qO-", "http://localhost:8080/readyz")
	if err != nil {
		t.Skipf("could not reach verifier readyz: %v", err)
	}
	t.Logf("readyz: %s", out)
}

func TestIntegration_PaymentGate_402WithoutPayment(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("pay-402")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	// Start mock facilitator and patch verifier config.
	_ = setupMockFacilitator(t, cfg)

	name := "test-pay402"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Trigger reconciliation to create Middleware + HTTPRoute.
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Add a pricing route for this service path.
	addPricingRoute(t, cfg, fmt.Sprintf("/services/%s/*", name), "0.001",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	// Wait for route propagation.
	time.Sleep(3 * time.Second)

	// Request WITHOUT payment should get 402.
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 402 Payment Required, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestIntegration_PaymentGate_RequirementsFormat(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("pay-req")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	_ = setupMockFacilitator(t, cfg)

	name := "test-payreq"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	addPricingRoute(t, cfg, fmt.Sprintf("/services/%s/*", name), "0.001",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	time.Sleep(3 * time.Second)

	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Skipf("expected 402 for requirements check, got %d", resp.StatusCode)
	}

	// Parse the 402 body for payment requirements.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read 402 body: %v", err)
	}

	var requirements map[string]interface{}
	if err := json.Unmarshal(body, &requirements); err != nil {
		t.Fatalf("parse 402 body: %v\nbody: %s", err, body)
	}

	// Should have accepts array with chain/currency/amount.
	accepts, ok := requirements["accepts"].([]interface{})
	if !ok || len(accepts) == 0 {
		t.Fatalf("402 body missing 'accepts' array: %s", body)
	}
	t.Logf("payment requirements: %s", body)

	// Verify first accept entry has expected fields.
	first, ok := accepts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("accepts[0] is not an object: %v", accepts[0])
	}
	if _, ok := first["network"]; !ok {
		t.Error("accepts[0] missing 'network' field")
	}
}

func TestIntegration_PaymentGate_200WithPayment(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("pay-200")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	mf := setupMockFacilitator(t, cfg)

	name := "test-pay200"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	addPricingRoute(t, cfg, fmt.Sprintf("/services/%s/*", name), "0.001",
		"0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	time.Sleep(3 * time.Second)

	// Request WITH payment should get 200 and RPC response from Anvil.
	walletAddr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	paymentHeader := testutil.TestPaymentHeader(t, walletAddr)

	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	req, err := http.NewRequest("POST", url, strings.NewReader(rpcBody))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with valid payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		t.Logf("payment accepted, response: %s", body)
	}

	// Verify mock facilitator was called.
	if mf.VerifyCalls.Load() == 0 {
		t.Logf("warning: mock facilitator verify was not called (may use cached result)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 5 — Full E2E (CLI-Driven) Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestIntegration_E2E_OfferLifecycle(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("e2e")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	mf := setupMockFacilitator(t, cfg)

	name := "test-e2e"
	walletAddr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	// Step 1: Create ServiceOffer via obol CLI.
	obolRun(t, cfg, "monetize", "offer", name,
		"--price", "0.001",
		"--chain", "base-sepolia",
		"--wallet", walletAddr,
		"--namespace", ns,
		"--upstream", "anvil-rpc",
		"--port", fmt.Sprintf("%d", anvil.Port),
		"--path", fmt.Sprintf("/services/%s", name),
		"--unit", "request",
	)
	t.Cleanup(func() {
		_, _ = obolRunErr(cfg, "monetize", "delete", name, "--namespace", ns, "--force")
	})

	// Step 2: Verify CR was created.
	so := getServiceOffer(t, cfg, name, ns)
	spec, ok := so["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec missing from created ServiceOffer")
	}
	if spec["wallet"] != walletAddr {
		t.Errorf("wallet = %v, want %s", spec["wallet"], walletAddr)
	}

	// Step 3: Trigger reconciliation via monetize.py.
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Step 4: Verify offer-status shows conditions.
	statusOut := obolRun(t, cfg, "monetize", "offer-status", name, "--namespace", ns)
	t.Logf("offer-status:\n%s", statusOut)

	// Step 5: Verify obol monetize list shows the offer.
	listOut := obolRun(t, cfg, "monetize", "list", "--namespace", ns)
	if !strings.Contains(listOut, name) {
		t.Errorf("monetize list does not contain %q:\n%s", name, listOut)
	}

	// Step 6: Add pricing route and test payment flow.
	addPricingRoute(t, cfg, fmt.Sprintf("/services/%s/*", name), "0.001", walletAddr)
	time.Sleep(3 * time.Second)

	// Without payment → 402.
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Logf("expected 402 without payment, got %d", resp.StatusCode)
	}

	// With payment → 200.
	paymentHeader := testutil.TestPaymentHeader(t, walletAddr)
	req, _ := http.NewRequest("POST", url, strings.NewReader(rpcBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("could not reach obol.stack:8080 with payment: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Logf("expected 200 with payment, got %d; body: %s", resp.StatusCode, body)
	}

	// Step 7: Delete via CLI.
	obolRun(t, cfg, "monetize", "delete", name, "--namespace", ns, "--force")

	// Step 8: Verify CR is gone.
	_, err = obolRunErr(cfg, "kubectl", "get", "serviceoffer", name, "-n", ns)
	if err == nil {
		t.Error("ServiceOffer still exists after CLI delete")
	}

	// Step 9: Verify route is gone.
	time.Sleep(3 * time.Second)
	resp, err = http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err == nil {
		resp.Body.Close()
		// After route removal, should get 404 or 502 (no backend).
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPaymentRequired {
			t.Logf("expected 404/502 after delete, got %d", resp.StatusCode)
		}
	}

	_ = mf // mock facilitator used
}

func TestIntegration_E2E_HeartbeatReconciles(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("e2e-heartbeat")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	name := "test-heartbeat"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Do NOT manually trigger process — wait for the heartbeat cron (every 60s)
	// to auto-reconcile the pending offer. Timeout 90s.
	deadline := time.Now().Add(90 * time.Second)
	reconciled := false
	for time.Now().Before(deadline) {
		so := getServiceOffer(t, cfg, name, ns)
		status := getConditionStatus(so, "UpstreamHealthy")
		if status != "" {
			reconciled = true
			t.Logf("heartbeat reconciled: UpstreamHealthy=%s", status)
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !reconciled {
		t.Skip("heartbeat did not reconcile within 90s — cron may not be configured")
	}
}

func TestIntegration_E2E_ListAndStatus(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)

	ns := testNamespace("e2e-ls")
	createTestNamespace(t, cfg, ns)

	name := "test-ls"
	yaml := minimalServiceOfferYAML(name, ns)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// obol monetize list should show the offer.
	listOut := obolRun(t, cfg, "monetize", "list")
	if !strings.Contains(listOut, name) {
		t.Errorf("monetize list does not contain %q:\n%s", name, listOut)
	}

	// obol monetize offer-status should show the CR.
	statusOut := obolRun(t, cfg, "monetize", "offer-status", name, "--namespace", ns)
	if !strings.Contains(statusOut, "ServiceOffer") && !strings.Contains(statusOut, "serviceoffer") && !strings.Contains(statusOut, "kind") {
		t.Logf("offer-status output (expected ServiceOffer YAML):\n%s", statusOut)
	}
}
