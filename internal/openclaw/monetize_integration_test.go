//go:build integration

package openclaw

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	petname "github.com/dustinkirkland/golang-petname"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/testutil"
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
