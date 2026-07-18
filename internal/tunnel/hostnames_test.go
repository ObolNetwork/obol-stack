package tunnel

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func newHostnameTestConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		ConfigDir: dir,
		BinDir:    filepath.Join(dir, "bin"),
		DataDir:   filepath.Join(dir, "data"),
		StateDir:  filepath.Join(dir, "state"),
	}
}

// writeFakeKubeconfig drops a placeholder kubeconfig so the "stack not running"
// guard passes and the later validation guards (the ones we assert here) fire.
// No cluster is contacted: every test below errors at a guard that runs BEFORE
// any kubectl/cloudflared call.
func writeFakeKubeconfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	if err := os.MkdirAll(cfg.ConfigDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	path := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatalf("write fake kubeconfig: %v", err)
	}
}

func persistentLocalState(hostnames ...string) *tunnelState {
	return &tunnelState{
		ExposureMode:   tunnelExposurePersistent,
		ManagementMode: tunnelManagementLocal,
		Hostname:       hostnames[0],
		Hostnames:      hostnames,
		TunnelID:       "00000000-0000-0000-0000-000000000000",
		TunnelName:     "obol-stack-test-local",
	}
}

// --- state reconciliation (legacy migration / dedup) -----------------------

func TestReconcileHostnameSet_LegacyMigration(t *testing.T) {
	st := &tunnelState{
		ExposureMode:   tunnelExposurePersistent,
		ManagementMode: tunnelManagementLocal,
		Hostname:       "Stack.Example.com",
		TunnelID:       "00000000-0000-0000-0000-000000000000",
	}
	got := normalizeTunnelState(st)

	if want := []string{"stack.example.com"}; !reflect.DeepEqual(got.Hostnames, want) {
		t.Fatalf("Hostnames = %v, want %v", got.Hostnames, want)
	}
	if got.Hostname != "stack.example.com" {
		t.Fatalf("Hostname = %q, want primary mirror stack.example.com", got.Hostname)
	}
}

func TestReconcileHostnameSet_DedupAndOrder(t *testing.T) {
	st := &tunnelState{
		ExposureMode:   tunnelExposurePersistent,
		ManagementMode: tunnelManagementRemote,
		AccountID:      "acct",
		Hostname:       "a.example.com",
		Hostnames:      []string{"a.example.com", "B.Example.com", "", "a.example.com", "c.example.com"},
		TunnelID:       "00000000-0000-0000-0000-000000000000",
	}
	got := normalizeTunnelState(st)

	want := []string{"a.example.com", "b.example.com", "c.example.com"}
	if !reflect.DeepEqual(got.Hostnames, want) {
		t.Fatalf("Hostnames = %v, want %v", got.Hostnames, want)
	}
	if got.Hostname != "a.example.com" {
		t.Fatalf("Hostname = %q, want a.example.com (Hostnames[0])", got.Hostname)
	}
}

func TestReconcileHostnameSet_HostnamesWithoutScalar(t *testing.T) {
	st := &tunnelState{
		ExposureMode:   tunnelExposurePersistent,
		ManagementMode: tunnelManagementLocal,
		Hostnames:      []string{"first.example.com", "second.example.com"},
		TunnelID:       "00000000-0000-0000-0000-000000000000",
	}
	got := normalizeTunnelState(st)
	if got.Hostname != "first.example.com" {
		t.Fatalf("Hostname = %q, want first.example.com", got.Hostname)
	}
	if want := []string{"first.example.com", "second.example.com"}; !reflect.DeepEqual(got.Hostnames, want) {
		t.Fatalf("Hostnames = %v, want %v", got.Hostnames, want)
	}
}

func TestHostnameSet_RoundTripsThroughDisk(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	if err := saveTunnelState(cfg, persistentLocalState("a.example.com", "b.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := []string{"a.example.com", "b.example.com"}; !reflect.DeepEqual(got.HostnameSet(), want) {
		t.Fatalf("HostnameSet = %v, want %v", got.HostnameSet(), want)
	}
}

// --- listing ---------------------------------------------------------------

func TestListHostnames_PersistentLocal(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	if err := saveTunnelState(cfg, persistentLocalState("a.example.com", "b.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}

	result, err := ListHostnames(cfg)
	if err != nil {
		t.Fatalf("ListHostnames: %v", err)
	}
	if result.ManagementMode != tunnelManagementLocal {
		t.Fatalf("ManagementMode = %q, want local", result.ManagementMode)
	}
	if len(result.Hostnames) != 2 {
		t.Fatalf("want 2 hostnames, got %d", len(result.Hostnames))
	}
	if !result.Hostnames[0].Primary || result.Hostnames[1].Primary {
		t.Fatalf("expected only the first hostname primary: %+v", result.Hostnames)
	}
	if result.Hostnames[0].URL != "https://a.example.com" {
		t.Fatalf("primary URL = %q, want https://a.example.com", result.Hostnames[0].URL)
	}
}

func TestListHostnames_Quick(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	if err := saveTunnelState(cfg, &tunnelState{Mode: "quick"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	result, err := ListHostnames(cfg)
	if err != nil {
		t.Fatalf("ListHostnames: %v", err)
	}
	if len(result.Hostnames) != 0 {
		t.Fatalf("quick tunnel should have no hostnames, got %v", result.Hostnames)
	}
}

func TestListHostnames_NoState(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	result, err := ListHostnames(cfg)
	if err != nil {
		t.Fatalf("ListHostnames with no state: %v", err)
	}
	if len(result.Hostnames) != 0 {
		t.Fatalf("no-state tunnel should have no hostnames, got %v", result.Hostnames)
	}
}

func TestHostnameInfos_PrimaryAndURL(t *testing.T) {
	infos := hostnameInfos([]string{"primary.example.com", "second.example.com"})
	if len(infos) != 2 {
		t.Fatalf("want 2 infos, got %d", len(infos))
	}
	if !infos[0].Primary || infos[1].Primary {
		t.Fatalf("only index 0 should be primary: %+v", infos)
	}
	if infos[1].URL != "https://second.example.com" {
		t.Fatalf("URL = %q, want https://second.example.com", infos[1].URL)
	}
}

// --- AddHostname guards (all error before any cluster call) ----------------
// Exception: TestAddHostname_DuplicateIsIdempotent succeeds as a no-op and
// fail-opens through CreateStorefront (no real cluster required).

func TestAddHostname_RejectsEmptyHostname(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	if _, err := AddHostname(cfg, ui.New(false), AddHostnameOptions{Hostname: "  "}); err == nil {
		t.Fatal("expected error for empty hostname")
	}
}

func TestAddHostname_RejectsWithoutStack(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	// No kubeconfig.yaml on disk → "stack not running" guard fires first.
	_, err := AddHostname(cfg, ui.New(false), AddHostnameOptions{Hostname: "x.example.com"})
	if err == nil || !strings.Contains(err.Error(), "stack not running") {
		t.Fatalf("expected 'stack not running' error, got %v", err)
	}
}

func TestAddHostname_RejectsWithoutPersistentTunnel(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)
	if err := saveTunnelState(cfg, &tunnelState{Mode: "quick"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := AddHostname(cfg, ui.New(false), AddHostnameOptions{Hostname: "x.example.com"})
	if err == nil || !strings.Contains(err.Error(), "no permanent tunnel") {
		t.Fatalf("expected 'no permanent tunnel' error, got %v", err)
	}
}

func TestAddHostname_DuplicateIsIdempotent(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)
	if err := saveTunnelState(cfg, persistentLocalState("a.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Mixed-case duplicate must still resolve to the same tracked hostname
	// (normalized match) and succeed as a no-op instead of erroring. This
	// exercises the storefront re-render path too (CreateStorefront fails
	// open when kubectl/cluster access isn't available, so it does not
	// turn this into an error in a unit-test environment).
	result, err := AddHostname(cfg, ui.New(false), AddHostnameOptions{Hostname: "A.Example.com"})
	if err != nil {
		t.Fatalf("expected idempotent success for already-bound hostname, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result")
	}
	if result.Action != "unchanged" {
		t.Fatalf("Action = %q, want %q", result.Action, "unchanged")
	}
	if len(result.Hostnames) != 1 || result.Hostnames[0].Hostname != "a.example.com" {
		t.Fatalf("expected result to report the existing hostname set, got %+v", result.Hostnames)
	}
}

// --- RemoveHostname guards (all error before any cluster call) -------------

func TestRemoveHostname_RejectsWithoutStack(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	_, err := RemoveHostname(cfg, ui.New(false), RemoveHostnameOptions{Hostname: "x.example.com"})
	if err == nil || !strings.Contains(err.Error(), "stack not running") {
		t.Fatalf("expected 'stack not running' error, got %v", err)
	}
}

func TestRemoveHostname_RefusesLast(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)
	if err := saveTunnelState(cfg, persistentLocalState("only.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := RemoveHostname(cfg, ui.New(false), RemoveHostnameOptions{Hostname: "only.example.com"})
	if err == nil || !strings.Contains(err.Error(), "refusing to remove the last hostname") {
		t.Fatalf("expected refuse-last error, got %v", err)
	}
}

func TestRemoveHostname_UnknownHostname(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)
	if err := saveTunnelState(cfg, persistentLocalState("a.example.com", "b.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := RemoveHostname(cfg, ui.New(false), RemoveHostnameOptions{Hostname: "nope.example.com"})
	if err == nil || !strings.Contains(err.Error(), "not a tracked tunnel hostname") {
		t.Fatalf("expected unknown-hostname error, got %v", err)
	}
}

// --- Delete (teardown) guards + state cleanup ------------------------------

func TestDelete_RejectsWithoutStack(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	_, err := Delete(cfg, ui.New(false), DeleteOptions{})
	if err == nil || !strings.Contains(err.Error(), "stack not running") {
		t.Fatalf("expected 'stack not running' error, got %v", err)
	}
}

func TestDelete_RejectsWithoutPersistentTunnel(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)
	if err := saveTunnelState(cfg, &tunnelState{Mode: "quick"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := Delete(cfg, ui.New(false), DeleteOptions{})
	if err == nil || !strings.Contains(err.Error(), "no permanent tunnel") {
		t.Fatalf("expected 'no permanent tunnel' error, got %v", err)
	}
}

func TestDeleteTunnelState_RemovesFile(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	if err := saveTunnelState(cfg, persistentLocalState("a.example.com")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := deleteTunnelState(cfg); err != nil {
		t.Fatalf("deleteTunnelState: %v", err)
	}
	if st, _ := loadTunnelState(cfg); st != nil {
		t.Fatalf("expected nil state after delete, got %+v", st)
	}
	// Idempotent: deleting again is not an error.
	if err := deleteTunnelState(cfg); err != nil {
		t.Fatalf("second deleteTunnelState should be a no-op, got %v", err)
	}
}
