package stack

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestCheckPortsAvailable_FreePorts(t *testing.T) {
	// Use high ephemeral ports that are almost certainly free
	ports := []int{19876, 19877}
	if err := checkPortsAvailable(ports); err != nil {
		t.Fatalf("expected no error for free ports, got: %v", err)
	}
}

func TestCheckPortsAvailable_BlockedPort(t *testing.T) {
	// Bind a port to simulate a conflict
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	// Extract the port number from the listener address
	addr := ln.Addr().(*net.TCPAddr)
	blockedPort := addr.Port

	err = checkPortsAvailable([]int{blockedPort})
	if err == nil {
		t.Fatal("expected error for blocked port, got nil")
	}

	portStr := strconv.Itoa(blockedPort)
	if !strings.Contains(err.Error(), portStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}

	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should mention 'already in use', got: %v", err)
	}

	if !strings.Contains(err.Error(), "sudo lsof") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestCheckPortsAvailable_MixedPorts(t *testing.T) {
	// Bind one port, leave another free
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	blockedPort := ln.Addr().(*net.TCPAddr).Port

	// Pick a free port by briefly binding and releasing
	ln2, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind second ephemeral port: %v", err)
	}

	freePort := ln2.Addr().(*net.TCPAddr).Port
	ln2.Close()

	err = checkPortsAvailable([]int{freePort, blockedPort})
	if err == nil {
		t.Fatal("expected error when one port is blocked, got nil")
	}

	// Should mention only the blocked port
	blockedStr := strconv.Itoa(blockedPort)
	if !strings.Contains(err.Error(), blockedStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}
}

func TestFormatPorts(t *testing.T) {
	tests := []struct {
		ports    []int
		expected string
	}{
		{[]int{443}, "443"},
		{[]int{80, 443}, "80, 443"},
		{[]int{80, 8080, 443, 8443}, "80, 8080, 443, 8443"},
	}
	for _, tt := range tests {
		got := formatPorts(tt.ports)
		if got != tt.expected {
			t.Errorf("formatPorts(%v) = %q, want %q", tt.ports, got, tt.expected)
		}
	}
}

func TestPortBlock_MatchesEmbeddedTemplate(t *testing.T) {
	// Guard: if someone reformats k3d-config.yaml, portBlock-based stripping
	// silently stops working. Verify the embedded template contains every
	// portBlock we might try to strip.
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("project root not found")
	}

	tmpl, err := os.ReadFile(filepath.Join(projectRoot, "internal/embed/k3d-config.yaml"))
	if err != nil {
		t.Fatalf("read embedded template: %v", err)
	}

	for _, ports := range [][2]int{{80, 80}, {443, 443}, {8080, 80}, {8443, 443}} {
		block := portBlock(ports[0], ports[1])
		if !strings.Contains(string(tmpl), block) {
			t.Errorf("portBlock(%d, %d) not found in k3d-config.yaml — template may have been reformatted", ports[0], ports[1])
		}
	}
}

func TestStripConflictingPorts_StringManipulation(t *testing.T) {
	// Verify that removal produces valid YAML structure.
	block80 := portBlock(80, 80)
	block443 := portBlock(443, 443)

	fullConfig := "ports:\n" + block80 +
		"  - port: 8080:80\n    nodeFilters:\n      - loadbalancer\n" +
		block443 +
		"  - port: 8443:443\n    nodeFilters:\n      - loadbalancer\n" +
		"options:\n"

	// Simulate removing port 80 block.
	after80 := strings.Replace(fullConfig, block80, "", 1)
	if strings.Contains(after80, block80) {
		t.Fatal("80:80 block should be removed")
	}
	if !strings.Contains(after80, "8080:80") {
		t.Fatal("8080:80 should be preserved")
	}
	if !strings.Contains(after80, block443) {
		t.Fatal("443:443 block should be preserved")
	}

	// Simulate removing both.
	afterBoth := strings.Replace(after80, block443, "", 1)
	if strings.Contains(afterBoth, block443) {
		t.Fatal("443:443 block should be removed")
	}
	if !strings.Contains(afterBoth, "8443:443") {
		t.Fatal("8443:443 should be preserved")
	}
	if !strings.Contains(afterBoth, "ports:\n") {
		t.Fatal("YAML ports key should remain")
	}
}

func TestRewriteConflictingPorts_PreservesAvailableFallbacks(t *testing.T) {
	fullConfig := "ports:\n" +
		portBlock(80, 80) +
		portBlock(8080, 80) +
		portBlock(443, 443) +
		portBlock(8443, 443) +
		"options:\n"

	got := rewriteConflictingPorts(fullConfig, ui.New(false), func(port int) bool {
		return port == 8080 || port == 8443
	}, func() (int, error) {
		t.Fatal("should not pick an ephemeral port when fallbacks are available")
		return 0, nil
	}, nil)

	for _, unexpected := range []string{"- port: 80:80", "- port: 443:443"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected %s mapping to be removed:\n%s", unexpected, got)
		}
	}
	for _, expected := range []string{"- port: 8080:80", "- port: 8443:443"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %s mapping to be preserved:\n%s", expected, got)
		}
	}
}

func TestRewriteConflictingPorts_PicksEphemeralWhenAllDefaultsBusy(t *testing.T) {
	fullConfig := "ports:\n" +
		portBlock(80, 80) +
		portBlock(8080, 80) +
		portBlock(443, 443) +
		portBlock(8443, 443) +
		"options:\n"
	picks := []int{18080, 18443}

	got := rewriteConflictingPorts(fullConfig, ui.New(false), func(int) bool {
		return false
	}, func() (int, error) {
		if len(picks) == 0 {
			t.Fatal("unexpected extra port pick")
		}
		port := picks[0]
		picks = picks[1:]
		return port, nil
	}, nil)

	for _, unexpected := range []string{"- port: 80:80", "- port: 8080:80", "- port: 443:443", "- port: 8443:443"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected default mapping %s to be removed:\n%s", unexpected, got)
		}
	}
	for _, expected := range []string{"- port: 18080:80", "- port: 18443:443"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %s mapping to be inserted:\n%s", expected, got)
		}
	}
	if !strings.Contains(got, "options:\n") {
		t.Fatal("YAML options key should remain")
	}
}

func TestRewriteConflictingPorts_ForceSkipsOwnedPorts(t *testing.T) {
	fullConfig := "ports:\n" +
		portBlock(80, 80) +
		portBlock(8080, 80) +
		portBlock(443, 443) +
		portBlock(8443, 443) +
		"options:\n"

	// Every default host port reads as occupied. 80 and 443 are "owned" —
	// held by the existing obol cluster that --force is about to recreate
	// — and must be kept. 8080/8443 are genuinely foreign occupants (not
	// owned) and must still be stripped.
	got := rewriteConflictingPorts(fullConfig, ui.New(false),
		func(int) bool { return false },
		func() (int, error) {
			t.Fatal("should not need an ephemeral port: both container ports resolve via the owned mapping")
			return 0, nil
		},
		map[int]bool{80: true, 443: true},
	)

	for _, expected := range []string{"- port: 80:80", "- port: 443:443"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected owned mapping %s to be preserved under force:\n%s", expected, got)
		}
	}
	for _, unexpected := range []string{"- port: 8080:80", "- port: 8443:443"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected genuinely foreign-occupied mapping %s to still be stripped:\n%s", unexpected, got)
		}
	}
}

func TestEnsureK3dPortsAvailable_NoDefaultMappings(t *testing.T) {
	// Verify the file read/write path stays a no-op for configs that do not
	// contain the default ingress mappings.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "k3d.yaml")

	original := "ports:\n" +
		portBlock(18080, 80) +
		portBlock(18443, 443)

	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	u := ui.New(false)
	ensureK3dPortsAvailable(cfgPath, u)

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != original {
		t.Errorf("expected config unchanged when no default mappings are present\ngot:\n%s", string(data))
	}
}

func TestLocalIngressURL_DefaultK3dPort(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	err := os.WriteFile(filepath.Join(tmpDir, k3dConfigFile), []byte(`
ports:
  - port: 80:80
  - port: 8080:80
`), 0o644)
	if err != nil {
		t.Fatalf("write k3d config: %v", err)
	}

	if got := LocalIngressURL(cfg); got != "http://obol.stack" {
		t.Fatalf("LocalIngressURL() = %q, want %q", got, "http://obol.stack")
	}
}

func TestLocalIngressURL_CustomK3dPort(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	err := os.WriteFile(filepath.Join(tmpDir, k3dConfigFile), []byte(`
ports:
  - port: 18080:80
  - port: 18081:80
`), 0o644)
	if err != nil {
		t.Fatalf("write k3d config: %v", err)
	}

	if got := LocalIngressURL(cfg); got != "http://obol.stack:18080" {
		t.Fatalf("LocalIngressURL() = %q, want %q", got, "http://obol.stack:18080")
	}
}

func TestDestroyOldBackendIfSwitching_CleansStaleConfigs(t *testing.T) {
	// Simulate a k3d → k3s switch: k3d.yaml should be cleaned up
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	// Write k3d as the current backend + its config file
	SaveBackend(cfg, BackendK3d)

	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0o644)

	// Switch to k3s — k3d config should be cleaned up
	// (Destroy will fail because no real cluster, but cleanup should still work)
	if err := destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3s, "test-id", false); err != nil {
		t.Fatalf("destroyOldBackendIfSwitching: %v", err)
	}

	if _, err := os.Stat(k3dPath); !os.IsNotExist(err) {
		t.Error("k3d.yaml should be removed when switching to k3s")
	}
}

func TestDestroyOldBackendIfSwitching_NoopSameBackend(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	SaveBackend(cfg, BackendK3d)

	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0o644)

	// Same backend — nothing should be cleaned up
	if err := destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id", false); err != nil {
		t.Fatalf("destroyOldBackendIfSwitching: %v", err)
	}

	if _, err := os.Stat(k3dPath); os.IsNotExist(err) {
		t.Error("k3d.yaml should NOT be removed when re-initing same backend")
	}
}

func TestDestroyOldBackendIfSwitching_K3sToK3d(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	SaveBackend(cfg, BackendK3s)
	// Create k3s state files
	for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
		os.WriteFile(filepath.Join(tmpDir, f), []byte("data"), 0o644)
	}

	// Switch to k3d — k3s files should be cleaned up
	if err := destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id", false); err != nil {
		t.Fatalf("destroyOldBackendIfSwitching: %v", err)
	}

	for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
		if _, err := os.Stat(filepath.Join(tmpDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed when switching from k3s to k3d", f)
		}
	}
}

func TestDestroyOldBackendIfSwitching_NoBackendFile(t *testing.T) {
	// No .stack-backend file — LoadBackend defaults to k3d.
	// Switching to k3d should be a no-op (same backend).
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
	}

	// Should not panic or error
	if err := destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id", false); err != nil {
		t.Fatalf("destroyOldBackendIfSwitching: %v", err)
	}
}

// TestDestroyOldBackendIfSwitching_LiveServicesRefusesNonInteractiveWithoutYes
// is the regression test for the Canary402 `stack init --force --backend <X>`
// finding: destroying the old backend's cluster is exactly as dangerous as
// `obol stack down`/`purge`, so it must be gated on the same
// ConfirmRunningServicesLoss safety bar instead of running unconditionally.
func TestDestroyOldBackendIfSwitching_LiveServicesRefusesNonInteractiveWithoutYes(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
		StateDir:  filepath.Join(tmpDir, "state"),
	}

	SaveBackend(cfg, BackendK3d)
	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0o644)

	// A live sell-inference gateway makes this stack "serving traffic".
	writeGatewayPID(t, cfg, "aeon", os.Getpid())

	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf) // isTTY defaults false, no --yes

	err := destroyOldBackendIfSwitching(cfg, u, BackendK3s, "test-id", false)
	if err == nil {
		t.Fatal("expected error when switching backends non-interactively with live services and no --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should mention --yes (operator escape hatch): %v", err)
	}

	// Destroy (and the cleanup that follows it) must not have run: the old
	// backend's stale config file must survive the refused call.
	if _, statErr := os.Stat(k3dPath); statErr != nil {
		t.Errorf("k3d.yaml should NOT be removed when the safety gate refuses: %v", statErr)
	}
}

// TestDestroyOldBackendIfSwitching_SkipConfirmStillDestroys ensures --yes
// keeps working as the non-interactive escape hatch after the safety gate
// was added, mirroring Down/Purge's --yes behavior.
func TestDestroyOldBackendIfSwitching_SkipConfirmStillDestroys(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
		BinDir:    filepath.Join(tmpDir, "bin"),
		StateDir:  filepath.Join(tmpDir, "state"),
	}

	SaveBackend(cfg, BackendK3d)
	k3dPath := filepath.Join(tmpDir, k3dConfigFile)
	os.WriteFile(k3dPath, []byte("k3d config"), 0o644)

	writeGatewayPID(t, cfg, "aeon", os.Getpid())

	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	if err := destroyOldBackendIfSwitching(cfg, u, BackendK3s, "test-id", true); err != nil {
		t.Fatalf("destroyOldBackendIfSwitching with skipConfirm: %v", err)
	}

	if _, statErr := os.Stat(k3dPath); !os.IsNotExist(statErr) {
		t.Error("k3d.yaml should be removed when --yes overrides the confirmation")
	}
}

func TestOllamaHostIPForBackend_K3s(t *testing.T) {
	// k3s runs on the host, so the configured Ollama host is 127.0.0.1 — but
	// the result feeds a Kubernetes Endpoints object, and Kubernetes rejects
	// loopback addresses there (enforced since v1.33). The resolver must
	// substitute the host's routable address instead.
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error for k3s backend: %v", err)
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("expected a valid IP for k3s backend, got %q", ip)
	}

	if parsed.IsLoopback() {
		t.Errorf("k3s backend returned loopback %s; Kubernetes rejects loopback in Endpoints", ip)
	}
}

func TestOllamaHostIPForBackend_K3d(t *testing.T) {
	// k3d backend should return a valid IP via one of two strategies:
	//   macOS: DNS resolution of host.docker.internal
	//   Linux: DNS resolution of host.k3d.internal, or docker0 bridge fallback
	// In CI without Docker, both may fail → skip.
	ip, err := ollamaHostIPForBackend(BackendK3d)
	if err != nil {
		t.Skipf("skipping: resolution failed (expected in CI without Docker): %v", err)
	}

	if ip == "" {
		t.Fatal("expected non-empty IP for k3d backend")
	}
	// The result must be a parseable IP address (not a hostname)
	if net.ParseIP(ip) == nil {
		t.Errorf("expected a valid IP address for k3d backend, got %q", ip)
	}
}

func TestOllamaHostIPForBackend_AlreadyIP(t *testing.T) {
	// The resolver short-circuits on net.ParseIP rather than attempting DNS
	// when the configured host is already numeric. k3s is the numeric case
	// (127.0.0.1), so this exercises that path — the loopback guard then
	// swaps it for a routable address, which must still be a valid IP and
	// must not have gone through a DNS lookup failure.
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if net.ParseIP(ip) == nil {
		t.Errorf("expected a valid IP address, got %q", ip)
	}
}

func TestDockerBridgeGatewayIP(t *testing.T) {
	// On Linux with Docker installed, docker0 should exist with an IPv4 address.
	// On macOS or CI without Docker, skip gracefully.
	if runtime.GOOS != "linux" {
		t.Skip("docker0 interface only exists on Linux")
	}

	ip, err := dockerBridgeGatewayIP()
	if err != nil {
		t.Skipf("skipping: docker0 not available (expected without Docker): %v", err)
	}

	if net.ParseIP(ip) == nil {
		t.Errorf("expected valid IP from docker0, got %q", ip)
	}

	t.Logf("docker0 gateway IP: %s", ip)
}

// TestHelmfile_IncludesBuyerPodMonitor asserts the litellm-x402-buyer
// PodMonitor is shipped with the stack. The PodMonitor previously lived
// as an inline `bedag/raw` release in helmfile.yaml; it now lives next
// to its workload in base/templates/llm.yaml. The chart layout (the
// `base` Helm release) renders it during `obol stack up`.
func TestHelmfile_IncludesBuyerPodMonitor(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Fatal("project root not found")
	}

	data, err := os.ReadFile(filepath.Join(projectRoot, "internal/embed/infrastructure/base/templates/llm.yaml"))
	if err != nil {
		t.Fatalf("read llm template: %v", err)
	}

	out := string(data)

	if !strings.Contains(out, "kind: PodMonitor") {
		t.Fatalf("llm template missing PodMonitor:\n%s", out)
	}

	if !strings.Contains(out, "name: litellm-x402-buyer") {
		t.Fatalf("llm template missing buyer PodMonitor name:\n%s", out)
	}

	if !strings.Contains(out, "release: monitoring") {
		t.Fatalf("llm template missing monitoring label:\n%s", out)
	}

	if !strings.Contains(out, "port: buyer-http") || !strings.Contains(out, "path: /metrics") {
		t.Fatalf("llm template missing buyer metrics endpoint:\n%s", out)
	}
}

func TestLLMTemplate_IncludesPaidRouteAndBuyerSidecar(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Fatal("project root not found")
	}

	data, err := os.ReadFile(filepath.Join(projectRoot, "internal/embed/infrastructure/base/templates/llm.yaml"))
	if err != nil {
		t.Fatalf("read llm template: %v", err)
	}

	out := string(data)

	for _, want := range []string{
		`model_name: "paid/*"`,
		`model: "openai/*"`,
		// Paid routes go through the standalone x402-buyer Service — the
		// buyer is no longer a litellm-pod sidecar (issue #321: LiteLLM must
		// be stateless so RollingUpdate maxUnavailable:0 gives zero-downtime
		// rollouts).
		`api_base: "http://x402-buyer.llm.svc.cluster.local:8402/v1"`,
		`name: x402-buyer`,
		`containerPort: 8402`,
		`name: buyer-http`,
		`name: x402-buyer-config`,
		`name: x402-buyer-auths`,
		// Key rotation is the one remaining case that needs a pod
		// replacement (os.environ/ refs resolve at config load).
		`secret.reloader.stakater.com/reload: "litellm-secrets"`,
		`emptyDir:`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("llm template missing %q:\n%s", want, out)
		}
	}

	// Reloader must NOT watch litellm-config: every model_list change is
	// hot-applied via /model/new + /model/delete (CLI and controller), and a
	// ConfigMap-triggered rollout would reintroduce an inference gap on
	// every model add/remove/prefer and first-time purchase (issue #321).
	// The buyer ConfigMaps likewise hot-reload via /admin/reload.
	if strings.Contains(out, "configmap.reloader.stakater.com/reload") {
		t.Fatal("llm template must not carry a configmap Reloader annotation — model_list changes are hot-applied; a CM-triggered rollout gaps inference (issue #321)")
	}

	if strings.Contains(out, "custom_provider_map") {
		t.Fatalf("llm template should not require a custom provider:\n%s", out)
	}

	// Regression guard: provider API keys must not be pre-declared as
	// empty placeholders in the bootstrap Secret. If they are, every
	// `obol stack up` re-applies the manifest and overwrites whatever
	// `obol model setup` (or autoConfigureLLM) patched in, leaving the
	// user with `obol model status` reporting `enabled: true, api_key: false`.
	for _, forbidden := range []string{
		`ANTHROPIC_API_KEY: ""`,
		`OPENAI_API_KEY: ""`,
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("llm template must not pre-declare empty provider API keys (found %q) — these get clobbered on every `obol stack up`", forbidden)
		}
	}
}

func TestMergeLiteLLMConfigPreservesChartDefaultsAndPreviousModels(t *testing.T) {
	current := `
model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
litellm_settings:
  cache: false
  drop_params: true
`
	previous := `
model_list:
  - model_name: "anthropic/*"
    litellm_params:
      model: "anthropic/claude-sonnet-4-5-20250929"
`

	merged, err := mergeLiteLLMConfig(current, previous)
	if err != nil {
		t.Fatalf("mergeLiteLLMConfig: %v", err)
	}

	var got model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("unmarshal merged config: %v\n%s", err, merged)
	}

	if !hasLiteLLMModel(got, "paid/*") {
		t.Fatalf("merged config lost chart paid route:\n%s", merged)
	}
	if !hasLiteLLMModel(got, "anthropic/*") {
		t.Fatalf("merged config lost previous provider route:\n%s", merged)
	}
	if got.GeneralSettings["master_key"] != "os.environ/LITELLM_MASTER_KEY" {
		t.Fatalf("merged config lost chart general_settings:\n%#v", got.GeneralSettings)
	}
	if got.LiteLLMSettings["drop_params"] != true {
		t.Fatalf("merged config lost chart litellm_settings:\n%#v", got.LiteLLMSettings)
	}
}

func TestMergeLiteLLMConfigCurrentEntryWinsForChartDefaults(t *testing.T) {
	current := `
model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY
`
	previous := `
model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://custom-buyer:8402/v1"
      api_key: "custom"
`

	merged, err := mergeLiteLLMConfig(current, previous)
	if err != nil {
		t.Fatalf("mergeLiteLLMConfig: %v", err)
	}

	var got model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("unmarshal merged config: %v\n%s", err, merged)
	}

	for _, entry := range got.ModelList {
		if entry.ModelName == "paid/*" {
			if entry.LiteLLMParams.APIBase != "http://127.0.0.1:8402/v1" {
				t.Fatalf("current paid route did not win over previous route:\n%+v", entry)
			}
			return
		}
	}

	t.Fatalf("merged config missing paid route:\n%s", merged)
}

func TestLiteLLMConfigSemanticEqualIgnoresFormatting(t *testing.T) {
	a := `model_list:
  - model_name: "paid/*"
    litellm_params:
      model: "openai/*"
      api_base: "http://127.0.0.1:8402/v1"
      api_key: "unused"
litellm_settings:
  drop_params: true
`
	b := `litellm_settings:
    drop_params: true
model_list:
- model_name: paid/*
  litellm_params:
    model: openai/*
    api_base: http://127.0.0.1:8402/v1
    api_key: unused
`
	if !litellmConfigSemanticallyEqual(a, b) {
		t.Fatal("semantically equivalent LiteLLM configs compared unequal")
	}
}

func TestSyncDefaultsRestartsLiteLLMAfterConfigRestore_SourceGuard(t *testing.T) {
	src, err := os.ReadFile("stack.go")
	if err != nil {
		t.Fatalf("read stack.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func syncDefaults(")
	if start < 0 {
		t.Fatal("syncDefaults not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit syncDefaults body")
	}
	fn := body[start : start+1+end]
	restoreIdx := strings.Index(fn, "restoredLiteLLMConfig, err = restoreLiteLLMConfig")
	restartIdx := strings.Index(fn, "model.RestartLiteLLM(cfg, u, \"restored LiteLLM config\")")
	autoIdx := strings.Index(fn, "autoConfigureLLM(cfg, u)")
	if restoreIdx < 0 || restartIdx < 0 || autoIdx < 0 {
		t.Fatalf("syncDefaults must restore ConfigMap, restart LiteLLM, then auto-configure; restore=%d restart=%d auto=%d", restoreIdx, restartIdx, autoIdx)
	}
	if !(restoreIdx < restartIdx && restartIdx < autoIdx) {
		t.Fatalf("syncDefaults order wrong: restore=%d restart=%d auto=%d", restoreIdx, restartIdx, autoIdx)
	}
}

func TestConfigMapFieldOwnershipManifestUsesLiteralBlock(t *testing.T) {
	manifest := string(configMapFieldOwnershipManifest("litellm-config", "llm", "config.yaml", "model_list:\n  - model_name: paid/*\n"))

	for _, want := range []string{
		"apiVersion: v1\n",
		"kind: ConfigMap\n",
		"  name: litellm-config\n",
		"  namespace: llm\n",
		"  config.yaml: |\n",
		"    model_list:\n",
		"      - model_name: paid/*\n",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

func hasLiteLLMModel(cfg model.LiteLLMConfig, name string) bool {
	for _, entry := range cfg.ModelList {
		if entry.ModelName == name {
			return true
		}
	}

	return false
}

func TestHasLiveK3dCluster(t *testing.T) {
	tests := []struct {
		name       string
		containers []string
		want       bool
	}{
		{name: "empty", containers: nil, want: false},
		{name: "only mirror", containers: []string{"k3d-obol-docker-io.localhost"}, want: false},
		{name: "serverlb attached", containers: []string{"k3d-obol-stack-fancy-yak-serverlb"}, want: true},
		{name: "server-0 attached", containers: []string{"k3d-obol-stack-fancy-yak-server-0"}, want: true},
		{name: "server-12 attached", containers: []string{"k3d-obol-stack-fancy-yak-server-12"}, want: true},
		{name: "server-non-numeric ignored", containers: []string{"unrelated-server-foo"}, want: false},
		{name: "mixed mirror and live", containers: []string{"k3d-obol-ghcr-io.localhost", "k3d-obol-stack-blue-fox-server-0"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasLiveK3dCluster(tt.containers); got != tt.want {
				t.Fatalf("hasLiveK3dCluster(%v) = %v, want %v", tt.containers, got, tt.want)
			}
		})
	}
}

func TestBuildAndImportLocalImages_DefaultReusesExistingImage(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, stackIDFile), []byte("test-stack"), 0o600); err != nil {
		t.Fatalf("write stack id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.x402-verifier"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ] && [ \"${3:-}\" = \"ghcr.io/obolnetwork/x402-verifier:latest\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"build\" ]; then\n" +
		"  exit 97\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"pull\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	k3dScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'k3d %s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "k3d"), []byte(k3dScript), 0o755); err != nil {
		t.Fatalf("write k3d stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "")

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir}, ui.NewWithOptions(false, true))

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	if strings.Contains(log, "docker build -f") {
		t.Fatalf("expected default dev path to skip docker build when the local image exists, log:\n%s", log)
	}
	if !strings.Contains(log, "k3d image import ghcr.io/obolnetwork/x402-verifier:latest -c obol-stack-test-stack") {
		t.Fatalf("expected k3d import for cached x402 verifier image, log:\n%s", log)
	}
}

func TestBuildAndImportLocalImages_ForceRebuildEvenWhenImageExists(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfgDir: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, stackIDFile), []byte("test-stack"), 0o600); err != nil {
		t.Fatalf("write stack id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.x402-verifier"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ] && [ \"${3:-}\" = \"ghcr.io/obolnetwork/x402-verifier:latest\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"build\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"pull\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	k3dScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'k3d %s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "k3d"), []byte(k3dScript), 0o755); err != nil {
		t.Fatalf("write k3d stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "true")

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir}, ui.NewWithOptions(false, true))

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "docker build -f ") || !strings.Contains(log, "Dockerfile.x402-verifier -t ghcr.io/obolnetwork/x402-verifier:latest") {
		t.Fatalf("expected force-rebuild env var to rebuild even when the local image exists, log:\n%s", log)
	}
	if strings.Contains(log, "Reusing existing local image ghcr.io/obolnetwork/x402-verifier:latest") {
		t.Fatalf("expected force-rebuild env var to rebuild instead of reusing cache, log:\n%s", log)
	}
}

func TestBuildAndImportLocalImages_SelectiveRebuild(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	for _, d := range []string{cfgDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfgDir, stackIDFile), []byte("test-stack"), 0o600); err != nil {
		t.Fatalf("write stack id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// Provide Dockerfiles for two images so both are candidates.
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.x402-verifier"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile x402-verifier: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.serviceoffer-controller"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile serviceoffer-controller: %v", err)
	}

	// Both images are already available locally; only x402-verifier should be rebuilt.
	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ]; then exit 0; fi\n" +
		"if [ \"${1:-}\" = \"build\" ]; then exit 0; fi\n" +
		"exit 0\n"
	k3dScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'k3d %s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "k3d"), []byte(k3dScript), 0o755); err != nil {
		t.Fatalf("write k3d stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)
	// Only request a rebuild of x402-verifier, not serviceoffer-controller.
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "x402-verifier")

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir}, ui.NewWithOptions(false, true))

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "Dockerfile.x402-verifier") {
		t.Fatalf("expected selective rebuild to build x402-verifier, log:\n%s", log)
	}
	if strings.Contains(log, "Dockerfile.serviceoffer-controller") {
		t.Fatalf("expected selective rebuild to skip serviceoffer-controller (already local), log:\n%s", log)
	}
}

func TestForceRebuildSet_PublicStorefrontAlias(t *testing.T) {
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "public-storefront")

	shouldForceRebuild := forceRebuildSet()
	if !shouldForceRebuild("ghcr.io/obolnetwork/obol-stack-public-storefront:latest") {
		t.Fatal("public-storefront alias should rebuild obol-stack-public-storefront")
	}
	if shouldForceRebuild("ghcr.io/obolnetwork/x402-verifier:latest") {
		t.Fatal("public-storefront alias should not rebuild unrelated images")
	}
}

func TestBuildAndImportLocalImages_SkipsK3dImportWhenCacheIsValid(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	for _, d := range []string{cfgDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfgDir, stackIDFile), []byte("test-stack"), 0o600); err != nil {
		t.Fatalf("write stack id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile.x402-verifier"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	const (
		fakeDigest = "sha256:cafef00d"
		fakeCID    = "k3d-server-cid-fake"
	)

	// Pre-seed the cache so the verifier image is treated as already imported.
	cachePath := filepath.Join(cfgDir, importedImagesCacheFile)
	cacheJSON := `{"entries":{"obol-stack-test-stack|ghcr.io/obolnetwork/x402-verifier:latest":{"digest":"` + fakeDigest + `","cluster_cid":"` + fakeCID + `"}}}`
	if err := os.WriteFile(cachePath, []byte(cacheJSON), 0o600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		// `docker image inspect <tag>` (no --format) — used by dockerImageAvailableLocally.
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ] && [ \"${3:-}\" = \"ghcr.io/obolnetwork/x402-verifier:latest\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		// `docker image inspect --format {{.Id}} <tag>` — dockerImageDigest.
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ] && [ \"${3:-}\" = \"--format\" ]; then\n" +
		"  printf '" + fakeDigest + "\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		// `docker inspect --format {{.Id}} k3d-<cluster>-server-0` — k3dServerContainerID.
		"if [ \"${1:-}\" = \"inspect\" ] && [ \"${2:-}\" = \"--format\" ]; then\n" +
		"  printf '" + fakeCID + "\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"build\" ] || [ \"${1:-}\" = \"pull\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	k3dScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'k3d %s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "k3d"), []byte(k3dScript), 0o755); err != nil {
		t.Fatalf("write k3d stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "")

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir}, ui.NewWithOptions(false, true))

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	if strings.Contains(log, "k3d image import ghcr.io/obolnetwork/x402-verifier:latest") {
		t.Fatalf("expected cache hit to skip k3d import for verifier, log:\n%s", log)
	}
	if strings.Contains(log, "docker build -f") {
		t.Fatalf("expected cache hit to skip docker build, log:\n%s", log)
	}
}

func TestBuildAndImportLocalImages_PreloadsRuntimeImagesViaCrictl(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	for _, d := range []string{cfgDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfgDir, stackIDFile), []byte("test-stack"), 0o600); err != nil {
		t.Fatalf("write stack id: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"${1:-}\" = \"inspect\" ] && [ \"${2:-}\" = \"--format\" ]; then\n" +
		"  printf 'k3d-server-cid-fake\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"exec\" ]; then exit 0; fi\n" +
		"if [ \"${1:-}\" = \"image\" ] && [ \"${2:-}\" = \"inspect\" ]; then exit 1; fi\n" +
		"if [ \"${1:-}\" = \"pull\" ]; then exit 97; fi\n" +
		"exit 0\n"
	k3dScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'k3d %s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "k3d"), []byte(k3dScript), 0o755); err != nil {
		t.Fatalf("write k3d stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)
	t.Setenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES", "")

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir}, ui.NewWithOptions(false, true))

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	for _, want := range []string{
		"docker exec k3d-obol-stack-test-stack-server-0 crictl pull ghcr.io/obolnetwork/openclaw:",
		"docker exec k3d-obol-stack-test-stack-server-0 crictl pull nousresearch/hermes-agent:",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("expected runtime preload via node crictl command %q, log:\n%s", want, log)
		}
	}
	for _, unwanted := range []string{
		"docker pull ghcr.io/obolnetwork/openclaw",
		"docker pull nousresearch/hermes-agent",
		"k3d image import ghcr.io/obolnetwork/openclaw",
		"k3d image import nousresearch/hermes-agent",
	} {
		if strings.Contains(log, unwanted) {
			t.Fatalf("runtime preload should not use host docker pull or k3d image import (%q), log:\n%s", unwanted, log)
		}
	}
}

func TestPreloadRuntimeImage_FallsBackToK3sCrictl(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	dockerScript := "#!/bin/sh\n" +
		"set -eu\n" +
		"printf 'docker %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ \"${1:-}\" = \"exec\" ] && [ \"${2:-}\" = \"k3d-obol-stack-test-stack-server-0\" ] && [ \"${3:-}\" = \"crictl\" ]; then\n" +
		"  exit 42\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"exec\" ] && [ \"${2:-}\" = \"k3d-obol-stack-test-stack-server-0\" ] && [ \"${3:-}\" = \"k3s\" ]; then\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	if ok := preloadRuntimeImageIntoCluster("obol-stack-test-stack", "example.com/runtime:tag", ui.NewWithOptions(false, true)); !ok {
		t.Fatal("expected k3s crictl fallback to succeed")
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logData)
	for _, want := range []string{
		"docker exec k3d-obol-stack-test-stack-server-0 crictl pull example.com/runtime:tag",
		"docker exec k3d-obol-stack-test-stack-server-0 k3s crictl pull example.com/runtime:tag",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("expected command %q, log:\n%s", want, log)
		}
	}
}

// newCaptureUI returns a UI that writes stdout and stderr into the returned
// buffers. Warn → stderr, Dim/Blank/Info → stdout.
func newCaptureUI() (*ui.UI, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return ui.NewForTest(&stdout, &stderr), &stdout, &stderr
}

// TestWarnIfNoChatModel_EmittsWarnWhenNoModels verifies that the warn block
// fires when chatModels is empty (all three detection branches found nothing).
func TestWarnIfNoChatModel_EmitsWarnWhenNoModels(t *testing.T) {
	u, stdout, stderr := newCaptureUI()
	warnIfNoChatModel(nil, u)

	if !strings.Contains(stderr.String(), "No chat-capable model detected") {
		t.Fatalf("expected warn on stderr, got: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "ollama pull") {
		t.Fatalf("expected ollama pull hint on stdout, got: %q", stdout.String())
	}
}

// TestWarnIfNoChatModel_SilentWhenModelsPresent verifies no warn is emitted
// when at least one chat-capable model is already configured.
func TestWarnIfNoChatModel_SilentWhenModelsPresent(t *testing.T) {
	u, stdout, stderr := newCaptureUI()
	warnIfNoChatModel([]string{"qwen3.5:4b"}, u)

	if stderr.Len() != 0 {
		t.Fatalf("expected no output on stderr when models present, got: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no output on stdout when models present, got: %q", stdout.String())
	}
}

// TestWarnIfNoChatModel_SilentWhenConcretePaidModelPresent verifies that a
// concrete paid/<model> entry (not just the wildcard) counts as chat-capable.
func TestWarnIfNoChatModel_SilentWhenConcretePaidModelPresent(t *testing.T) {
	u, _, stderr := newCaptureUI()
	warnIfNoChatModel([]string{"paid/aeon"}, u)

	if strings.Contains(stderr.String(), "No chat-capable model detected") {
		t.Fatalf("concrete paid/aeon should suppress warn, got: %q", stderr.String())
	}
}

// TestWarnIfNoChatModel_EmitsWarnForWildcardOnly verifies that only the
// "paid/*" wildcard in the model_list (no concrete entries) is not sufficient
// to suppress the warning. ListChatCapableModels filters wildcards out, so
// warnIfNoChatModel receives an empty slice in this scenario.
func TestWarnIfNoChatModel_EmitsWarnForWildcardOnly(t *testing.T) {
	// Simulate what ListChatCapableModels returns when the ConfigMap contains
	// only "paid/*": isChatCapableModelName filters it out → empty slice.
	u, _, stderr := newCaptureUI()
	warnIfNoChatModel([]string{}, u)

	if !strings.Contains(stderr.String(), "No chat-capable model detected") {
		t.Fatalf("wildcard-only list should trigger warn, got: %q", stderr.String())
	}
}

// fakeHelmScript returns a sh script body that fakes a `helm` binary with
// configurable behaviour for repo update. It logs every invocation to
// invokeLog so callers can assert on what helm was asked to do.
//
//   - `helm version --short`:       prints "v3.20.1".
//   - `helm repo update --help`:    advertises --fail-on-repo-update-fail=false.
//   - `helm repo add ...`:          exits 0 (idempotent registration).
//   - `helm repo update ...`:       exits with repoUpdateExit. Stdout is empty,
//     stderr mimics helm's real "failed to update the following repositories"
//     message when repoUpdateExit != 0.
//   - Anything else:                exits 0.
func fakeHelmScript(invokeLog string, repoUpdateExit int) string {
	return `#!/bin/sh
echo "$@" >> "` + invokeLog + `"
if [ "$1" = "version" ] && [ "$2" = "--short" ]; then
  echo "v3.20.1"
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "add" ]; then
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "update" ] && [ "$3" = "--help" ]; then
  echo "      --fail-on-repo-update-fail=false   tolerate individual repo failures"
  exit 0
fi
if [ "$1" = "repo" ] && [ "$2" = "update" ]; then
  if [ "` + sprintInt(repoUpdateExit) + `" != "0" ]; then
    echo "Error: failed to update the following repositories: [https://example.invalid/charts]" 1>&2
    exit ` + sprintInt(repoUpdateExit) + `
  fi
  echo "...Successfully got an update from all chart repositories"
  exit 0
fi
exit 0
`
}

func sprintInt(n int) string { return strconv.Itoa(n) }

// TestPreflightHelmRepos_SuccessAllowsSkipDeps verifies the happy path:
// when our managed repos update cleanly, preflightHelmRepos returns true so
// the caller can pass --skip-deps to helmfile sync.
func TestPreflightHelmRepos_SuccessAllowsSkipDeps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not supported on windows")
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	cfgDir := filepath.Join(dir, "cfg")
	for _, d := range []string{binDir, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	invokeLog := filepath.Join(dir, "helm.log")
	helm := filepath.Join(binDir, "helm")
	if err := os.WriteFile(helm, []byte(fakeHelmScript(invokeLog, 0)), 0o755); err != nil { //nolint:gosec // test fake
		t.Fatalf("write fake helm: %v", err)
	}

	helmfilePath := filepath.Join(cfgDir, "helmfile.yaml")
	body := `
repositories:
  - name: traefik
    url: https://traefik.github.io/charts
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/
`
	if err := os.WriteFile(helmfilePath, []byte(body), 0o600); err != nil {
		t.Fatalf("write helmfile: %v", err)
	}

	cfg := &config.Config{BinDir: binDir, ConfigDir: cfgDir}
	u, _, _ := newCaptureUI()

	if !preflightHelmRepos(cfg, u, helm, helmfilePath) {
		t.Fatal("preflightHelmRepos should return true when helm repo update succeeds")
	}

	logged, _ := os.ReadFile(invokeLog)
	got := string(logged)
	// Sanity: we should have updated only our managed repos by name, and
	// passed the tolerant flag.
	if !strings.Contains(got, "--fail-on-repo-update-fail=false") {
		t.Fatalf("expected tolerant flag in helm invocation, got: %s", got)
	}
	if !strings.Contains(got, "traefik") || !strings.Contains(got, "obol") {
		t.Fatalf("expected managed repo names in helm invocation, got: %s", got)
	}
}

// TestPreflightHelmRepos_FailureFallsBackGracefully captures the actual bug
// scenario: a tertiary repo update fails. The preflight should swallow that
// (and return false so the caller falls back to letting helmfile manage
// dependencies itself) rather than propagate a fatal error that would, in
// the old code path, have stopped the entire cluster.
func TestPreflightHelmRepos_FailureFallsBackGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary not supported on windows")
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	cfgDir := filepath.Join(dir, "cfg")
	for _, d := range []string{binDir, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	invokeLog := filepath.Join(dir, "helm.log")
	helm := filepath.Join(binDir, "helm")
	// Fake helm exits 1 on `repo update` — same shape as the real-world
	// kubernetes-dashboard 404 incident that motivated this fix.
	if err := os.WriteFile(helm, []byte(fakeHelmScript(invokeLog, 1)), 0o755); err != nil { //nolint:gosec // test fake
		t.Fatalf("write fake helm: %v", err)
	}

	helmfilePath := filepath.Join(cfgDir, "helmfile.yaml")
	body := `
repositories:
  - name: traefik
    url: https://traefik.github.io/charts
`
	if err := os.WriteFile(helmfilePath, []byte(body), 0o600); err != nil {
		t.Fatalf("write helmfile: %v", err)
	}

	cfg := &config.Config{BinDir: binDir, ConfigDir: cfgDir}
	u, _, _ := newCaptureUI()

	// Critical assertion: the preflight returns (without panicking, without
	// returning an error type at all — by design it absorbs failure and
	// signals "let helmfile try its own resolution").
	if got := preflightHelmRepos(cfg, u, helm, helmfilePath); got {
		t.Fatal("preflightHelmRepos should return false when helm repo update fails")
	}
}

// TestSyncDefaults_DoesNotCallDownOnHelmfileFailure is a source-level
// regression guard for the cluster-stop-on-failure bug. The old syncDefaults
// invoked Down() (which calls `k3d cluster delete`) whenever helmfile sync
// errored, destroying user state for transient failures like a single dead
// helm repo. The fix removes that call; this test keeps it gone.
//
// We inspect the source rather than mock the entire backend stack because
// the wrong behaviour to prevent is statically visible (a Down call in the
// error branch) and the right behaviour is statically defined (no Down
// call). Behavioural drift is checked by the helmcmd unit tests above.
func TestSyncDefaults_DoesNotCallDownOnHelmfileFailure(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Skip("project root not found")
	}

	src, err := os.ReadFile(filepath.Join(projectRoot, "internal/stack/stack.go"))
	if err != nil {
		t.Fatalf("read stack.go: %v", err)
	}

	// Locate the syncDefaults function body. We bound the scan to the
	// function so we don't accidentally match Down() calls in unrelated
	// helpers (e.g. the `Down(cfg, u *ui.UI)` definition itself).
	const fnSig = "func syncDefaults("
	start := strings.Index(string(src), fnSig)
	if start < 0 {
		t.Fatalf("syncDefaults function not found in stack.go")
	}
	const fnEndMarker = "\n// claudeTipIfRelevant"
	end := strings.Index(string(src)[start:], fnEndMarker)
	if end < 0 {
		t.Fatalf("could not locate end of syncDefaults body")
	}
	body := string(src)[start : start+end]

	if strings.Contains(body, "Down(cfg, u)") {
		t.Fatalf("syncDefaults must not call Down() on failure — that destroys " +
			"unrelated user state when helmfile sync fails for transient reasons. " +
			"See fix/tolerant-helm-repo-update.")
	}
}
