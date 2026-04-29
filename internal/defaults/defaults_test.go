package defaults

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestCopyInfrastructureRendersStackPlaceholders(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	if err := CopyInfrastructure(cfg, backendK3s, "test-stack"); err != nil {
		t.Fatalf("CopyInfrastructure: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "llm.yaml"))
	if err != nil {
		t.Fatalf("read llm defaults: %v", err)
	}

	out := string(data)
	for _, want := range []string{
		`ip: "127.0.0.1"`,
		`LITELLM_MASTER_KEY: "sk-obol-test-stack"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered defaults missing %q:\n%s", want, out)
		}
	}
	for _, unexpected := range []string{"{{OLLAMA_HOST_IP}}", "{{CLUSTER_ID}}"} {
		if strings.Contains(out, unexpected) {
			t.Fatalf("rendered defaults still contain %q:\n%s", unexpected, out)
		}
	}
}

func TestRefreshInfrastructureIfChangedUsesStamp(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	refreshed, err := RefreshInfrastructureIfChanged(cfg, backendK3s, "test-stack")
	if err != nil {
		t.Fatalf("first RefreshInfrastructureIfChanged: %v", err)
	}
	if !refreshed {
		t.Fatal("first refresh should copy defaults")
	}

	marker := filepath.Join(cfg.ConfigDir, "defaults", "marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	refreshed, err = RefreshInfrastructureIfChanged(cfg, backendK3s, "test-stack")
	if err != nil {
		t.Fatalf("second RefreshInfrastructureIfChanged: %v", err)
	}
	if refreshed {
		t.Fatal("second refresh should be skipped for the same stamp")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unchanged refresh should not rewrite defaults tree: %v", err)
	}

	refreshed, err = RefreshInfrastructureIfChanged(cfg, backendK3s, "other-stack")
	if err != nil {
		t.Fatalf("changed RefreshInfrastructureIfChanged: %v", err)
	}
	if !refreshed {
		t.Fatal("stack ID change should refresh defaults")
	}

	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "llm.yaml"))
	if err != nil {
		t.Fatalf("read llm defaults: %v", err)
	}
	if !strings.Contains(string(data), `LITELLM_MASTER_KEY: "sk-obol-other-stack"`) {
		t.Fatalf("refreshed defaults did not use new stack ID:\n%s", string(data))
	}
}

func TestDetectedBackendNameDefaultsToK3d(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	if got := DetectedBackendName(cfg); got != backendK3d {
		t.Fatalf("DetectedBackendName() = %q, want %q", got, backendK3d)
	}

	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, stackBackendFile), []byte("k3s\n"), 0o600); err != nil {
		t.Fatalf("write backend: %v", err)
	}
	if got := DetectedBackendName(cfg); got != backendK3s {
		t.Fatalf("DetectedBackendName() = %q, want %q", got, backendK3s)
	}
}
