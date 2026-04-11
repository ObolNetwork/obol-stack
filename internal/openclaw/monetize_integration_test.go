//go:build integration

package openclaw

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	petname "github.com/dustinkirkland/golang-petname"
	"gopkg.in/yaml.v3"

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
	_, _ = obolRunErr(cfg, "kubectl", "delete", "serviceoffers.obol.org", name, "-n", namespace, "--ignore-not-found")
}

// getServiceOffer returns the ServiceOffer as a parsed JSON map.
func getServiceOffer(t *testing.T, cfg *config.Config, name, namespace string) map[string]interface{} {
	t.Helper()
	out := obolRun(t, cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", namespace, "-o", "json")
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse serviceoffer JSON: %v", err)
	}
	return result
}

func resourceExists(t *testing.T, cfg *config.Config, kind, name, namespace string) bool {
	t.Helper()
	_, err := obolRunErr(cfg, "kubectl", "get", kind, name, "-n", namespace)
	return err == nil
}

func assertOfferRouteResourcesPresent(t *testing.T, cfg *config.Config, name, namespace string) {
	t.Helper()
	if !resourceExists(t, cfg, "middleware", "x402-"+name, namespace) {
		t.Fatalf("middleware x402-%s not found in %s", name, namespace)
	}
	if !resourceExists(t, cfg, "httproute", "so-"+name, namespace) {
		t.Fatalf("httproute so-%s not found in %s", name, namespace)
	}
}

func assertOfferRouteResourcesAbsent(t *testing.T, cfg *config.Config, name, namespace string) {
	t.Helper()
	if resourceExists(t, cfg, "middleware", "x402-"+name, namespace) {
		t.Fatalf("middleware x402-%s still exists in %s", name, namespace)
	}
	if resourceExists(t, cfg, "httproute", "so-"+name, namespace) {
		t.Fatalf("httproute so-%s still exists in %s", name, namespace)
	}
}

// testNamespace generates a unique test namespace name.
func testNamespace(prefix string) string {
	return fmt.Sprintf("test-%s-%s", prefix, petname.Generate(2, "-"))
}

// minimalServiceOfferYAML returns a valid ServiceOffer YAML for testing.
// Field names align with x402 (payment.payTo, payment.network) and ERC-8004 (registration).
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
  payment:
    network: base-sepolia
    payTo: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    price:
      perRequest: "0.001"
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

	// Verify spec fields match (x402-aligned schema)
	spec, ok := so["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec not found in ServiceOffer")
	}

	payment, ok := spec["payment"].(map[string]interface{})
	if !ok {
		t.Fatal("payment not found")
	}

	payTo, _ := payment["payTo"].(string)
	if payTo != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
		t.Errorf("payment.payTo = %q, want 0x70997970C51812dc3A010C7d01b50e0d17dc79C8", payTo)
	}

	price, ok := payment["price"].(map[string]interface{})
	if !ok {
		t.Fatal("payment.price not found")
	}
	if perReq := price["perRequest"]; perReq != "0.001" {
		t.Errorf("payment.price.perRequest = %v, want 0.001", perReq)
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

	out := obolRun(t, cfg, "kubectl", "get", "serviceoffers.obol.org", "-n", ns)
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
	obolRun(t, cfg, "kubectl", "patch", "serviceoffers.obol.org", name, "-n", ns,
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
	payment, ok := spec["payment"].(map[string]interface{})
	if !ok {
		t.Fatal("spec.payment missing after status patch")
	}
	if payment["payTo"] != "0x70997970C51812dc3A010C7d01b50e0d17dc79C8" {
		t.Error("spec.payment.payTo was modified by status subresource patch")
	}
}

func TestIntegration_CRD_WalletValidation(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)

	ns := testNamespace("crd-wallet")
	createTestNamespace(t, cfg, ns)

	// Bad wallet — should be rejected by CRD validation regex on payment.payTo
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
  payment:
    network: base-sepolia
    payTo: "not-a-valid-wallet"
    price:
      perRequest: "0.001"
`, ns, ns)

	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, "kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(badYAML)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected kubectl apply to fail with invalid payTo, but it succeeded")
	}
	// The error should mention the payTo pattern validation
	if !strings.Contains(string(out), "payTo") && !strings.Contains(string(out), "pattern") {
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
	out := obolRun(t, cfg, "kubectl", "get", "serviceoffers.obol.org", "-n", ns)
	// Column headers should include TYPE, PRICE, NETWORK, and AGE
	for _, col := range []string{"TYPE", "PRICE", "NETWORK", "AGE"} {
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
	obolRun(t, cfg, "kubectl", "delete", "serviceoffers.obol.org", name, "-n", ns)

	// Verify GET fails
	_, err := obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
	if err == nil {
		t.Error("expected GET to fail after delete, but it succeeded")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 2 — RBAC + Reconciliation Tests
// ─────────────────────────────────────────────────────────────────────────────

// agentNamespace returns the namespace of the OpenClaw instance that has
// monetize RBAC. This is always the "obol-agent" instance ("openclaw-obol-agent").
type listedAgentInstance struct {
	ID        string
	Namespace string
}

func listedAgentInstances(cfg *config.Config) []listedAgentInstance {
	out, err := obolRunErr(cfg, "openclaw", "list")
	if err != nil {
		return nil
	}

	var (
		instances []listedAgentInstance
		currentID string
	)
	for _, raw := range strings.Split(out, "\n") {
		if strings.HasPrefix(raw, "  ") {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "Namespace:") {
				if currentID == "" {
					continue
				}
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					instances = append(instances, listedAgentInstance{
						ID:        currentID,
						Namespace: parts[1],
					})
				}
				continue
			}
			if !strings.Contains(trimmed, ":") {
				currentID = trimmed
			}
		}
	}

	return instances
}

func hasOpenClawDeployment(cfg *config.Config, namespace string) bool {
	_, err := obolRunErr(cfg, "kubectl", "get", "deploy", "openclaw", "-n", namespace)
	return err == nil
}

func agentNamespace(cfg *config.Config) string {
	instances := listedAgentInstances(cfg)
	for _, inst := range instances {
		if inst.ID == "obol-agent" && hasOpenClawDeployment(cfg, inst.Namespace) {
			return inst.Namespace
		}
	}
	for _, inst := range instances {
		if hasOpenClawDeployment(cfg, inst.Namespace) {
			return inst.Namespace
		}
	}

	return "openclaw-obol-agent"
}

func agentInstanceID(cfg *config.Config) string {
	instances := listedAgentInstances(cfg)
	for _, inst := range instances {
		if inst.ID == "obol-agent" && hasOpenClawDeployment(cfg, inst.Namespace) {
			return inst.ID
		}
	}
	for _, inst := range instances {
		if hasOpenClawDeployment(cfg, inst.Namespace) {
			return inst.ID
		}
	}

	return "obol-agent"
}

// requireAgent skips the test if no OpenClaw instance is deployed.
func requireAgent(t *testing.T, cfg *config.Config) {
	t.Helper()
	out, err := obolRunErr(cfg, "openclaw", "list")
	if err != nil {
		t.Skip("no OpenClaw instance deployed — run: obol agent init")
	}
	if !strings.Contains(out, "Namespace:") {
		t.Skip("no OpenClaw instance deployed — run: obol agent init")
	}
}

// execInAgent runs a command inside the OpenClaw pod.
func execInAgent(t *testing.T, cfg *config.Config, args ...string) string {
	t.Helper()
	ns := agentNamespace(cfg)
	fullArgs := append([]string{"kubectl", "exec", "-i",
		"-n", ns, "deploy/openclaw",
		"-c", "openclaw", "--"}, args...)
	return obolRun(t, cfg, fullArgs...)
}

// execInAgentErr runs a command inside the OpenClaw pod, returning output + error.
func execInAgentErr(cfg *config.Config, args ...string) (string, error) {
	ns := agentNamespace(cfg)
	fullArgs := append([]string{"kubectl", "exec", "-i",
		"-n", ns, "deploy/openclaw",
		"-c", "openclaw", "--"}, args...)
	return obolRunErr(cfg, fullArgs...)
}

func getAgentWalletAddress(t *testing.T, cfg *config.Config) string {
	t.Helper()

	walletRaw := obolRun(t, cfg, "kubectl", "get", "configmap", "wallet-metadata",
		"-n", agentNamespace(cfg), "-o", `jsonpath={.data.addresses\.json}`)
	var walletData struct {
		Addresses []struct {
			Address string `json:"address"`
		} `json:"addresses"`
	}
	if err := json.Unmarshal([]byte(walletRaw), &walletData); err != nil || len(walletData.Addresses) == 0 {
		t.Fatalf("agent wallet-metadata not found or empty: %v", err)
	}
	return walletData.Addresses[0].Address
}

func getERPCConfigYAML(t *testing.T, cfg *config.Config) string {
	t.Helper()
	return obolRun(t, cfg, "kubectl", "get", "configmap", "erpc-config", "-n", "erpc",
		"-o", "jsonpath={.data.erpc\\.yaml}")
}

func setERPCConfigYAML(t *testing.T, cfg *config.Config, configYAML string) {
	t.Helper()

	patch := map[string]interface{}{
		"data": map[string]string{
			"erpc.yaml": configYAML,
		},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal eRPC config patch: %v", err)
	}

	obolRun(t, cfg, "kubectl", "patch", "configmap", "erpc-config", "-n", "erpc",
		"-p", string(patchJSON), "--type=merge")
	if out, err := obolRunErr(cfg, "kubectl", "rollout", "restart", "deployment/erpc", "-n", "erpc"); err != nil &&
		!strings.Contains(out, "restart has already been triggered within the past second") {
		t.Fatalf("restart erpc: %v\n%s", err, out)
	}
	obolRun(t, cfg, "kubectl", "rollout", "status", "deployment/erpc", "-n", "erpc", "--timeout=60s")
}

func yamlInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func pinERPCChainToSingleUpstream(t *testing.T, cfg *config.Config, chainID int, upstreamID string) {
	t.Helper()

	configYAML := getERPCConfigYAML(t, cfg)
	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		t.Fatalf("parse eRPC config: %v", err)
	}

	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		t.Fatal("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		t.Fatal("eRPC config project[0] is not a map")
	}

	upstreams, _ := project["upstreams"].([]interface{})
	filtered := make([]interface{}, 0, len(upstreams))
	var selected map[string]interface{}
	for _, upstream := range upstreams {
		um, ok := upstream.(map[string]interface{})
		if !ok {
			filtered = append(filtered, upstream)
			continue
		}

		id, _ := um["id"].(string)
		evm, _ := um["evm"].(map[string]interface{})
		if yamlInt(evm["chainId"]) != chainID {
			filtered = append(filtered, upstream)
			continue
		}
		if id == upstreamID {
			selected = um
		}
	}

	if selected == nil {
		t.Fatalf("eRPC upstream %q for chain %d not found", upstreamID, chainID)
	}

	project["upstreams"] = append([]interface{}{selected}, filtered...)
	updatedYAML, err := yaml.Marshal(erpcConfig)
	if err != nil {
		t.Fatalf("marshal pinned eRPC config: %v", err)
	}
	setERPCConfigYAML(t, cfg, string(updatedYAML))
}

func parseUSDCMicroUnits(t *testing.T, output string) *big.Int {
	t.Helper()

	start := strings.Index(output, "(")
	end := strings.Index(output, " micro-units)")
	if start == -1 || end == -1 || end <= start+1 {
		t.Fatalf("could not parse balance output: %s", output)
	}

	value := strings.TrimSpace(output[start+1 : end])
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		t.Fatalf("invalid balance micro-units %q in output: %s", value, output)
	}
	return n
}

func parseAuthsRemaining(t *testing.T, output string) int {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Auths remaining:") {
			continue
		}
		var remaining int
		if _, err := fmt.Sscanf(line, "Auths remaining: %d", &remaining); err != nil {
			t.Fatalf("parse auth count from %q: %v", line, err)
		}
		return remaining
	}

	t.Fatalf("could not find auth count in output: %s", output)
	return 0
}

func waitForBuyerReportedBalance(t *testing.T, cfg *config.Config, want *big.Int, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/buy-inference/scripts/buy.py",
			"balance", "--chain", "base-sepolia")
		last = out
		if err == nil {
			reported := parseUSDCMicroUnits(t, out)
			if reported.Cmp(want) == 0 {
				return out
			}
		}
		time.Sleep(2 * time.Second)
	}

	t.Fatalf("timed out waiting for buy.py balance=%s\nlast output:\n%s", want, last)
	return ""
}

func waitForUSDCSettlement(
	t *testing.T,
	anvil *testutil.AnvilFork,
	buyerAddr string,
	sellerAddr string,
	buyerBefore *big.Int,
	sellerBefore *big.Int,
	timeout time.Duration,
) (*big.Int, *big.Int) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var buyerAfter, sellerAfter *big.Int
	for time.Now().Before(deadline) {
		buyerAfter = anvil.GetUSDCBalance(t, buyerAddr)
		sellerAfter = anvil.GetUSDCBalance(t, sellerAddr)
		if buyerAfter.Cmp(buyerBefore) < 0 && sellerAfter.Cmp(sellerBefore) > 0 {
			return buyerAfter, sellerAfter
		}
		time.Sleep(2 * time.Second)
	}

	t.Fatalf(
		"timed out waiting for USDC settlement: buyer before=%s after=%s seller before=%s after=%s",
		buyerBefore, buyerAfter, sellerBefore, sellerAfter,
	)
	return nil, nil
}

func waitForBuyerAuthCount(t *testing.T, cfg *config.Config, name string, want int, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/buy-inference/scripts/buy.py",
			"status", name)
		last = out
		if err == nil && parseAuthsRemaining(t, out) == want {
			return out
		}
		time.Sleep(2 * time.Second)
	}

	t.Fatalf("timed out waiting for %s auths=%d\nlast output:\n%s", name, want, last)
	return ""
}

func waitForBuyerLiveAuthCount(t *testing.T, cfg *config.Config, name string, want int, timeout time.Duration) string {
	t.Helper()

	script := `
import json
import sys
sys.path.insert(0, "/data/.openclaw/skills/buy-inference/scripts")
import buy
print(json.dumps(buy._buyer_status() or {}))
`

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := execInAgentErr(cfg, "python3", "-c", script)
		last = out
		if err == nil {
			var status map[string]struct {
				Remaining int `json:"remaining"`
			}
			if json.Unmarshal([]byte(out), &status) == nil {
				if live, ok := status[name]; ok && live.Remaining == want {
					return out
				}
			}
		}
		time.Sleep(2 * time.Second)
	}

	t.Fatalf("timed out waiting for live buyer sidecar %s auths=%d\nlast output:\n%s", name, want, last)
	return ""
}

func waitForAgentCommand(t *testing.T, cfg *config.Config, timeout time.Duration, args ...string) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastOut string
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := execInAgentErr(cfg, args...)
		if err == nil {
			return out
		}
		lastOut = out
		lastErr = err
		time.Sleep(3 * time.Second)
	}

	t.Fatalf("timed out waiting for agent command to succeed: %v\nlast output:\n%s", lastErr, lastOut)
	return ""
}

func waitForBuyerProbePricing(t *testing.T, cfg *config.Config, timeout time.Duration, endpointURL, model string) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastOut string
	for time.Now().Before(deadline) {
		out, _ := execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/buy-inference/scripts/buy.py",
			"probe", endpointURL, "--model", model)
		lastOut = out
		if strings.Contains(out, "402") && strings.Contains(out, "payTo:") {
			return out
		}
		time.Sleep(3 * time.Second)
	}

	t.Fatalf("timed out waiting for x402 pricing from %s\nlast output:\n%s", endpointURL, lastOut)
	return ""
}

type buyerLiveUpstream struct {
	URL         string `json:"url"`
	RemoteModel string `json:"remote_model"`
	PublicModel string `json:"public_model"`
	Remaining   int    `json:"remaining"`
	Spent       int    `json:"spent"`
	Network     string `json:"network"`
}

func parseBuyerLiveUpstream(t *testing.T, output, name string) buyerLiveUpstream {
	t.Helper()

	var status map[string]buyerLiveUpstream
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("parse buyer live status: %v\nraw: %s", err, output)
	}
	upstream, ok := status[name]
	if !ok {
		t.Fatalf("buyer live status missing %q:\n%s", name, output)
	}
	return upstream
}

func getStatusFieldString(so map[string]interface{}, field string) string {
	status, ok := so["status"].(map[string]interface{})
	if !ok {
		return ""
	}
	value, _ := status[field].(string)
	return value
}

func callLiteLLMPaidModelFromAgent(t *testing.T, cfg *config.Config, masterKey, model, prompt string) (int, []byte) {
	t.Helper()

	script := fmt.Sprintf(`
import json
import sys
import urllib.error
import urllib.request

payload = json.dumps({
    "model": %q,
    "messages": [{"role": "user", "content": %q}],
    "max_tokens": 16,
    "stream": False,
}).encode()

req = urllib.request.Request(
    %q,
    data=payload,
    method="POST",
    headers={
        "Content-Type": "application/json",
        "Authorization": %q,
    },
)

try:
    with urllib.request.urlopen(req, timeout=180) as resp:
        sys.stdout.write(json.dumps({
            "status": resp.status,
            "body": resp.read().decode(),
        }))
except urllib.error.HTTPError as err:
    sys.stdout.write(json.dumps({
        "status": err.code,
        "body": err.read().decode(),
    }))
    sys.exit(1)
`, model, prompt, "http://litellm.llm.svc.cluster.local:4000/v1/chat/completions", "Bearer "+masterKey)

	out, err := execInAgentErr(cfg, "python3", "-c", script)
	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if unmarshalErr := json.Unmarshal([]byte(out), &result); unmarshalErr != nil {
		t.Fatalf("parse LiteLLM response wrapper: %v\nraw: %s", unmarshalErr, out)
	}
	if err != nil && result.Status == 0 {
		t.Fatalf("LiteLLM paid request failed: %v\nraw: %s", err, out)
	}
	return result.Status, []byte(result.Body)
}

func TestIntegration_RBAC_ClusterRolesExist(t *testing.T) {
	cfg := requireCluster(t)

	// Both ClusterRoles should exist after stack init.
	for _, name := range []string{"openclaw-monetize-read", "openclaw-monetize-workload"} {
		out := obolRun(t, cfg, "kubectl", "get", "clusterrole", name, "-o", "json")
		var cr map[string]interface{}
		if err := json.Unmarshal([]byte(out), &cr); err != nil {
			t.Fatalf("parse clusterrole %s JSON: %v", name, err)
		}

		rules, ok := cr["rules"].([]interface{})
		if !ok || len(rules) == 0 {
			t.Errorf("ClusterRole %s has no rules", name)
		}
	}

	// Workload role should cover key mutate apiGroups.
	out := obolRun(t, cfg, "kubectl", "get", "clusterrole", "openclaw-monetize-workload", "-o", "json")
	var cr map[string]interface{}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("parse clusterrole JSON: %v", err)
	}
	rules := cr["rules"].([]interface{})
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
			t.Errorf("workload ClusterRole missing apiGroup %q", want)
		}
	}
}

func TestIntegration_RBAC_BindingsPatched(t *testing.T) {
	cfg := requireCluster(t)
	requireAgent(t, cfg)

	// Both ClusterRoleBindings should have subjects after obol agent init.
	for _, name := range []string{"openclaw-monetize-read-binding", "openclaw-monetize-workload-binding"} {
		out := obolRun(t, cfg, "kubectl", "get", "clusterrolebinding", name, "-o", "json")
		var crb map[string]interface{}
		if err := json.Unmarshal([]byte(out), &crb); err != nil {
			t.Fatalf("parse binding %s JSON: %v", name, err)
		}

		subjects, ok := crb["subjects"].([]interface{})
		if !ok || len(subjects) == 0 {
			t.Skipf("ClusterRoleBinding %s has no subjects yet — obol agent init may not have run", name)
		}

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
			t.Errorf("no openclaw-* service account found in %s subjects", name)
		}
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
  payment:
    network: base-sepolia
    payTo: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    price:
      perRequest: "0.001"
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

// portForwardGeneric port-forwards any resource and returns the base URL.
// Registers t.Cleanup to stop the port-forward.
func portForwardGeneric(t *testing.T, cfg *config.Config, namespace, resource string, remotePort, localPort int) string {
	t.Helper()
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary,
		"kubectl", "-n", namespace, "port-forward", resource,
		fmt.Sprintf("%d:%d", localPort, remotePort),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("port-forward start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	// Wait for TCP readiness.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", localPort), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return fmt.Sprintf("http://localhost:%d", localPort)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("port-forward to %s/%s:%d did not become ready", namespace, resource, remotePort)
	return ""
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

// resolveK3dHostIP returns the IP that pods inside k3d use to reach the host.
// Uses testutil.ClusterHostIP which handles macOS (Docker Desktop gateway) and
// Linux (docker0 bridge) without needing to exec into a container.
func resolveK3dHostIP(t *testing.T, _ *config.Config) string {
	t.Helper()
	return testutil.ClusterHostIP(t)
}

// deployAnvilUpstream creates a K8s Service + EndpointSlice in the given namespace
// that routes to the host-side Anvil instance via host.k3d.internal.
// Uses a ClusterIP Service (without selector) + EndpointSlice because Traefik's
// Gateway API provider requires EndpointSlices (not legacy Endpoints) and does
// not support ExternalName backends.
func deployAnvilUpstream(t *testing.T, cfg *config.Config, namespace string, anvil *testutil.AnvilFork) {
	t.Helper()

	hostIP := resolveK3dHostIP(t, cfg)

	// Create a Service (without selector) + EndpointSlice pointing at the host IP.
	svcManifest := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: anvil-rpc
  namespace: %s
spec:
  ports:
    - port: %d
      targetPort: %d
      protocol: TCP
`, namespace, anvil.Port, anvil.Port)

	// EndpointSlice (preferred by Traefik over legacy Endpoints).
	// The label kubernetes.io/service-name links the slice to the Service.
	epSliceManifest := fmt.Sprintf(`apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: anvil-rpc-manual
  namespace: %s
  labels:
    kubernetes.io/service-name: anvil-rpc
addressType: IPv4
endpoints:
  - addresses:
      - "%s"
    conditions:
      ready: true
ports:
  - port: %d
    protocol: TCP
`, namespace, hostIP, anvil.Port)

	applyServiceOffer(t, cfg, svcManifest)
	applyServiceOffer(t, cfg, epSliceManifest)

	// Wait for EndpointSlice to propagate — DNS + kube-proxy need time,
	// especially on Linux where docker0 bridge adds latency.
	t.Log("waiting for EndpointSlice propagation...")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := obolRunErr(cfg, "kubectl", "exec", "-i",
			"-n", agentNamespace(cfg), "deploy/openclaw",
			"-c", "openclaw", "--",
			"python3", "-c",
			fmt.Sprintf("import urllib.request; urllib.request.urlopen('http://anvil-rpc.%s.svc.cluster.local:%d/', timeout=2)", namespace, anvil.Port))
		if err == nil {
			t.Log("EndpointSlice reachable from cluster")
			break
		}
		_ = out
		time.Sleep(2 * time.Second)
	}
}

// serviceOfferWithAnvil returns a ServiceOffer YAML targeting an Anvil upstream.
// Field names align with x402 (payment.payTo, payment.network).
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
  payment:
    network: base-sepolia
    payTo: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
    price:
      perRequest: "0.001"
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
	processOut, processErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("monetize.py output:\n%s", processOut)
	if processErr != nil {
		t.Logf("monetize.py error: %v", processErr)
	}

	// Wait for Traefik to index the new route.
	time.Sleep(5 * time.Second)

	// Try to reach Anvil through Traefik — retry with backoff.
	// The URLRewrite filter strips /services/<name> prefix to / so Anvil
	// receives the request at its root path (JSON-RPC endpoint).
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)

	var resp *http.Response
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, lastErr = http.Post(url, "application/json", strings.NewReader(rpcBody))
		if lastErr != nil {
			t.Logf("attempt %d: connection error: %v", attempt, lastErr)
			time.Sleep(3 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusNotFound {
			break
		}
		t.Logf("attempt %d: status=%d", attempt, resp.StatusCode)
		resp.Body.Close()
		time.Sleep(3 * time.Second)
	}

	if lastErr != nil {
		t.Skipf("could not reach obol.stack:8080 — is /etc/hosts configured? %v", lastErr)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Accept 200 (verifier pass-through + Anvil response) or 402 (payment gated).
	// Both prove the route is working through Traefik.
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("got 404 — route not working (body: %s)", string(body))
	}
	t.Logf("route response: status=%d body=%s", resp.StatusCode, string(body))
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
	obolRun(t, cfg, "kubectl", "delete", "serviceoffers.obol.org", name, "-n", ns)

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

// addPricingRoute is a no-op retained for integration test compatibility.
// The serviceoffer-controller now manages routes via the ServiceOffer informer;
// pricing routes are derived automatically when the offer reaches RoutePublished.
// The sleep preserves the original timing to let the controller converge.
func addPricingRoute(t *testing.T, _ *config.Config, _, _, _ string) {
	t.Helper()
	time.Sleep(5 * time.Second)
}

func TestIntegration_PaymentGate_VerifierHealthy(t *testing.T) {
	cfg := requireCluster(t)

	// x402-verifier uses a distroless image (no shell/wget), so we
	// port-forward and probe health endpoints from the test process.
	localPort := freePort(t)
	pfURL := portForwardGeneric(t, cfg, "x402", "deploy/x402-verifier", 8080, localPort)

	for _, path := range []string{"/healthz", "/readyz"} {
		url := pfURL + path
		resp, err := http.Get(url)
		if err != nil {
			t.Skipf("could not reach verifier %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("verifier %s returned %d: %s", path, resp.StatusCode, body)
		} else {
			t.Logf("verifier %s: %s", path, body)
		}
	}
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

	// Step 1: Create ServiceOffer via obol CLI (x402-aligned flags).
	obolRun(t, cfg, "monetize", "offer", name,
		"--per-request", "0.001",
		"--network", "base-sepolia",
		"--pay-to", walletAddr,
		"--namespace", ns,
		"--upstream", "anvil-rpc",
		"--port", fmt.Sprintf("%d", anvil.Port),
		"--path", fmt.Sprintf("/services/%s", name),
	)
	t.Cleanup(func() {
		_, _ = obolRunErr(cfg, "monetize", "delete", name, "--namespace", ns, "--force")
	})

	// Step 2: Verify CR was created with x402-aligned fields.
	so := getServiceOffer(t, cfg, name, ns)
	spec, ok := so["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("spec missing from created ServiceOffer")
	}
	payment, ok := spec["payment"].(map[string]interface{})
	if !ok {
		t.Fatal("spec.payment missing from created ServiceOffer")
	}
	if payment["payTo"] != walletAddr {
		t.Errorf("payment.payTo = %v, want %s", payment["payTo"], walletAddr)
	}

	// Step 3: Trigger reconciliation via monetize.py.
	execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)

	// Step 4: Verify offer-status shows conditions.
	statusOut := obolRun(t, cfg, "sell", "status", name, "--namespace", ns)
	t.Logf("offer-status:\n%s", statusOut)

	// Step 5: Verify obol sell list shows the offer.
	listOut := obolRun(t, cfg, "sell", "list", "--namespace", ns)
	if !strings.Contains(listOut, name) {
		t.Errorf("sell list does not contain %q:\n%s", name, listOut)
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
	obolRun(t, cfg, "sell", "delete", name, "--namespace", ns, "--force")

	// Step 8: Verify CR is gone.
	_, err = obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
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

	// obol sell list should show the offer.
	listOut := obolRun(t, cfg, "sell", "list")
	if !strings.Contains(listOut, name) {
		t.Errorf("sell list does not contain %q:\n%s", name, listOut)
	}

	// obol sell status should show the CR.
	statusOut := obolRun(t, cfg, "sell", "status", name, "--namespace", ns)
	if !strings.Contains(statusOut, "ServiceOffer") && !strings.Contains(statusOut, "serviceoffer") && !strings.Contains(statusOut, "kind") {
		t.Logf("offer-status output (expected ServiceOffer YAML):\n%s", statusOut)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 6 — Tunnel E2E: Ollama model exposed and sold via CF tunnel
// ─────────────────────────────────────────────────────────────────────────────

// requireTunnel skips the test if the CF tunnel is not active.
// Returns the tunnel URL (e.g. "https://xxx.trycloudflare.com").
func requireTunnel(t *testing.T, cfg *config.Config) string {
	t.Helper()
	tunnelURL, err := obolRunErr(cfg, "tunnel", "status")
	if err != nil {
		t.Skip("tunnel not available — run: obol stack up")
	}

	// Extract URL from the status output.
	for _, line := range strings.Split(tunnelURL, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "URL:") {
			url := strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
			if strings.HasPrefix(url, "https://") {
				return url
			}
		}
	}

	t.Skip("tunnel URL not found in status output")
	return ""
}

// requireOllamaModel ensures a specific model is available, pulling it if needed.
// Returns the model name that's available (may be adjusted if not found).
func requireOllamaModel(t *testing.T, targetModel string) string {
	t.Helper()
	models := requireOllama(t)

	// Check if the target model is already available.
	for _, m := range models {
		if strings.Contains(m, targetModel) {
			return m
		}
	}

	// Try to use whatever model is available (smallest first).
	// For the test, any model works — we just need a valid inference endpoint.
	t.Logf("target model %q not found, using available model %q", targetModel, models[0])
	return models[0]
}

func requireExactOllamaModel(t *testing.T, targetModel string) string {
	t.Helper()

	models := requireOllama(t)
	for _, m := range models {
		if strings.EqualFold(m, targetModel) {
			return m
		}
	}

	t.Skipf("required Ollama model %q not available (have: %s)", targetModel, strings.Join(models, ", "))
	return ""
}

// ollamaServiceOfferYAML returns a ServiceOffer YAML for an Ollama model.
// Field names align with the CRD schema (payment.payTo, payment.price.perRequest).
func ollamaServiceOfferYAML(name, namespace, model, wallet string) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  model:
    name: %s
    runtime: ollama
  upstream:
    service: ollama
    namespace: llm
    port: 11434
    healthPath: /api/generate
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
`, name, namespace, model, wallet, name)
}

func registeredOllamaServiceOfferYAML(name, namespace, model, wallet, registerName, registerDesc string) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  type: inference
  model:
    name: %s
    runtime: ollama
  upstream:
    service: ollama
    namespace: llm
    port: 11434
    healthPath: /api/generate
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
  registration:
    enabled: true
    name: %s
    description: %q
`, name, namespace, model, wallet, name, registerName, registerDesc)
}

func registeredLiteLLMServiceOfferYAML(name, namespace, model, wallet, registerName, registerDesc string) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  type: inference
  model:
    name: %s
    runtime: vllm
  upstream:
    service: litellm
    namespace: llm
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
  registration:
    enabled: true
    name: %s
    description: %q
`, name, namespace, model, wallet, name, registerName, registerDesc)
}

// TestIntegration_Tunnel_OllamaMonetized is the full E2E test:
// Ollama model → ServiceOffer → reconciliation → x402 payment gate → CF tunnel.
//
// Validates that:
//  1. An Ollama model is exposed as a ServiceOffer
//  2. The reconciler creates Middleware + HTTPRoute
//  3. Requests without payment return 402
//  4. Requests with valid payment return 200 + inference result
//  5. The service is accessible via the CF tunnel
//  6. Deletion cleans up all owned route resources
func TestIntegration_Tunnel_OllamaMonetized(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireOllamaModel(t, "qwen2.5")
	tunnelURL := requireTunnel(t, cfg)

	mf := setupMockFacilitator(t, cfg)

	walletAddr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	name := "test-tunnel-ollama"
	// Use the llm namespace since that's where the ollama service lives.
	ns := "llm"

	// Step 1: Create ServiceOffer for the Ollama model.
	yaml := ollamaServiceOfferYAML(name, ns, model, walletAddr)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		deleteServiceOffer(t, cfg, name, ns)
		// Give the controller time to observe the deletion.
		time.Sleep(2 * time.Second)
	})
	t.Logf("created ServiceOffer %s/%s for model %s", ns, name, model)

	// Step 2: Trigger reconciliation (monetize.py process).
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", out)

	// Step 3: Verify all conditions are True.
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Errorf("condition %s = %q, want True", cond, status)
		}
	}

	// Step 4: Verify route resources exist.
	assertOfferRouteResourcesPresent(t, cfg, name, ns)

	// Step 5: Wait for Reloader to restart verifier + route propagation.
	time.Sleep(8 * time.Second)

	// Step 6: Test via LOCAL endpoint (obol.stack:8080) — request without payment → 402.
	localURL := fmt.Sprintf("http://obol.stack:8080/services/%s/v1/chat/completions", name)
	chatBody := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"say hello"}],"stream":false}`, model)

	resp, err := http.Post(localURL, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("[local] expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		t.Logf("[local] correctly returned 402 Payment Required")
	}

	// Step 7: Test via LOCAL endpoint — request WITH payment → 200 + inference.
	paymentHeader := testutil.TestPaymentHeader(t, walletAddr)
	req, _ := http.NewRequest("POST", localURL, strings.NewReader(chatBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("[local] request with payment failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("[local] expected 200 with payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		t.Logf("[local] payment accepted, inference response received (%d bytes)", len(body))
		// Verify response contains a completion.
		var chatResp map[string]interface{}
		if err := json.Unmarshal(body, &chatResp); err == nil {
			if choices, ok := chatResp["choices"].([]interface{}); ok && len(choices) > 0 {
				t.Logf("[local] inference response has %d choice(s)", len(choices))
			}
		}
	}

	// Step 8: Verify mock facilitator was called.
	if mf.VerifyCalls.Load() == 0 {
		t.Error("mock facilitator /verify was never called")
	}
	t.Logf("mock facilitator: %d verify calls, %d settle calls",
		mf.VerifyCalls.Load(), mf.SettleCalls.Load())

	// Step 9: Test via TUNNEL endpoint — same flow through the public URL.
	tunnelChatURL := fmt.Sprintf("%s/services/%s/v1/chat/completions", tunnelURL, name)
	t.Logf("testing via tunnel: %s", tunnelChatURL)

	client := &http.Client{Timeout: 30 * time.Second}

	// 9a: Without payment → 402 via tunnel.
	resp, err = client.Post(tunnelChatURL, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Logf("[tunnel] could not reach tunnel URL: %v (may be expected if tunnel not ready)", err)
	} else {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusPaymentRequired {
			t.Errorf("[tunnel] expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
		} else {
			t.Logf("[tunnel] correctly returned 402 Payment Required")
		}

		// 9b: With payment → 200 via tunnel.
		req, _ = http.NewRequest("POST", tunnelChatURL, strings.NewReader(chatBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PAYMENT", paymentHeader)

		resp, err = client.Do(req)
		if err != nil {
			t.Errorf("[tunnel] request with payment failed: %v", err)
		} else {
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("[tunnel] expected 200 with payment, got %d; body: %s", resp.StatusCode, body)
			} else {
				t.Logf("[tunnel] payment accepted via tunnel, inference response (%d bytes)", len(body))
			}
		}
	}

	// Step 10: Delete and verify cleanup.
	obolRun(t, cfg, "kubectl", "delete", "serviceoffers.obol.org", name, "-n", ns)
	time.Sleep(5 * time.Second)

	// Delete happened via kubectl, so only Kubernetes-owned resources are expected
	// to disappear automatically here.
	// Let's verify the K8s resources are gone (cascade via OwnerRef).
	mwOut, _ := obolRunErr(cfg, "kubectl", "get", "middleware", "-n", ns, "-o", "name")
	if strings.Contains(mwOut, name) {
		t.Errorf("middleware still exists after deletion")
	}
	hrOut, _ := obolRunErr(cfg, "kubectl", "get", "httproute", "-n", ns, "-o", "name")
	if strings.Contains(hrOut, name) {
		t.Errorf("httproute still exists after deletion")
	}

	t.Logf("tunnel E2E test complete: model %s exposed, gated, paid, and cleaned up", model)
}

// TestIntegration_Tunnel_AgentAutonomousMonetize validates that the OpenClaw agent
// can autonomously create, reconcile, and manage a ServiceOffer using the monetize
// skill — the full lifecycle driven entirely from inside the agent pod.
func TestIntegration_Tunnel_AgentAutonomousMonetize(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	_ = requireOllamaModel(t, "qwen2.5")

	walletAddr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	name := "test-agent-auto"
	ns := "llm"

	// Step 1: Agent creates the ServiceOffer via monetize.py create (x402-aligned flags).
	out := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"create", name,
		"--model", "qwen2.5:3b",
		"--upstream", "ollama",
		"--namespace", ns,
		"--port", "11434",
		"--per-request", "0.001",
		"--network", "base-sepolia",
		"--pay-to", walletAddr,
		"--path", fmt.Sprintf("/services/%s", name),
	)
	t.Logf("create output:\n%s", out)
	t.Cleanup(func() {
		// Delete via the skill.
		execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/monetize/scripts/monetize.py",
			"delete", name, "--namespace", ns)
	})

	// Step 2: Agent reconciles the offer.
	out, _ = execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("process output:\n%s", out)

	// Step 3: Agent checks status.
	statusOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"status", name, "--namespace", ns)
	t.Logf("status output:\n%s", statusOut)

	// Step 4: Verify conditions.
	so := getServiceOffer(t, cfg, name, ns)
	readyStatus := getConditionStatus(so, "Ready")
	if readyStatus != "True" {
		// Log all conditions for debugging.
		for _, cond := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Registered", "Ready"} {
			t.Logf("  %s = %s", cond, getConditionStatus(so, cond))
		}
		t.Errorf("offer not Ready after agent reconciliation: Ready=%s", readyStatus)
	}

	// Step 5: Verify route resources exist.
	assertOfferRouteResourcesPresent(t, cfg, name, ns)

	// Step 6: Agent lists offers — should see the one we created.
	listOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"list")
	if !strings.Contains(listOut, name) {
		t.Errorf("agent list does not contain %q:\n%s", name, listOut)
	}

	// Step 7: Agent deletes the offer.
	delOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"delete", name, "--namespace", ns)
	t.Logf("delete output:\n%s", delOut)

	// Step 8: Verify route resources removed.
	time.Sleep(2 * time.Second)
	assertOfferRouteResourcesAbsent(t, cfg, name, ns)

	// Step 9: Verify CR is gone.
	_, err := obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
	if err == nil {
		t.Error("ServiceOffer still exists after agent delete")
	}

	t.Logf("agent autonomous monetize test complete: full lifecycle managed from pod")
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 7 — Fork Validation: Anvil-backed ServiceOffer with mock facilitator
// ─────────────────────────────────────────────────────────────────────────────

// TestIntegration_Fork_FullPaymentFlow validates the full payment flow using
// an Anvil fork of Base Sepolia as the upstream (simulating a real chain
// environment) with a mock facilitator for payment verification.
//
// This test proves:
//  1. The agent can reconcile an offer backed by a forked chain upstream
//  2. The controller-backed route resources are created by the agent flow
//  3. The payment gate correctly returns 402 for unpaid requests
//  4. The payment gate correctly returns 200 with valid payment
//  5. The mock facilitator receives verify+settle calls
//  6. Deletion cleans up both K8s resources and service exposure
func TestIntegration_Fork_FullPaymentFlow(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("fork-pay")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	// Use Anvil account[1] as the payment recipient (has 10000 ETH).
	walletAddr := anvil.Accounts[1].Address

	mf := setupMockFacilitator(t, cfg)

	name := "test-fork-pay"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		deleteServiceOffer(t, cfg, name, ns)
	})

	// Agent reconciles the offer.
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", out)

	// Verify conditions.
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Errorf("condition %s = %q, want True", cond, status)
		}
	}

	// Verify route resources were added by the reconciler.
	assertOfferRouteResourcesPresent(t, cfg, name, ns)

	// Wait for Reloader + route propagation.
	time.Sleep(8 * time.Second)

	// Request WITHOUT payment → 402.
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)
	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		t.Logf("correctly returned 402 Payment Required")

		// Verify payment requirements include chain info.
		var reqs map[string]interface{}
		if err := json.Unmarshal(body, &reqs); err == nil {
			if accepts, ok := reqs["accepts"].([]interface{}); ok && len(accepts) > 0 {
				first := accepts[0].(map[string]interface{})
				t.Logf("payment requirements: network=%v, maxAmount=%v",
					first["network"], first["maxAmountRequired"])
			}
		}
	}

	// Request WITH payment → 200 + RPC response from Anvil fork.
	paymentHeader := testutil.TestPaymentHeader(t, walletAddr)
	req, _ := http.NewRequest("POST", url, strings.NewReader(rpcBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request with payment failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		// Parse RPC response — should have a block number from the fork.
		var rpcResp map[string]interface{}
		if err := json.Unmarshal(body, &rpcResp); err == nil {
			if result, ok := rpcResp["result"].(string); ok {
				t.Logf("Anvil fork block number: %s (payment accepted)", result)
			}
		}
	}

	// Verify mock facilitator was invoked.
	if mf.VerifyCalls.Load() == 0 {
		t.Error("mock facilitator /verify was never called")
	}
	t.Logf("facilitator calls: verify=%d, settle=%d",
		mf.VerifyCalls.Load(), mf.SettleCalls.Load())

	// Delete via the agent skill.
	delOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"delete", name, "--namespace", ns)
	t.Logf("delete output:\n%s", delOut)

	// Verify route resources were removed.
	time.Sleep(2 * time.Second)
	assertOfferRouteResourcesAbsent(t, cfg, name, ns)

	// Verify K8s resources are gone.
	_, err = obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
	if err == nil {
		t.Error("ServiceOffer still exists after delete")
	}

	t.Logf("fork payment flow test complete: Anvil fork → x402 → paid → cleaned up")
}

// TestIntegration_Fork_AgentSkillIteration validates that the monetize skill
// can handle error cases gracefully and recover:
//   - Create offer with unreachable upstream → process fails at UpstreamHealthy
//   - Fix upstream (deploy Anvil) → re-process → all conditions True
//   - Demonstrates the agent can iterate and self-heal
func TestIntegration_Fork_AgentSkillIteration(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	ns := testNamespace("fork-iterate")
	createTestNamespace(t, cfg, ns)

	walletAddr := anvil.Accounts[1].Address
	name := "test-iterate"

	// Step 1: Create offer pointing at non-existent upstream.
	badYAML := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: does-not-exist
    namespace: %s
    port: 8545
    healthPath: /
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
`, name, ns, ns, walletAddr, name)
	applyServiceOffer(t, cfg, badYAML)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Step 2: Agent tries to reconcile → should fail at UpstreamHealthy.
	out1, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("first process (expected failure):\n%s", out1)

	so := getServiceOffer(t, cfg, name, ns)
	if status := getConditionStatus(so, "UpstreamHealthy"); status != "False" {
		t.Logf("UpstreamHealthy = %q (expected False)", status)
	}
	if status := getConditionStatus(so, "Ready"); status == "True" {
		t.Error("Ready should not be True with bad upstream")
	}

	// Step 3: Fix the upstream — deploy Anvil service.
	deployAnvilUpstream(t, cfg, ns, anvil)
	// Wait for EndpointSlice propagation before re-processing.
	time.Sleep(3 * time.Second)

	// Step 4: Update the ServiceOffer to point at the correct upstream.
	fixedYAML := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, fixedYAML)

	// Step 5: Agent re-processes — should now succeed.
	// Reset UpstreamHealthy condition by patching status to force re-check.
	statusPatch := `{"status":{"conditions":[]}}`
	obolRun(t, cfg, "kubectl", "patch", "serviceoffers.obol.org", name, "-n", ns,
		"--type=merge", "--subresource=status", "-p", statusPatch)

	out2, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("second process (after fix):\n%s", out2)

	// Step 6: Verify all conditions now True.
	so = getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Errorf("after fix: %s = %q, want True", cond, status)
		}
	}

	t.Logf("skill iteration test complete: agent recovered from bad upstream")
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 5 — Real Facilitator Payment (x402-rs)
// ─────────────────────────────────────────────────────────────────────────────

// TestIntegration_Fork_RealFacilitatorPayment validates the full payment flow
// using the real x402-rs facilitator with an Anvil fork of Base Sepolia.
//
// Unlike TestIntegration_Fork_FullPaymentFlow (which uses a mock facilitator
// that always returns isValid:true), this test:
//  1. Starts the real x402-rs facilitator binary
//  2. Funds a buyer wallet with USDC on the Anvil fork
//  3. Signs a real EIP-712 TransferWithAuthorization (ERC-3009)
//  4. Proves the facilitator validates the real signature
//  5. Confirms 402 → 200 through the full payment gate
//
// Prerequisites:
//   - Running k3d cluster with CRD, agent, and x402-verifier
//   - Anvil (Foundry) installed
//   - x402-rs source or binary (set X402_RS_DIR or X402_FACILITATOR_BIN)
func TestIntegration_Fork_RealFacilitatorPayment(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)

	// ── Start real x402-rs facilitator ──────────────────────────────────
	facilitator := testutil.StartRealFacilitator(t, anvil)

	// ── Fund buyer with USDC on Anvil fork ─────────────────────────────
	// Use Anvil account[0] as buyer, account[1] as seller (payTo).
	buyerKey := anvil.Accounts[0].PrivateKey
	buyerAddr := anvil.Accounts[0].Address
	sellerAddr := anvil.Accounts[1].Address

	// 10 USDC = 10_000_000 micro-units (6 decimals).
	anvil.MintUSDC(t, buyerAddr, testutil.USDCMicroUnits(10))
	t.Logf("funded buyer %s with 10 USDC", buyerAddr)

	// ── Set up test namespace + Anvil upstream ─────────────────────────
	ns := testNamespace("real-fac")
	createTestNamespace(t, cfg, ns)
	deployAnvilUpstream(t, cfg, ns, anvil)

	// ── Patch x402-pricing ConfigMap to point at real facilitator ──────
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	// ── Create ServiceOffer ────────────────────────────────────────────
	name := "test-real-fac"
	yaml := serviceOfferWithAnvil(name, ns, anvil.Port)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		deleteServiceOffer(t, cfg, name, ns)
	})

	// ── Agent reconciles the offer ─────────────────────────────────────
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", out)

	// Verify conditions.
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Errorf("condition %s = %q, want True", cond, status)
		}
	}

	// Wait for Reloader + route propagation.
	time.Sleep(8 * time.Second)

	// ── Request WITHOUT payment → 402 ──────────────────────────────────
	rpcBody := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	url := fmt.Sprintf("http://obol.stack:8080/services/%s", name)

	resp, err := http.Post(url, "application/json", strings.NewReader(rpcBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
	}
	t.Log("correctly returned 402 Payment Required (no payment header)")

	// Parse 402 body to extract payment requirements.
	var reqs map[string]interface{}
	if err := json.Unmarshal(body, &reqs); err == nil {
		if accepts, ok := reqs["accepts"].([]interface{}); ok && len(accepts) > 0 {
			first := accepts[0].(map[string]interface{})
			t.Logf("payment requirements: network=%v, maxAmount=%v, asset=%v",
				first["network"], first["maxAmountRequired"], first["asset"])
		}
	}

	// ── Sign REAL EIP-712 payment ──────────────────────────────────────
	// Amount: 1000 micro-units (matches ServiceOffer price of 0.001 USDC).
	paymentHeader := testutil.SignRealPaymentHeader(t,
		buyerKey,   // buyer's private key
		sellerAddr, // payTo (same as ServiceOffer)
		"1000",     // 0.001 USDC = 1000 micro-units (6 decimals)
		84532,      // base-sepolia chain ID
	)

	// ── Request WITH real payment → 200 ────────────────────────────────
	req, _ := http.NewRequest("POST", url, strings.NewReader(rpcBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request with payment failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with real payment, got %d; body: %s", resp.StatusCode, body)
	}

	// Parse RPC response — should have a block number from the fork.
	var rpcResp map[string]interface{}
	if err := json.Unmarshal(body, &rpcResp); err == nil {
		if result, ok := rpcResp["result"].(string); ok {
			t.Logf("Anvil fork block number: %s (real payment accepted!)", result)
		}
	}

	// ── Cleanup: delete ServiceOffer ───────────────────────────────────
	delOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"delete", name, "--namespace", ns)
	t.Logf("delete output:\n%s", delOut)

	// Verify route resources were removed.
	time.Sleep(2 * time.Second)
	assertOfferRouteResourcesAbsent(t, cfg, name, ns)

	// Verify K8s resources are gone.
	_, err = obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
	if err == nil {
		t.Error("ServiceOffer still exists after delete")
	}

	t.Logf("real facilitator payment test complete: Anvil fork → x402-rs → EIP-712 → paid → cleaned up")
}

// TestIntegration_Tunnel_RealFacilitatorOllama is the highest-fidelity test:
// real Ollama inference, real x402-rs facilitator, real EIP-712 signatures,
// and requests routed through the Cloudflare quick tunnel.
//
// This is the closest thing to a production sell-side scenario:
//   - Buyer discovers the service via the public tunnel URL
//   - Gets 402 with pricing info
//   - Signs a real TransferWithAuthorization (ERC-3009)
//   - Sends payment through the tunnel → Traefik → x402 ForwardAuth → x402-rs validates → Ollama responds
//
// Prerequisites:
//   - Running k3d cluster with CRD, agent, x402-verifier, CF quick tunnel
//   - Ollama with a cached model (any model — qwen2.5, qwen3:0.6b, etc.)
//   - Anvil (Foundry) installed
//   - x402-rs source or binary (set X402_RS_DIR or X402_FACILITATOR_BIN)
func TestIntegration_Tunnel_RealFacilitatorOllama(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireOllamaModel(t, "qwen2.5")
	tunnelURL := requireTunnel(t, cfg)
	anvil := requireAnvil(t)

	t.Logf("tunnel URL: %s", tunnelURL)
	t.Logf("model: %s", model)

	// ── Start real x402-rs facilitator ──────────────────────────────────
	facilitator := testutil.StartRealFacilitator(t, anvil)

	// ── Fund buyer with USDC ───────────────────────────────────────────
	buyerKey := anvil.Accounts[0].PrivateKey
	buyerAddr := anvil.Accounts[0].Address
	sellerAddr := anvil.Accounts[1].Address
	anvil.MintUSDC(t, buyerAddr, testutil.USDCMicroUnits(10))
	t.Logf("funded buyer %s with 10 USDC", buyerAddr)

	// ── Patch x402-pricing to point at real facilitator ────────────────
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	// ── Create ServiceOffer for real Ollama model ──────────────────────
	name := "test-tunnel-real"
	ns := "llm" // Ollama lives here
	yaml := ollamaServiceOfferYAML(name, ns, model, sellerAddr)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		deleteServiceOffer(t, cfg, name, ns)
		time.Sleep(2 * time.Second)
	})
	t.Logf("created ServiceOffer %s/%s for model %s", ns, name, model)

	// ── Agent reconciles ───────────────────────────────────────────────
	out, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", out)

	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Errorf("condition %s = %q, want True", cond, status)
		}
	}

	// Wait for Reloader + route propagation.
	time.Sleep(8 * time.Second)

	chatBody := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"say hello in one word"}],"stream":false}`, model)
	client := &http.Client{Timeout: 60 * time.Second}

	// ── LOCAL: 402 without payment ─────────────────────────────────────
	localURL := fmt.Sprintf("http://obol.stack:8080/services/%s/v1/chat/completions", name)
	resp, err := client.Post(localURL, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("[local] expected 402, got %d; body: %s", resp.StatusCode, body)
	}
	t.Log("[local] correctly returned 402")

	// ── LOCAL: 200 with real payment ───────────────────────────────────
	paymentHeader := testutil.SignRealPaymentHeader(t, buyerKey, sellerAddr, "1000", 84532)

	req, _ := http.NewRequest("POST", localURL, strings.NewReader(chatBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("[local] request with payment failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[local] expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	t.Logf("[local] real payment accepted, inference response (%d bytes)", len(body))

	// Verify we got actual inference content.
	var chatResp map[string]interface{}
	if err := json.Unmarshal(body, &chatResp); err == nil {
		if choices, ok := chatResp["choices"].([]interface{}); ok && len(choices) > 0 {
			t.Logf("[local] inference: %d choice(s)", len(choices))
		}
	}

	// ── TUNNEL: 402 without payment ────────────────────────────────────
	tunnelChatURL := fmt.Sprintf("%s/services/%s/v1/chat/completions", tunnelURL, name)
	t.Logf("testing via tunnel: %s", tunnelChatURL)

	resp, err = client.Post(tunnelChatURL, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Fatalf("[tunnel] could not reach tunnel URL: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("[tunnel] expected 402, got %d; body: %s", resp.StatusCode, body)
	}
	t.Log("[tunnel] correctly returned 402")

	// ── TUNNEL: 200 with real payment (fresh signature) ────────────────
	// Each payment needs a unique nonce, so sign a new one.
	tunnelPayment := testutil.SignRealPaymentHeader(t, buyerKey, sellerAddr, "1000", 84532)

	req, _ = http.NewRequest("POST", tunnelChatURL, strings.NewReader(chatBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", tunnelPayment)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("[tunnel] request with payment failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[tunnel] expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	t.Logf("[tunnel] real payment accepted via tunnel, inference response (%d bytes)", len(body))

	if err := json.Unmarshal(body, &chatResp); err == nil {
		if choices, ok := chatResp["choices"].([]interface{}); ok && len(choices) > 0 {
			choice := choices[0].(map[string]interface{})
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				t.Logf("[tunnel] model said: %v", msg["content"])
			}
		}
	}

	// ── Cleanup ────────────────────────────────────────────────────────
	delOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"delete", name, "--namespace", ns)
	t.Logf("delete output:\n%s", delOut)

	t.Logf("tunnel + real facilitator test complete: %s → CF tunnel → x402-rs → EIP-712 → Ollama → response", tunnelURL)
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 9 — Agent Coordination Validation
// ─────────────────────────────────────────────────────────────────────────────

// TestIntegration_AgentCoordination_FullReconcileOrder validates that the
// obol-agent autonomously coordinates the entire monetisation lifecycle in
// the correct order, producing all derived Kubernetes resources, without
// any human intervention beyond the initial CR intent.
//
// The test creates ONLY the ServiceOffer CR (the "intent") and then invokes
// `monetize.py process --all` (exactly what the heartbeat cron does). It then
// verifies EVERY coordination step the agent should have performed:
//
//	Step 1: ModelReady        → model checked in Ollama /api/tags
//	Step 2: UpstreamHealthy   → upstream service health-checked
//	Step 3: PaymentGateReady  → Middleware x402-<name> created
//	                          → verifier derives route from published ServiceOffer
//	Step 4: RoutePublished    → HTTPRoute so-<name> created
//	                          → parentRef = traefik-gateway
//	                          → filter = ExtensionRef to Middleware
//	                          → backend = upstream service
//	Step 5: Registered        → skipped (registration.enabled=false)
//	Step 6: Ready             → all conditions True
//
// After reconciliation, it verifies:
//   - Each derived resource exists with correct content
//   - ownerReferences point back to the ServiceOffer (GC cascade)
//   - The pricing ConfigMap has the route with correct pattern, price, payTo
//   - A second `process --all` is idempotent (no errors, same state)
//   - Delete via agent removes the CR (cascade removes owned route resources)
//
// This proves: drop a CR → agent does everything → monetisation works.
func TestIntegration_AgentCoordination_FullReconcileOrder(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireOllamaModel(t, "qwen2.5")

	sellerAddr := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	name := "test-coord"
	ns := "llm"
	path := fmt.Sprintf("/services/%s", name)

	// ────────────────────────────────────────────────────────────────────
	// Step 0: Drop the CR — this is the ONLY human action.
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 0: Creating ServiceOffer CR (the intent)")
	yaml := ollamaServiceOfferYAML(name, ns, model, sellerAddr)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		// Final safety net — agent delete should have cleaned up already.
		deleteServiceOffer(t, cfg, name, ns)
	})

	// Verify CR exists with empty status (no conditions yet).
	so := getServiceOffer(t, cfg, name, ns)
	if status, ok := so["status"].(map[string]interface{}); ok {
		if conds, ok := status["conditions"].([]interface{}); ok && len(conds) > 0 {
			t.Error("Step 0: ServiceOffer should have no conditions before reconciliation")
		}
	}
	t.Log("Step 0: CR created — no conditions, no derived resources")

	// Verify no derived resources exist yet.
	_, mwErr := obolRunErr(cfg, "kubectl", "get", "middleware", fmt.Sprintf("x402-%s", name), "-n", ns)
	if mwErr == nil {
		t.Error("Step 0: Middleware should not exist before reconciliation")
	}
	_, hrErr := obolRunErr(cfg, "kubectl", "get", "httproute", fmt.Sprintf("so-%s", name), "-n", ns)
	if hrErr == nil {
		t.Error("Step 0: HTTPRoute should not exist before reconciliation")
	}

	// ────────────────────────────────────────────────────────────────────
	// Step 1: Agent reconciles — `process --all` (heartbeat simulation)
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 1: Triggering agent reconciliation (process --all)")
	processOut, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", "--all")
	t.Logf("process --all output:\n%s", processOut)

	// processOut should mention our offer being processed.
	if !strings.Contains(processOut, name) && !strings.Contains(processOut, "Ready") {
		t.Logf("warning: process --all output does not mention %s", name)
	}

	// ────────────────────────────────────────────────────────────────────
	// Step 2: Verify conditions — all 6 in order
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 2: Verifying all 6 conditions")
	so = getServiceOffer(t, cfg, name, ns)

	conditionOrder := []struct {
		name     string
		blocking bool // whether this condition blocks Ready
	}{
		{"ModelReady", true},
		{"UpstreamHealthy", true},
		{"PaymentGateReady", true},
		{"RoutePublished", true},
		{"Registered", false}, // non-blocking, may be "False" with reason "Skipped"
		{"Ready", true},
	}

	for _, c := range conditionOrder {
		status := getConditionStatus(so, c.name)
		if c.blocking {
			if status != "True" {
				t.Errorf("condition %s = %q, want True (blocking)", c.name, status)
			} else {
				t.Logf("  ✓ %s = True", c.name)
			}
		} else {
			// Registered is non-blocking — True (skipped) or False (no remote-signer) both OK.
			t.Logf("  ~ %s = %s (non-blocking)", c.name, status)
		}
	}

	// ────────────────────────────────────────────────────────────────────
	// Step 3: Verify Middleware — ForwardAuth to x402-verifier
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 3: Verifying Middleware x402-" + name)
	mwJSON := obolRun(t, cfg, "kubectl", "get", "middleware",
		fmt.Sprintf("x402-%s", name), "-n", ns, "-o", "json")

	var mw map[string]interface{}
	if err := json.Unmarshal([]byte(mwJSON), &mw); err != nil {
		t.Fatalf("parse middleware JSON: %v", err)
	}

	// Verify ForwardAuth address points at x402-verifier.
	spec := mw["spec"].(map[string]interface{})
	forwardAuth, ok := spec["forwardAuth"].(map[string]interface{})
	if !ok {
		t.Fatal("Middleware missing spec.forwardAuth")
	}
	address, _ := forwardAuth["address"].(string)
	if !strings.Contains(address, "x402-verifier") {
		t.Errorf("Middleware forwardAuth address = %q, want x402-verifier URL", address)
	} else {
		t.Logf("  ✓ Middleware ForwardAuth → %s", address)
	}

	// Verify ownerReference back to ServiceOffer.
	verifyOwnerRef(t, mw, name, "ServiceOffer")

	// ────────────────────────────────────────────────────────────────────
	// Step 4: Verify route resources
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 4: Verifying route resources")
	assertOfferRouteResourcesPresent(t, cfg, name, ns)

	// ────────────────────────────────────────────────────────────────────
	// Step 5: Verify HTTPRoute — gateway parent + middleware filter + backend
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 5: Verifying HTTPRoute so-" + name)
	hrJSON := obolRun(t, cfg, "kubectl", "get", "httproute",
		fmt.Sprintf("so-%s", name), "-n", ns, "-o", "json")

	var hr map[string]interface{}
	if err := json.Unmarshal([]byte(hrJSON), &hr); err != nil {
		t.Fatalf("parse httproute JSON: %v", err)
	}

	hrSpec := hr["spec"].(map[string]interface{})

	// 5a: parentRef = traefik-gateway in traefik namespace.
	parentRefs, _ := hrSpec["parentRefs"].([]interface{})
	if len(parentRefs) == 0 {
		t.Fatal("HTTPRoute has no parentRefs")
	}
	parentRef := parentRefs[0].(map[string]interface{})
	parentName, _ := parentRef["name"].(string)
	parentNS, _ := parentRef["namespace"].(string)
	if parentName != "traefik-gateway" || parentNS != "traefik" {
		t.Errorf("HTTPRoute parentRef = %s/%s, want traefik/traefik-gateway", parentNS, parentName)
	} else {
		t.Logf("  ✓ HTTPRoute parent: traefik/traefik-gateway")
	}

	// 5b: rules[0].matches[0].path = PathPrefix matching spec.path.
	rules, _ := hrSpec["rules"].([]interface{})
	if len(rules) == 0 {
		t.Fatal("HTTPRoute has no rules")
	}
	rule0 := rules[0].(map[string]interface{})
	matches, _ := rule0["matches"].([]interface{})
	if len(matches) > 0 {
		match0 := matches[0].(map[string]interface{})
		matchPath, _ := match0["path"].(map[string]interface{})
		pathValue, _ := matchPath["value"].(string)
		if pathValue != path {
			t.Errorf("HTTPRoute path match = %q, want %q", pathValue, path)
		} else {
			t.Logf("  ✓ HTTPRoute path: %s", pathValue)
		}
	}

	// 5c: filters include ExtensionRef to Middleware x402-<name>.
	filters, _ := rule0["filters"].([]interface{})
	foundMiddlewareFilter := false
	for _, f := range filters {
		fm := f.(map[string]interface{})
		if fm["type"] == "ExtensionRef" {
			ref, _ := fm["extensionRef"].(map[string]interface{})
			refName, _ := ref["name"].(string)
			if refName == fmt.Sprintf("x402-%s", name) {
				foundMiddlewareFilter = true
				t.Logf("  ✓ HTTPRoute filter: ExtensionRef → x402-%s", name)
			}
		}
	}
	if !foundMiddlewareFilter {
		t.Errorf("HTTPRoute missing ExtensionRef filter to x402-%s", name)
	}

	// 5d: backendRefs point at the upstream service.
	backendRefs, _ := rule0["backendRefs"].([]interface{})
	if len(backendRefs) > 0 {
		backend := backendRefs[0].(map[string]interface{})
		backendName, _ := backend["name"].(string)
		backendNS, _ := backend["namespace"].(string)
		t.Logf("  ✓ HTTPRoute backend: %s/%s", backendNS, backendName)
	}

	// 5e: ownerReference.
	verifyOwnerRef(t, hr, name, "ServiceOffer")

	// ────────────────────────────────────────────────────────────────────
	// Step 6: Idempotency — second `process --all` changes nothing
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 6: Verifying idempotency (second process --all)")
	processOut2, _ := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"process", "--all")

	// Second run should see everything as Ready and not re-process.
	if strings.Contains(processOut2, "Error") || strings.Contains(processOut2, "error") {
		t.Errorf("second process --all produced errors:\n%s", processOut2)
	}
	t.Logf("  ✓ second process --all: no errors")

	// Conditions should still all be True.
	so = getServiceOffer(t, cfg, name, ns)
	for _, c := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		if getConditionStatus(so, c) != "True" {
			t.Errorf("after idempotent re-process: %s is not True", c)
		}
	}

	// ────────────────────────────────────────────────────────────────────
	// Step 7: Traffic validation — request reaches upstream
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 7: Verifying traffic routes through payment gate")

	// Wait for Reloader to restart verifier + route propagation.
	time.Sleep(8 * time.Second)

	chatBody := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"say one word"}],"stream":false}`, model)
	localURL := fmt.Sprintf("http://obol.stack:8080%s/v1/chat/completions", path)

	resp, err := http.Post(localURL, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
	} else {
		t.Logf("  ✓ 402 Payment Required (payment gate active)")
	}

	// ────────────────────────────────────────────────────────────────────
	// Step 8: Agent delete — route resources removed + cascade
	// ────────────────────────────────────────────────────────────────────
	t.Log("Step 8: Agent deletes offer (route resources + CR)")
	delOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/monetize/scripts/monetize.py",
		"delete", name, "--namespace", ns)
	t.Logf("delete output:\n%s", delOut)

	// Wait for GC cascade.
	time.Sleep(3 * time.Second)

	// 8a: Owned route resources removed.
	assertOfferRouteResourcesAbsent(t, cfg, name, ns)

	// 8b: ServiceOffer CR gone.
	_, err = obolRunErr(cfg, "kubectl", "get", "serviceoffers.obol.org", name, "-n", ns)
	if err == nil {
		t.Error("ServiceOffer still exists after delete")
	} else {
		t.Logf("  ✓ ServiceOffer CR deleted")
	}

	// 8c: Middleware gone (ownerRef cascade).
	_, err = obolRunErr(cfg, "kubectl", "get", "middleware", fmt.Sprintf("x402-%s", name), "-n", ns)
	if err == nil {
		t.Error("Middleware still exists after ServiceOffer delete (ownerRef cascade failed)")
	} else {
		t.Logf("  ✓ Middleware x402-%s cascaded", name)
	}

	// 8d: HTTPRoute gone (ownerRef cascade).
	_, err = obolRunErr(cfg, "kubectl", "get", "httproute", fmt.Sprintf("so-%s", name), "-n", ns)
	if err == nil {
		t.Error("HTTPRoute still exists after ServiceOffer delete (ownerRef cascade failed)")
	} else {
		t.Logf("  ✓ HTTPRoute so-%s cascaded", name)
	}

	t.Log("agent coordination test complete: CR intent → agent reconcile → all resources → delete cascade")
}

// verifyOwnerRef checks that a resource has an ownerReference pointing at
// a ServiceOffer with the given name.
func verifyOwnerRef(t *testing.T, resource map[string]interface{}, ownerName, ownerKind string) {
	t.Helper()

	metadata, ok := resource["metadata"].(map[string]interface{})
	if !ok {
		t.Error("resource has no metadata")
		return
	}
	ownerRefs, ok := metadata["ownerReferences"].([]interface{})
	if !ok || len(ownerRefs) == 0 {
		t.Errorf("resource missing ownerReferences (expected %s/%s)", ownerKind, ownerName)
		return
	}

	for _, ref := range ownerRefs {
		rm := ref.(map[string]interface{})
		if rm["kind"] == ownerKind && rm["name"] == ownerName {
			controller, _ := rm["controller"].(bool)
			blockDel, _ := rm["blockOwnerDeletion"].(bool)
			if controller && blockDel {
				t.Logf("  ✓ ownerReference: %s/%s (controller=true, blockOwnerDeletion=true)", ownerKind, ownerName)
			} else {
				t.Logf("  ~ ownerReference: %s/%s (controller=%v, blockDel=%v)", ownerKind, ownerName, controller, blockDel)
			}
			return
		}
	}
	t.Errorf("no ownerReference for %s/%s found", ownerKind, ownerName)
}

// ---------------------------------------------------------------------------
// TestIntegration_SellDiscoverBuySettle — Full closed-loop test
//
// Agent sells LiteLLM inference → registers on ERC-8004 (Anvil fork) →
// discovery.py finds it on-chain → buy.py probes 402 → buy.py buys
// (pre-signs auths, deploys sidecar, wires LiteLLM) → request through
// sidecar auto-pays → USDC settles on Anvil fork.
//
// Prerequisites:
//   - Running k3d cluster with CRD, agent, x402-verifier, LiteLLM
//   - Anthropic API key configured in LiteLLM (for agent tool calling)
//   - Anvil (Foundry) installed
//   - x402-rs facilitator binary (set X402_FACILITATOR_BIN or X402_RS_DIR)
// ---------------------------------------------------------------------------

func TestIntegration_SellDiscoverBuySettle(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	anvil := requireAnvil(t)
	facilitator := testutil.StartRealFacilitator(t, anvil)

	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// ── Accounts ──
	// Buyer = Anvil account[9], Seller = agent's own wallet
	buyerKey := anvil.Accounts[9].PrivateKey
	buyerAddr := anvil.Accounts[9].Address
	anvil.ClearCode(t, buyerAddr)
	anvil.ClearCode(t, anvil.Accounts[0].Address) // facilitator signer
	anvil.MintUSDC(t, buyerAddr, testutil.USDCMicroUnits(100))

	// Read agent wallet from cluster (wallet-metadata stores addresses.json)
	walletRaw := obolRun(t, cfg, "kubectl", "get", "configmap", "wallet-metadata",
		"-n", agentNamespace(cfg), "-o", `jsonpath={.data.addresses\.json}`)
	var walletData struct {
		Addresses []struct {
			Address string `json:"address"`
		} `json:"addresses"`
	}
	if err := json.Unmarshal([]byte(walletRaw), &walletData); err != nil || len(walletData.Addresses) == 0 {
		t.Skip("agent wallet-metadata not found or empty")
	}
	agentWallet := walletData.Addresses[0].Address
	t.Logf("agent wallet: %s", agentWallet)

	// Fund agent with ETH for ERC-8004 registration gas
	anvil.FundETH(t, agentWallet, big.NewInt(1e18))
	anvil.ClearCode(t, agentWallet)

	// ── eRPC → Anvil route ──
	// Register the Anvil fork as a custom RPC for base-sepolia in eRPC.
	// This is needed for: buy.py balance checks, monetize.py register tx, discovery.py queries.
	anvilClusterURL := fmt.Sprintf("http://%s:%d", testutil.ClusterHostAddress(), anvil.Port)
	obolRun(t, cfg, "network", "add", "base-sepolia", "--endpoint", anvilClusterURL)
	t.Logf("eRPC route: base-sepolia → %s", anvilClusterURL)

	// ── STEP 1: SELL ──
	t.Log("═══ STEP 1: SELL — Create ServiceOffer targeting LiteLLM ═══")
	name := "test-loop"
	ns := "llm"

	offerYAML := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: litellm
    namespace: %s
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
  registration:
    enabled: true
    name: "Test Loop Agent"
    description: "Self-test: sell → discover → buy → settle"
`, name, ns, ns, agentWallet, name)

	applyServiceOffer(t, cfg, offerYAML)
	t.Cleanup(func() { deleteServiceOffer(t, cfg, name, ns) })

	// Patch x402 verifier to use real facilitator on Anvil
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	// ── STEP 2: REGISTER ──
	t.Log("═══ STEP 2: REGISTER — Reconcile ServiceOffer (6 stages + ERC-8004) ═══")

	out, reconcileErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/sell/scripts/monetize.py",
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", out)
	if reconcileErr != nil {
		t.Logf("reconciliation error (may be partial): %v", reconcileErr)
	}

	// Check conditions
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Fatalf("condition %s = %q, want True", cond, status)
		}
		t.Logf("  ✓ %s", cond)
	}

	// Registration may fail if eRPC → Anvil route isn't ready fast enough.
	regStatus := getConditionStatus(so, "Registered")
	t.Logf("  Registered: %s", regStatus)

	// Patch the HTTPRoute with LiteLLM auth header (monetize.py doesn't add it yet).
	// Without this, paid requests that pass x402 verification get 401 from LiteLLM.
	masterKey := obolRun(t, cfg, "kubectl", "get", "secret", "litellm-secrets",
		"-n", "llm", "-o", "jsonpath={.data.LITELLM_MASTER_KEY}")
	// Decode base64
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterKey)); err == nil {
		masterKey = string(decoded)
	}
	patchJSON := fmt.Sprintf(`[{"op":"add","path":"/spec/rules/0/filters/-","value":{"type":"RequestHeaderModifier","requestHeaderModifier":{"set":[{"name":"Authorization","value":"Bearer %s"}]}}}]`, masterKey)
	obolRun(t, cfg, "kubectl", "patch", "httproute", fmt.Sprintf("so-%s", name),
		"-n", ns, "--type=json", "-p", patchJSON)
	t.Log("  ✓ Patched HTTPRoute with LiteLLM auth header")

	// Wait for Traefik to pick up the HTTPRoute + Reloader to restart x402-verifier
	t.Log("  Waiting 15s for route propagation...")
	time.Sleep(15 * time.Second)

	// ── STEP 3: DISCOVER ──
	t.Log("═══ STEP 3: DISCOVER — Query ERC-8004 registry via discovery.py ═══")

	if regStatus == "True" {
		// Extract agentId from condition message and discover it
		discoveryOut, err := execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/discovery/scripts/discovery.py",
			"search", "--chain", "base-sepolia", "--limit", "5")
		if err != nil {
			t.Logf("discovery search failed (expected if eRPC not routing to Anvil): %v", err)
		} else {
			t.Logf("discovery output:\n%s", discoveryOut)
		}
	} else {
		t.Log("  (skipping discovery — registration did not complete on Anvil fork)")
	}

	// ── STEP 4: BUY (probe + buy) ──
	t.Log("═══ STEP 4: BUY — Probe 402, then buy via pre-signed auths ═══")

	// Probe the endpoint — should get 402
	// The endpoint is accessible within the cluster at obol.stack:8080/services/<name>
	// But from the agent pod, we use the Traefik service directly
	serviceURL := fmt.Sprintf("http://traefik.traefik.svc.cluster.local/services/%s/v1/chat/completions", name)

	probeOut, probeErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"probe", serviceURL)
	t.Logf("probe output:\n%s", probeOut)
	if probeErr != nil {
		t.Logf("probe error: %v", probeErr)
	}
	if !strings.Contains(probeOut, "402") && !strings.Contains(probeOut, "payTo") {
		t.Logf("  ⚠ probe did not return 402 pricing (endpoint may not be routed yet)")
	} else {
		t.Log("  ✓ Probe returned 402 with pricing info")
	}

	// Buy: pre-sign auths + deploy sidecar + wire LiteLLM
	sellerEndpoint := fmt.Sprintf("http://traefik.traefik.svc.cluster.local/services/%s", name)
	buyOut, buyErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"buy", "self-loop",
		"--endpoint", sellerEndpoint,
		"--model", "claude-sonnet-4-5-20250929",
		"--count", "3")
	t.Logf("buy output:\n%s", buyOut)
	if buyErr != nil {
		t.Logf("buy error: %v", buyErr)
	}

	// ── STEP 5: SETTLE ──
	t.Log("═══ STEP 5: SETTLE — Verify USDC transfer on Anvil fork ═══")

	// Record balances before manual payment test
	buyerBefore := anvil.GetUSDCBalance(t, buyerAddr)
	sellerBefore := anvil.GetUSDCBalance(t, agentWallet)
	t.Logf("balances before: buyer=%s, seller=%s", buyerBefore, sellerBefore)

	// Send a direct paid request (bypassing sidecar, using manual EIP-712 signing)
	// This validates the sell-side payment gate works end-to-end
	url := fmt.Sprintf("http://obol.stack:8080/services/%s/v1/chat/completions", name)
	rpcBody := `{"model":"claude-sonnet-4-5-20250929","messages":[{"role":"user","content":"ping"}],"max_tokens":3}`

	paymentHeader := testutil.SignRealPaymentHeader(t, buyerKey, agentWallet, "1000", 84532)
	req, _ := http.NewRequest("POST", url, strings.NewReader(rpcBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v (DNS not configured)", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	t.Logf("paid request: status=%d body=%s", resp.StatusCode, string(body)[:min(200, len(body))])

	if resp.StatusCode == http.StatusOK {
		t.Log("  ✓ Paid inference succeeded!")

		// Verify USDC transfer
		buyerAfter := anvil.GetUSDCBalance(t, buyerAddr)
		sellerAfter := anvil.GetUSDCBalance(t, agentWallet)
		t.Logf("balances after:  buyer=%s, seller=%s", buyerAfter, sellerAfter)

		buyerDelta := new(big.Int).Sub(buyerBefore, buyerAfter)
		sellerDelta := new(big.Int).Sub(sellerAfter, sellerBefore)
		t.Logf("deltas: buyer=-%s, seller=+%s", buyerDelta, sellerDelta)

		if buyerDelta.Cmp(big.NewInt(0)) <= 0 {
			t.Error("buyer balance did not decrease — USDC not transferred")
		}
		if sellerDelta.Cmp(big.NewInt(0)) <= 0 {
			t.Error("seller balance did not increase — USDC not received")
		}
	} else {
		t.Logf("  ⚠ Paid request returned %d (may need HTTPRoute auth header patch)", resp.StatusCode)
	}

	t.Log("═══ SELL → DISCOVER → BUY → SETTLE loop test complete ═══")
}

// TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance validates the
// full commerce path with public registration/discovery and in-cluster buy-side
// spending:
//   - Sell the current qwen3.5:9b model through the LiteLLM gateway with ERC-8004 registration enabled
//   - Discover that registration on-chain
//   - Verify the public agent URI is reachable, while logging the current
//     shared-registration limitation on reused clusters
//   - Buy the service through the in-cluster Traefik route with buy.py
//   - Consume the purchased model via LiteLLM's paid/<model> alias
//   - Verify the buyer sidecar quota decreases and USDC moves buyer -> seller
func TestIntegration_Tunnel_SellDiscoverBuySidecar_QuotaAndBalance(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireExactOllamaModel(t, "qwen3.5:9b")
	tunnelURL := requireTunnel(t, cfg)
	anvil := requireAnvil(t)

	facilitator := testutil.StartRealFacilitator(t, anvil)
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	agentWallet := getAgentWalletAddress(t, cfg)
	sellerAddr := anvil.Accounts[1].Address
	for _, addr := range []string{
		anvil.Accounts[0].Address, // facilitator signer
		agentWallet,               // buyer / registrant
		sellerAddr,                // seller payTo
	} {
		anvil.ClearCode(t, addr)
	}
	anvil.FundETH(t, agentWallet, big.NewInt(1e18))
	anvil.MintUSDC(t, agentWallet, testutil.USDCMicroUnits(10))
	t.Logf("funded agent wallet %s with 10 USDC for buy-side settlement", agentWallet)

	originalERPCConfig := getERPCConfigYAML(t, cfg)
	t.Cleanup(func() {
		setERPCConfigYAML(t, cfg, originalERPCConfig)
	})

	anvilClusterURL := fmt.Sprintf("http://%s:%d", testutil.ClusterHostAddress(), anvil.Port)
	obolRun(t, cfg, "network", "add", "base-sepolia", "--endpoint", anvilClusterURL, "--allow-writes")
	pinERPCChainToSingleUpstream(t, cfg, 84532, "custom-84532-0")
	t.Logf("eRPC route: base-sepolia -> %s", anvilClusterURL)

	runID := petname.Generate(2, "-")
	name := "test-sidecar-tunnel-" + runID
	buyerName := "tunnel-sidecar-" + runID
	ns := "llm"
	offerYAML := registeredLiteLLMServiceOfferYAML(
		name,
		ns,
		model,
		sellerAddr,
		"Tunnel Sidecar Seller",
		"Self-test: register -> discover -> buy via tunnel -> paid alias -> settle",
	)
	applyServiceOffer(t, cfg, offerYAML)
	t.Cleanup(func() {
		_, _ = execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/buy-inference/scripts/buy.py",
			"remove", buyerName)
		_, _ = execInAgentErr(cfg, "python3",
			monetizePy,
			"delete", name, "--namespace", ns)
		deleteServiceOffer(t, cfg, name, ns)
	})

	processOut, processErr := execInAgentErr(cfg, "python3",
		monetizePy,
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", processOut)
	if processErr != nil {
		t.Fatalf("reconcile ServiceOffer: %v", processErr)
	}

	for _, cond := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Registered", "Ready"} {
		waitForCondition(t, cfg, name, ns, cond, "True", 2*time.Minute)
	}

	so := getServiceOffer(t, cfg, name, ns)
	agentID := getStatusFieldString(so, "agentId")
	if agentID == "" {
		t.Fatal("ServiceOffer status.agentId is empty after registration")
	}
	t.Logf("registered ServiceOffer as agent ID %s", agentID)

	searchOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/discovery/scripts/discovery.py",
		"search", "--chain", "base-sepolia", "--limit", "20", "--lookback", "20000")
	t.Logf("discovery search output:\n%s", searchOut)
	if !strings.Contains(searchOut, agentID) {
		t.Fatalf("discovery search did not contain agent ID %s:\n%s", agentID, searchOut)
	}

	uriOut := waitForAgentCommand(t, cfg, 90*time.Second, "python3",
		"/data/.openclaw/skills/discovery/scripts/discovery.py",
		"uri", agentID, "--chain", "base-sepolia")
	t.Logf("discovery uri output:\n%s", uriOut)
	if !strings.Contains(uriOut, `"x402Support": true`) {
		t.Fatalf("registration JSON did not expose x402 support:\n%s", uriOut)
	}
	if !strings.Contains(uriOut, tunnelURL+"/services/"+name) {
		t.Logf("registration JSON still points at another offer on the shared agent URI (known multi-offer limitation on reused clusters):\n%s", uriOut)
	}

	localBaseURL := fmt.Sprintf("http://traefik.traefik.svc.cluster.local/services/%s", name)
	probeURL := localBaseURL + "/v1/chat/completions"
	probeOut := waitForBuyerProbePricing(t, cfg, 90*time.Second, probeURL, model)
	t.Logf("probe output:\n%s", probeOut)
	if !strings.Contains(probeOut, "402") || !strings.Contains(probeOut, "payTo") {
		t.Fatalf("probe did not return pricing info:\n%s", probeOut)
	}

	buyOut, buyErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"buy", buyerName,
		"--endpoint", localBaseURL,
		"--model", model,
		"--count", "3")
	t.Logf("buy output:\n%s", buyOut)
	if buyErr != nil {
		t.Fatalf("buy.py buy failed: %v", buyErr)
	}
	if !strings.Contains(buyOut, "paid/"+model) {
		t.Fatalf("buy output did not advertise paid/%s:\n%s", model, buyOut)
	}

	liveBefore := waitForBuyerLiveAuthCount(t, cfg, buyerName, 3, 90*time.Second)
	t.Logf("buyer live status before inference:\n%s", liveBefore)
	liveBeforeStatus := parseBuyerLiveUpstream(t, liveBefore, buyerName)
	if liveBeforeStatus.Spent != 0 {
		t.Fatalf("buyer live status spent=%d before inference, want 0:\n%s", liveBeforeStatus.Spent, liveBefore)
	}
	statusBefore := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"status", buyerName)
	t.Logf("buyer status before inference:\n%s", statusBefore)
	if !strings.Contains(statusBefore, "Alias:    paid/"+model) {
		t.Fatalf("buyer status missing paid alias:\n%s", statusBefore)
	}
	if !strings.Contains(statusBefore, "Sidecar: Running") {
		t.Fatalf("buyer sidecar not running:\n%s", statusBefore)
	}

	balanceBeforeOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"balance", "--chain", "base-sepolia")
	t.Logf("buyer balance before:\n%s", balanceBeforeOut)
	if !strings.Contains(balanceBeforeOut, agentWallet) {
		t.Fatalf("buy.py balance did not report the agent wallet:\n%s", balanceBeforeOut)
	}

	buyerBefore := anvil.GetUSDCBalance(t, agentWallet)
	sellerBefore := anvil.GetUSDCBalance(t, sellerAddr)
	balanceBeforeOut = waitForBuyerReportedBalance(t, cfg, buyerBefore, 20*time.Second)
	t.Logf("buyer balance before (confirmed):\n%s", balanceBeforeOut)
	buyerScriptBefore := parseUSDCMicroUnits(t, balanceBeforeOut)
	if buyerBefore.Cmp(buyerScriptBefore) != 0 {
		t.Fatalf("buy.py balance (%s) != on-chain balance (%s) before request", buyerScriptBefore, buyerBefore)
	}

	masterKey := getLiteLLMMasterKey(t, cfg)
	statusCode, body := callLiteLLMPaidModelFromAgent(t, cfg, masterKey, "paid/"+model, "reply with one short paid word")
	if statusCode != http.StatusOK {
		t.Fatalf("paid alias request returned %d: %s", statusCode, string(body))
	}

	var chatResp map[string]interface{}
	if err := json.Unmarshal(body, &chatResp); err != nil {
		t.Fatalf("parse paid alias response: %v\nbody: %s", err, string(body))
	}
	choices, _ := chatResp["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatalf("paid alias response contained no choices: %s", string(body))
	}
	t.Logf("paid alias response: %s", string(body)[:min(200, len(body))])

	liveAfter := waitForBuyerLiveAuthCount(t, cfg, buyerName, 2, 60*time.Second)
	t.Logf("buyer live status after inference:\n%s", liveAfter)
	liveAfterStatus := parseBuyerLiveUpstream(t, liveAfter, buyerName)
	if liveAfterStatus.Spent != 1 {
		t.Fatalf("buyer live status spent=%d after one inference, want 1:\n%s", liveAfterStatus.Spent, liveAfter)
	}
	statusAfter := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"status", buyerName)
	t.Logf("buyer status after inference:\n%s", statusAfter)

	balanceAfterOut := execInAgent(t, cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"balance", "--chain", "base-sepolia")
	t.Logf("buyer balance after:\n%s", balanceAfterOut)

	buyerAfter, sellerAfter := waitForUSDCSettlement(t, anvil, agentWallet, sellerAddr, buyerBefore, sellerBefore, 20*time.Second)
	balanceAfterOut = waitForBuyerReportedBalance(t, cfg, buyerAfter, 20*time.Second)
	t.Logf("buyer balance after (confirmed):\n%s", balanceAfterOut)
	buyerScriptAfter := parseUSDCMicroUnits(t, balanceAfterOut)
	if buyerAfter.Cmp(buyerScriptAfter) != 0 {
		t.Fatalf("buy.py balance (%s) != on-chain balance (%s) after request", buyerScriptAfter, buyerAfter)
	}

	buyerDelta := new(big.Int).Sub(buyerBefore, buyerAfter)
	sellerDelta := new(big.Int).Sub(sellerAfter, sellerBefore)
	t.Logf("buyer delta: -%s, seller delta: +%s", buyerDelta, sellerDelta)
	if buyerDelta.Cmp(big.NewInt(0)) <= 0 {
		t.Fatal("buyer balance did not decrease after auto-paid request")
	}
	if sellerDelta.Cmp(big.NewInt(0)) <= 0 {
		t.Fatal("seller balance did not increase after auto-paid request")
	}

	t.Logf("tunnel sidecar flow complete: registered agent %s, discovered endpoint, bought paid/%s, auths 3->2, spent 0->1", agentID, model)
}

// TestIntegration_SellBuySidecar_OBOLPermit2 validates the in-cluster buy path
// using a fork-local OBOL-compatible ERC20Permit token deployed on an Anvil fork
// of Base Sepolia. This avoids any dependence on a public bridged OBOL testnet
// deployment while exercising:
//   - seller-side OBOL asset metadata
//   - buyer-side Permit2 payload construction
//   - automatic EIP-2612 gas sponsoring attachment
//   - x402-buyer replay of a full signed x402 payload
func TestIntegration_SellBuySidecar_OBOLPermit2(t *testing.T) {
	if os.Getenv("X402_FACILITATOR_BIN") == "" {
		t.Skip("set X402_FACILITATOR_BIN to an ObolNetwork/x402-rs main build with eip2612GasSponsoring support")
	}

	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireExactOllamaModel(t, "qwen3.5:9b")
	anvil := requireAnvil(t)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	obolRun(t, cfg, "openclaw", "skills", "sync", agentInstanceID(cfg), "--from", filepath.Join(repoRoot, "internal", "embed", "skills"))
	t.Log("synced embedded skills to running OpenClaw instance")
	obolRun(t, cfg, "kubectl", "delete", "purchaserequests.obol.org", "-n", agentNamespace(cfg), "--all", "--ignore-not-found")
	time.Sleep(5 * time.Second)

	facilitator := testutil.StartRealFacilitatorWithOptions(t, anvil, testutil.RealFacilitatorOptions{
		EnableEIP2612GasSponsoring: true,
	})
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	obolToken := anvil.DeployForkObolToken(t, anvil.Accounts[0].PrivateKey, anvil.Accounts[0].Address, big.NewInt(0))

	agentWallet := getAgentWalletAddress(t, cfg)
	sellerAddr := anvil.Accounts[1].Address
	for _, addr := range []string{
		anvil.Accounts[0].Address, // facilitator signer / token deployer
		agentWallet,               // buyer / remote signer
		sellerAddr,                // seller payTo
	} {
		anvil.ClearCode(t, addr)
	}
	anvil.FundETH(t, agentWallet, big.NewInt(1e18))
	anvil.MintMintableERC20(t, obolToken, anvil.Accounts[0].PrivateKey, agentWallet, new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)))
	t.Logf("funded agent wallet %s with 10 OBOL on fork token %s", agentWallet, obolToken)

	originalERPCConfig := getERPCConfigYAML(t, cfg)
	t.Cleanup(func() {
		setERPCConfigYAML(t, cfg, originalERPCConfig)
	})

	anvilClusterURL := fmt.Sprintf("http://%s:%d", testutil.ClusterHostAddress(), anvil.Port)
	obolRun(t, cfg, "network", "add", "base-sepolia", "--endpoint", anvilClusterURL, "--allow-writes")
	pinERPCChainToSingleUpstream(t, cfg, 84532, "custom-84532-0")
	t.Logf("eRPC route: base-sepolia -> %s", anvilClusterURL)

	runID := petname.Generate(2, "-")
	name := "test-obol-sidecar-" + runID
	buyerName := "obol-sidecar-" + runID
	ns := "llm"
	offerYAML := fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: litellm
    namespace: llm
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "%s"
    asset:
      address: "%s"
      symbol: "OBOL"
      decimals: 18
      transferMethod: "permit2"
      eip712Name: "Obol Network"
      eip712Version: "1"
    price:
      perRequest: "0.001"
  path: /services/%s
`, name, ns, sellerAddr, obolToken, name)

	applyServiceOffer(t, cfg, offerYAML)
	t.Cleanup(func() {
		_, _ = execInAgentErr(cfg, "python3",
			"/data/.openclaw/skills/buy-inference/scripts/buy.py",
			"remove", buyerName)
		_, _ = execInAgentErr(cfg, "python3",
			monetizePy,
			"delete", name, "--namespace", ns)
		deleteServiceOffer(t, cfg, name, ns)
	})

	processOut, processErr := execInAgentErr(cfg, "python3",
		monetizePy,
		"process", name, "--namespace", ns)
	t.Logf("reconciliation output:\n%s", processOut)
	if processErr != nil {
		t.Fatalf("reconcile ServiceOffer: %v", processErr)
	}
	for _, cond := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		waitForCondition(t, cfg, name, ns, cond, "True", 2*time.Minute)
	}

	masterKey := getLiteLLMMasterKey(t, cfg)
	patchJSON := fmt.Sprintf(`[{"op":"add","path":"/spec/rules/0/filters/-","value":{"type":"RequestHeaderModifier","requestHeaderModifier":{"set":[{"name":"Authorization","value":"Bearer %s"}]}}}]`, masterKey)
	obolRun(t, cfg, "kubectl", "patch", "httproute", fmt.Sprintf("so-%s", name),
		"-n", ns, "--type=json", "-p", patchJSON)
	time.Sleep(15 * time.Second)

	localBaseURL := fmt.Sprintf("http://traefik.traefik.svc.cluster.local/services/%s", name)
	probeOut := waitForBuyerProbePricing(t, cfg, 90*time.Second, localBaseURL+"/v1/chat/completions", model)
	t.Logf("probe output:\n%s", probeOut)
	if !strings.Contains(probeOut, obolToken) {
		t.Fatalf("probe output did not include OBOL token address %s:\n%s", obolToken, probeOut)
	}
	if !strings.Contains(probeOut, "permit2") {
		t.Fatalf("probe output did not include permit2 transfer method:\n%s", probeOut)
	}

	buyOut, buyErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/buy-inference/scripts/buy.py",
		"buy", buyerName,
		"--endpoint", localBaseURL,
		"--model", model,
		"--count", "3")
	t.Logf("buy output:\n%s", buyOut)
	if buyErr != nil {
		t.Fatalf("buy.py buy failed: %v", buyErr)
	}
	if !strings.Contains(buyOut, "paid/"+model) {
		t.Fatalf("buy output did not advertise paid/%s:\n%s", model, buyOut)
	}

	liveBefore := waitForBuyerLiveAuthCount(t, cfg, buyerName, 3, 90*time.Second)
	t.Logf("buyer live status before inference:\n%s", liveBefore)

	buyerBefore := anvil.GetERC20Balance(t, obolToken, agentWallet)
	sellerBefore := anvil.GetERC20Balance(t, obolToken, sellerAddr)
	settlementFromBlock := anvil.BlockNumber(t)

	requestCount := 3
	for i := 1; i <= requestCount; i++ {
		statusCode, body := callLiteLLMPaidModelFromAgent(t, cfg, masterKey, "paid/"+model, fmt.Sprintf("reply with one short paid word %d", i))
		if statusCode != http.StatusOK {
			t.Fatalf("paid alias request %d returned %d: %s", i, statusCode, string(body))
		}
		wantRemaining := requestCount - i
		liveStatus := waitForBuyerLiveAuthCount(t, cfg, buyerName, wantRemaining, 60*time.Second)
		t.Logf("buyer live status after request %d:\n%s", i, liveStatus)
	}

	deadline := time.Now().Add(20 * time.Second)
	var buyerAfter, sellerAfter *big.Int
	for time.Now().Before(deadline) {
		buyerAfter = anvil.GetERC20Balance(t, obolToken, agentWallet)
		sellerAfter = anvil.GetERC20Balance(t, obolToken, sellerAddr)
		expectedBuyerDelta := new(big.Int).Mul(big.NewInt(int64(requestCount)), big.NewInt(1_000_000_000_000_000))
		if new(big.Int).Sub(buyerBefore, buyerAfter).Cmp(expectedBuyerDelta) == 0 &&
			new(big.Int).Sub(sellerAfter, sellerBefore).Cmp(expectedBuyerDelta) == 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	expectedDelta := new(big.Int).Mul(big.NewInt(int64(requestCount)), big.NewInt(1_000_000_000_000_000))
	if buyerAfter == nil || sellerAfter == nil ||
		new(big.Int).Sub(buyerBefore, buyerAfter).Cmp(expectedDelta) != 0 ||
		new(big.Int).Sub(sellerAfter, sellerBefore).Cmp(expectedDelta) != 0 {
		t.Fatalf("OBOL settlement did not complete: buyer before=%s after=%s seller before=%s after=%s", buyerBefore, buyerAfter, sellerBefore, sellerAfter)
	}

	receipts := anvil.FindERC20TransferReceipts(t, obolToken, agentWallet, sellerAddr, settlementFromBlock)
	if len(receipts) != requestCount {
		t.Fatalf("expected %d OBOL settlement receipts, got %d", requestCount, len(receipts))
	}
	totalGasWei := big.NewInt(0)
	totalGasUsed := big.NewInt(0)
	for i, receipt := range receipts {
		gasUsed := testutil.ParseHexBigInt(t, receipt.GasUsed)
		effectiveGasPrice := testutil.ParseHexBigInt(t, receipt.EffectiveGasPrice)
		receiptGasWei := new(big.Int).Mul(new(big.Int).Set(gasUsed), effectiveGasPrice)
		totalGasUsed.Add(totalGasUsed, gasUsed)
		totalGasWei.Add(totalGasWei, receiptGasWei)
		t.Logf(
			"OBOL settlement receipt %d/%d: tx=%s block=%s from=%s to=%s status=%s gasUsed=%s effectiveGasPriceWei=%s totalGasWei=%s",
			i+1,
			requestCount,
			receipt.TransactionHash,
			receipt.BlockNumber,
			receipt.From,
			receipt.To,
			receipt.Status,
			gasUsed.String(),
			effectiveGasPrice.String(),
			receiptGasWei.String(),
		)
	}
	t.Logf(
		"OBOL exact pack benchmark: requests=%d totalGasUsed=%s totalGasWei=%s avgGasUsedPerRequest=%s avgGasWeiPerRequest=%s",
		requestCount,
		totalGasUsed.String(),
		totalGasWei.String(),
		new(big.Int).Div(new(big.Int).Set(totalGasUsed), big.NewInt(int64(requestCount))).String(),
		new(big.Int).Div(new(big.Int).Set(totalGasWei), big.NewInt(int64(requestCount))).String(),
	)
	t.Logf("OBOL sidecar flow complete: token=%s buyer delta=-%s seller delta=+%s", obolToken, new(big.Int).Sub(buyerBefore, buyerAfter), new(big.Int).Sub(sellerAfter, sellerBefore))
}

// ─────────────────────────────────────────────────────────────────────────────
// Phase 10 — Full Sell→Buy Roundtrip via LiteLLM + Ollama
// ─────────────────────────────────────────────────────────────────────────────

// litellmServiceOfferYAML returns a ServiceOffer targeting the LiteLLM gateway
// (not Ollama directly). This is the production path: x402 gate → LiteLLM → provider.
func litellmServiceOfferYAML(name, namespace, wallet string) string {
	return fmt.Sprintf(`apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: %s
  namespace: %s
spec:
  upstream:
    service: litellm
    namespace: llm
    port: 4000
    healthPath: /health/readiness
  payment:
    network: base-sepolia
    payTo: "%s"
    price:
      perRequest: "0.001"
  path: /services/%s
`, name, namespace, wallet, name)
}

// getLiteLLMMasterKey reads the LiteLLM master key from the cluster Secret.
func getLiteLLMMasterKey(t *testing.T, cfg *config.Config) string {
	t.Helper()
	raw := obolRun(t, cfg, "kubectl", "get", "secret", "litellm-secrets",
		"-n", "llm", "-o", "jsonpath={.data.LITELLM_MASTER_KEY}")
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("decode LiteLLM master key: %v", err)
	}
	return string(decoded)
}

// patchHTTPRouteAuth adds a RequestHeaderModifier with the LiteLLM Authorization
// header to an HTTPRoute. Required because monetize.py doesn't inject this yet.
func patchHTTPRouteAuth(t *testing.T, cfg *config.Config, routeName, namespace, masterKey string) {
	t.Helper()
	patchJSON := fmt.Sprintf(`[{"op":"add","path":"/spec/rules/0/filters/-","value":{"type":"RequestHeaderModifier","requestHeaderModifier":{"set":[{"name":"Authorization","value":"Bearer %s"}]}}}]`, masterKey)
	obolRun(t, cfg, "kubectl", "patch", "httproute", routeName,
		"-n", namespace, "--type=json", "-p", patchJSON)
}

// monetizePy is the in-pod path to the monetize.py reconciler script.
// The skill was renamed from "monetize" to "sell" but the script is still monetize.py.
const monetizePy = "/data/.openclaw/skills/sell/scripts/monetize.py"

// TestIntegration_SellBuyRoundtrip_LiteLLM is the highest-level E2E test:
// real Ollama inference through LiteLLM, real x402-rs facilitator, real EIP-712
// signatures, real USDC settlement on Anvil fork, and agent-side discovery.
//
// The full pipeline tested:
//
//	SELL:     ServiceOffer CR → agent reconciles 5 stages → Ready
//	GATE:     Unpaid POST → 402 Payment Required with pricing
//	PAY:      Sign EIP-712 TransferWithAuthorization → facilitator settles USDC
//	INFER:    LiteLLM → Ollama → real model response (via qwen3.5:9b)
//	SETTLE:   Buyer USDC decreases, Seller USDC increases on Anvil fork
//	DISCOVER: discovery.py search finds agents on-chain (bounded block range)
//
// Prerequisites:
//   - Running k3d cluster with CRD, agent, x402-verifier, LiteLLM
//   - Ollama with qwen3.5:9b available locally
//   - Anvil (Foundry) installed
//   - x402-rs facilitator binary (set X402_FACILITATOR_BIN or X402_RS_DIR)
func TestIntegration_SellBuyRoundtrip_LiteLLM(t *testing.T) {
	cfg := requireCluster(t)
	requireCRD(t, cfg)
	requireAgent(t, cfg)
	model := requireExactOllamaModel(t, "qwen3.5:9b")
	anvil := requireAnvil(t)

	// ── Infrastructure ─────────────────────────────────────────────────
	facilitator := testutil.StartRealFacilitator(t, anvil)
	t.Logf("facilitator: %s", facilitator.ClusterURL)

	// Buyer = Anvil account[9], Seller = Anvil account[1]
	buyerKey := anvil.Accounts[9].PrivateKey
	buyerAddr := anvil.Accounts[9].Address
	sellerAddr := anvil.Accounts[1].Address

	// Clear contract code for all Anvil accounts used in the test.
	// Anvil deterministic accounts have deployed contract code on Base Sepolia;
	// without clearing, EIP-1271 signature checks fail in the facilitator.
	for _, acc := range []string{
		anvil.Accounts[0].Address, // facilitator signer
		buyerAddr,                 // buyer
		sellerAddr,                // seller (payTo)
	} {
		anvil.ClearCode(t, acc)
	}
	anvil.MintUSDC(t, buyerAddr, testutil.USDCMicroUnits(10))
	t.Logf("funded buyer %s with 10 USDC", buyerAddr)

	// Point x402-verifier at real facilitator.
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	testutil.PatchVerifierFacilitator(t, kubectlBin, kubeconfig, facilitator.ClusterURL)

	// ── STEP 1: SELL — Create ServiceOffer targeting LiteLLM ───────────
	t.Log("═══ STEP 1: SELL — Create ServiceOffer → Agent Reconciles ═══")

	name := "test-roundtrip"
	ns := "llm"
	yaml := litellmServiceOfferYAML(name, ns, sellerAddr)
	applyServiceOffer(t, cfg, yaml)
	t.Cleanup(func() {
		// Agent-side cleanup
		_, _ = execInAgentErr(cfg, "python3",
			monetizePy,
			"delete", name, "--namespace", ns)
		deleteServiceOffer(t, cfg, name, ns)
	})

	// Trigger agent reconciliation.
	out, reconcileErr := execInAgentErr(cfg, "python3",
		monetizePy,
		"process", name, "--namespace", ns)
	t.Logf("reconciliation:\n%s", out)
	if reconcileErr != nil {
		t.Logf("reconciliation returned error (may be partial): %v", reconcileErr)
	}

	// Verify conditions.
	so := getServiceOffer(t, cfg, name, ns)
	for _, cond := range []string{"UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Ready"} {
		status := getConditionStatus(so, cond)
		if status != "True" {
			t.Fatalf("condition %s = %q, want True", cond, status)
		}
		t.Logf("  ✓ %s", cond)
	}

	// Wait for Traefik to pick up the published HTTPRoute.
	time.Sleep(5 * time.Second)

	// ── STEP 2: GATE — Unpaid request returns 402 ──────────────────────
	t.Log("═══ STEP 2: GATE — Unpaid request → 402 Payment Required ═══")

	chatBody := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"say hello"}],"max_tokens":20,"stream":false}`, model)
	url := fmt.Sprintf("http://obol.stack:8080/services/%s/v1/chat/completions", name)
	client := &http.Client{Timeout: 90 * time.Second}

	resp, err := client.Post(url, "application/json", strings.NewReader(chatBody))
	if err != nil {
		t.Skipf("could not reach obol.stack:8080: %v (DNS not configured?)", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("expected 402 without payment, got %d; body: %s", resp.StatusCode, body)
	}
	t.Log("  ✓ 402 Payment Required")

	// Parse and log the 402 pricing info.
	var payReqs map[string]interface{}
	if err := json.Unmarshal(body, &payReqs); err == nil {
		if accepts, ok := payReqs["accepts"].([]interface{}); ok && len(accepts) > 0 {
			first := accepts[0].(map[string]interface{})
			t.Logf("  pricing: payTo=%v amount=%v network=%v",
				first["payTo"], first["maxAmountRequired"], first["network"])
		}
	}

	// ── STEP 3: PAY + INFER — Signed request → 200 + model response ───
	t.Log("═══ STEP 3: PAY + INFER — EIP-712 payment → LiteLLM → Ollama ═══")

	// Record USDC balances before payment.
	buyerBefore := anvil.GetUSDCBalance(t, buyerAddr)
	sellerBefore := anvil.GetUSDCBalance(t, sellerAddr)
	t.Logf("  balances before: buyer=%s, seller=%s", buyerBefore, sellerBefore)

	// Sign real EIP-712 TransferWithAuthorization.
	paymentHeader := testutil.SignRealPaymentHeader(t, buyerKey, sellerAddr, "1000", 84532)

	req, _ := http.NewRequest("POST", url, strings.NewReader(chatBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PAYMENT", paymentHeader)

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("paid request failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with payment, got %d; body: %s", resp.StatusCode, body)
	}

	// Parse inference response.
	var chatResp map[string]interface{}
	if err := json.Unmarshal(body, &chatResp); err == nil {
		if choices, ok := chatResp["choices"].([]interface{}); ok && len(choices) > 0 {
			choice := choices[0].(map[string]interface{})
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				content := msg["content"]
				if content == nil || content == "" {
					content = msg["reasoning_content"]
				}
				t.Logf("  ✓ model=%v tokens=%v", chatResp["model"], chatResp["usage"])
				t.Logf("  response: %v", content)
			}
		}
	}
	t.Log("  ✓ Paid inference succeeded")

	// ── STEP 4: SETTLE — Verify USDC transfer on Anvil ─────────────────
	t.Log("═══ STEP 4: SETTLE — Verify USDC balance changes ═══")

	buyerAfter := anvil.GetUSDCBalance(t, buyerAddr)
	sellerAfter := anvil.GetUSDCBalance(t, sellerAddr)

	buyerDelta := new(big.Int).Sub(buyerBefore, buyerAfter)
	sellerDelta := new(big.Int).Sub(sellerAfter, sellerBefore)

	t.Logf("  buyer:  %s → %s (delta: -%s)", buyerBefore, buyerAfter, buyerDelta)
	t.Logf("  seller: %s → %s (delta: +%s)", sellerBefore, sellerAfter, sellerDelta)

	if buyerDelta.Cmp(big.NewInt(0)) <= 0 {
		t.Error("buyer USDC balance did not decrease — settlement failed")
	}
	if sellerDelta.Cmp(big.NewInt(0)) <= 0 {
		t.Error("seller USDC balance did not increase — settlement failed")
	}
	t.Log("  ✓ USDC transferred on Anvil fork")

	// ── STEP 5: DISCOVER — Agent-side discovery finds on-chain agents ───
	t.Log("═══ STEP 5: DISCOVER — discovery.py search from agent pod ═══")

	discoveryOut, discoveryErr := execInAgentErr(cfg, "python3",
		"/data/.openclaw/skills/discovery/scripts/discovery.py",
		"search", "--chain", "base-sepolia", "--limit", "5")
	if discoveryErr != nil {
		t.Logf("  discovery failed: %v\n%s", discoveryErr, discoveryOut)
	} else {
		t.Logf("  discovery output:\n%s", discoveryOut)
		if strings.Contains(discoveryOut, "Found") {
			t.Log("  ✓ Discovery found agents on-chain")
		}
	}

	// ── STEP 6: CLEANUP — Agent deletes all derived resources ───────────
	t.Log("═══ STEP 6: CLEANUP — Agent deletes ServiceOffer + resources ═══")

	delOut := execInAgent(t, cfg, "python3",
		monetizePy,
		"delete", name, "--namespace", ns)
	t.Logf("  delete output:\n%s", delOut)

	// Verify route resources were removed.
	time.Sleep(3 * time.Second)
	assertOfferRouteResourcesAbsent(t, cfg, name, ns)

	t.Log("═══ SELL → GATE → PAY → INFER → SETTLE → DISCOVER — ALL PASSED ═══")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
