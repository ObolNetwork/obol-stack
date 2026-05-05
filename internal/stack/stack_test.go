package stack

import (
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
	})

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
	})

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
	destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3s, "test-id")

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
	destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id")

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
	destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id")

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
	destroyOldBackendIfSwitching(cfg, ui.New(false), BackendK3d, "test-id")
}

func TestOllamaHostIPForBackend_K3s(t *testing.T) {
	// k3s backend should return 127.0.0.1 (already an IP, no DNS resolution needed)
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error for k3s backend: %v", err)
	}

	if ip != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1 for k3s backend, got %s", ip)
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
	// Verify the function passes through an already-numeric IP unchanged.
	// k3s returns "127.0.0.1" from ollamaHostForBackend, so it should
	// short-circuit on net.ParseIP without attempting DNS.
	ip, err := ollamaHostIPForBackend(BackendK3s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ip != "127.0.0.1" {
		t.Errorf("expected pass-through of 127.0.0.1, got %s", ip)
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

func TestHelmfile_IncludesBuyerPodMonitor(t *testing.T) {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		t.Fatal("project root not found")
	}

	data, err := os.ReadFile(filepath.Join(projectRoot, "internal/embed/infrastructure/helmfile.yaml"))
	if err != nil {
		t.Fatalf("read helmfile: %v", err)
	}

	out := string(data)

	if !strings.Contains(out, "kind: PodMonitor") {
		t.Fatalf("helmfile missing PodMonitor:\n%s", out)
	}

	if !strings.Contains(out, "name: litellm-x402-buyer") {
		t.Fatalf("helmfile missing buyer PodMonitor name:\n%s", out)
	}

	if !strings.Contains(out, "release: monitoring") {
		t.Fatalf("helmfile missing monitoring label:\n%s", out)
	}

	if !strings.Contains(out, "port: buyer-http") || !strings.Contains(out, "path: /metrics") {
		t.Fatalf("helmfile missing buyer metrics endpoint:\n%s", out)
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
		`api_base: "http://127.0.0.1:8402/v1"`,
		`name: x402-buyer`,
		`containerPort: 8402`,
		`name: buyer-http`,
		`name: x402-buyer-config`,
		`name: x402-buyer-auths`,
		`emptyDir: {}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("llm template missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "custom_provider_map") {
		t.Fatalf("llm template should not require a custom provider:\n%s", out)
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

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir})

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

	buildAndImportLocalImages(&config.Config{ConfigDir: cfgDir, BinDir: binDir})

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
