package defaults

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestCopyInfrastructure_DevModeRewritesDigestPins(t *testing.T) {
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

	// The dev rewrite swaps the published digest/SHA pins for the per-commit
	// dev tag (dev-<sha>, or :latest when not a git checkout). In CI this is a
	// git checkout, so expect dev-<sha>.
	devTag := DevImageTag()

	// Every locally-built image must have lost its @sha256: pin and gained the
	// dev tag, otherwise the cluster pulls a stale ghcr.io binary even when
	// OBOL_DEVELOPMENT=true rebuilt the image locally.
	for _, base := range devLocallyBuiltImageBases {
		if strings.Contains(out, base+"@sha256:") {
			t.Errorf("dev mode left digest pin on %s in %s", base, x402Path)
		}
	}
	for _, base := range []string{
		"ghcr.io/obolnetwork/x402-verifier",
		"ghcr.io/obolnetwork/serviceoffer-controller",
		"ghcr.io/obolnetwork/x402-escrow",
	} {
		want := base + ":" + devTag
		if !strings.Contains(out, want) {
			t.Errorf("dev mode did not rewrite to %q in %s", want, x402Path)
		}
		if strings.Contains(out, base+":"+devTag+"@sha256:") {
			t.Errorf("dev mode left orphan @sha256: suffix on %s:%s in %s — regex missed the combo form", base, devTag, x402Path)
		}
	}

	// The persisted dev tag MUST equal what was stamped into the manifests, or
	// internal/stack would build/import a tag the cluster doesn't pin.
	if got := ReadDevImageTag(cfg); got != devTag {
		t.Errorf("persisted dev image tag = %q, want %q", got, devTag)
	}

	// Combo tag+digest form (used by x402-buyer in llm.yaml) must be
	// rewritten to a clean `:<devTag>` with no stale `@sha256:` suffix.
	// Regression guard for the bug where the old regex matched only
	// the `:b13254e` part and left `@sha256:...` behind, causing Docker
	// to silently pull the registry-pinned image instead of the local
	// build (root cause of flow-11 step 43 unable-to-debug regression
	// in May 2026 release-smoke).
	llmPath := filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "llm.yaml")
	llmData, err := os.ReadFile(llmPath)
	if err != nil {
		t.Fatalf("read llm.yaml: %v", err)
	}
	llmOut := string(llmData)
	buyer := "ghcr.io/obolnetwork/x402-buyer"
	if !strings.Contains(llmOut, buyer+":"+devTag) {
		t.Errorf("dev mode did not rewrite x402-buyer to :%s in %s", devTag, llmPath)
	}
	if strings.Contains(llmOut, buyer+":"+devTag+"@sha256:") {
		t.Errorf("dev mode left orphan @sha256: suffix on x402-buyer:%s in %s — regex missed the combo form", devTag, llmPath)
	}
	if strings.Contains(llmOut, buyer+"@sha256:") {
		t.Errorf("dev mode left @sha256: digest pin on x402-buyer in %s — regex missed it", llmPath)
	}
}

// TestRewriteDevDigestPins_ComboFormAllBases pins the rewrite behaviour for
// every locally-built base — including ghcr.io/obolnetwork/x402-escrow —
// against all three pin styles, with the combo `<tag>@sha256:<digest>` form
// exercised explicitly. The embedded manifests don't carry every base in
// every style (x402-escrow ships tag-only until the first publish), so this
// synthetic file guarantees a future digest bump can't resurrect the
// orphan-@sha256 bug for a base the real tree happens not to cover today.
func TestRewriteDevDigestPins_ComboFormAllBases(t *testing.T) {
	dir := t.TempDir()

	digest := strings.Repeat("ab12", 16) // 64 hex chars
	var lines []string
	for _, base := range devLocallyBuiltImageBases {
		lines = append(lines,
			"image: "+base+":b13254e@sha256:"+digest, // combo tag+digest
			"image: "+base+"@sha256:"+digest,         // digest-only
			"image: "+base+":b13254e",                // short-SHA tag
		)
	}
	path := filepath.Join(dir, "synthetic.yaml")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write synthetic manifest: %v", err)
	}

	if err := rewriteDevDigestPins(dir, "dev-test"); err != nil {
		t.Fatalf("rewriteDevDigestPins: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten manifest: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "@sha256:") {
		t.Errorf("rewrite left a @sha256: pin behind (orphan-suffix combo bug):\n%s", out)
	}
	for _, base := range devLocallyBuiltImageBases {
		want := "image: " + base + ":dev-test"
		if got := strings.Count(out, want); got != 3 {
			t.Errorf("base %s: %d of 3 pin styles rewritten to %q:\n%s", base, got, want, out)
		}
	}
}

func TestDevImageTag_Format(t *testing.T) {
	// Tests run inside the git checkout, so expect dev-<sha>; tolerate the
	// :latest fallback for non-git build environments.
	tag := DevImageTag()
	if tag == "latest" {
		t.Skip("not a git checkout (DevImageTag fell back to latest) — nothing to assert")
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

func TestCopyInfrastructure_ProductionPreservesImagePins(t *testing.T) {
	// Without OBOL_DEVELOPMENT=true, the immutable image pins must
	// survive untouched. A regression here would silently downgrade prod
	// installs to floating :latest tags. We accept either pin style
	// (digest or short-SHA tag) — both are immutable, and the rewrite
	// path under OBOL_DEVELOPMENT now handles both equivalently.
	t.Setenv("OBOL_DEVELOPMENT", "")

	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := CopyInfrastructure(cfg, backendK3s, "test-stack"); err != nil {
		t.Fatalf("CopyInfrastructure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "defaults", "base", "templates", "x402.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, "ghcr.io/obolnetwork/serviceoffer-controller:latest") {
		t.Error("production install downgraded serviceoffer-controller to :latest")
	}
	hasDigest := strings.Contains(out, "ghcr.io/obolnetwork/serviceoffer-controller@sha256:")
	hasShortTag := pinnedShortTag.MatchString(out)
	if !hasDigest && !hasShortTag {
		t.Error("production install lost serviceoffer-controller immutable pin (expected digest or short-SHA tag)")
	}
}

// pinnedShortTag matches a serviceoffer-controller tag of 7-40 hex chars
// (git short SHA). Used by the production-pin test to accept either of
// the two pinning styles the dev-mode rewriter knows about.
var pinnedShortTag = regexp.MustCompile(`ghcr\.io/obolnetwork/serviceoffer-controller:[a-f0-9]{7,40}\b`)

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
