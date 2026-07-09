package defaults

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/images"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

func TestCopyInfrastructure_DevModeRewritesManagedImages(t *testing.T) {
	t.Setenv("OBOL_DEVELOPMENT", "true")

	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := CopyInfrastructure(cfg, backendK3s, "test-stack"); err != nil {
		t.Fatalf("CopyInfrastructure: %v", err)
	}

	x402Path := filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "x402.yaml")
	data, err := os.ReadFile(x402Path)
	if err != nil {
		t.Fatalf("read x402.yaml: %v", err)
	}
	out := string(data)
	devTag := DevImageTag()

	for _, base := range []string{
		"ghcr.io/obolnetwork/x402-verifier",
		"ghcr.io/obolnetwork/serviceoffer-controller",
		"ghcr.io/obolnetwork/job-broker",
	} {
		want := base + ":" + devTag
		if !strings.Contains(out, want) {
			t.Errorf("dev mode did not rewrite to %q in %s", want, x402Path)
		}
		if strings.Contains(out, base+"@sha256:") {
			t.Errorf("dev mode left digest pin on %s", base)
		}
		if strings.Contains(out, base+":"+images.PlaceholderTag) {
			t.Errorf("dev mode left placeholder on %s", base)
		}
		if strings.Contains(out, base+":latest") {
			t.Errorf("dev mode left :latest on %s", base)
		}
	}

	if got := ReadDevImageTag(cfg); got != devTag {
		t.Errorf("persisted dev image tag = %q, want %q", got, devTag)
	}

	llmPath := filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "llm.yaml")
	llmData, err := os.ReadFile(llmPath)
	if err != nil {
		t.Fatalf("read llm.yaml: %v", err)
	}
	buyer := "ghcr.io/obolnetwork/x402-buyer"
	if !strings.Contains(string(llmData), buyer+":"+devTag) {
		t.Errorf("dev mode did not rewrite x402-buyer to :%s", devTag)
	}
	if strings.Contains(string(llmData), buyer+":"+devTag+"@sha256:") {
		t.Errorf("dev mode left orphan @sha256 on x402-buyer")
	}
}

func TestDevImageTag_Format(t *testing.T) {
	tag := DevImageTag()
	if tag == "latest" {
		t.Skip("not a git checkout")
	}
	if !regexp.MustCompile(`^dev-[0-9a-f]{7,40}$`).MatchString(tag) {
		t.Errorf("DevImageTag() = %q, want dev-<short-sha> or latest", tag)
	}
}

func TestReadDevImageTag_FallbackWhenAbsent(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if got := ReadDevImageTag(cfg); got != "latest" {
		t.Errorf("ReadDevImageTag with no file = %q, want latest", got)
	}
}

func TestCopyInfrastructure_ProductionUsesCommitSHA(t *testing.T) {
	// Production path: rewrite placeholder → :GitCommit (digest skipped for
	// offline unit tests). Never :latest, never leave the placeholder.
	t.Setenv("OBOL_DEVELOPMENT", "")
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")
	prev := version.GitCommit
	version.GitCommit = "abc1234"
	t.Cleanup(func() { version.GitCommit = prev })

	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := CopyInfrastructure(cfg, backendK3s, "test-stack"); err != nil {
		t.Fatalf("CopyInfrastructure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "x402.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, base := range []string{
		"ghcr.io/obolnetwork/x402-verifier",
		"ghcr.io/obolnetwork/serviceoffer-controller",
		"ghcr.io/obolnetwork/job-broker",
	} {
		want := base + ":abc1234"
		if !strings.Contains(out, want) {
			t.Errorf("production missing %s", want)
		}
		if strings.Contains(out, base+":latest") {
			t.Errorf("production downgraded %s to :latest", base)
		}
		if strings.Contains(out, base+":"+images.PlaceholderTag) {
			t.Errorf("production left placeholder on %s", base)
		}
	}
}

func TestCopyInfrastructureRendersStackPlaceholders(t *testing.T) {
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")
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
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")
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
