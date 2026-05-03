package images

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/version"
)

// withVersion temporarily overrides the version package globals for one test.
// Restores them in t.Cleanup so subsequent tests see the package's natural
// state (whatever ldflags or defaults left behind).
func withVersion(t *testing.T, commit, dirty string) {
	t.Helper()
	prevCommit := version.GitCommit
	prevDirty := version.GitDirty
	version.GitCommit = commit
	version.GitDirty = dirty
	t.Cleanup(func() {
		version.GitCommit = prevCommit
		version.GitDirty = prevDirty
	})
}

func TestResolve_DevModeForcesLatest(t *testing.T) {
	// Even when GitCommit is set to a real SHA, OBOL_DEVELOPMENT=true must win.
	// The local-build path imports images into k3d as :latest, so the manifest
	// must reference :latest to actually pick up the local image.
	withVersion(t, "abc1234", "false")
	t.Setenv("OBOL_DEVELOPMENT", "true")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_UnknownCommitFallsBackToLatest(t *testing.T) {
	// Binaries built without ldflags leave GitCommit at "unknown". There's no
	// matching CI image tag, so :latest is the only safe choice.
	withVersion(t, "unknown", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_DirtyRepoFallsBackToLatest(t *testing.T) {
	// A dirty build has no published image — its commit doesn't match anything
	// on GHCR. Use :latest rather than producing a tag that 404s.
	withVersion(t, "abc1234", "true")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_ProductionUsesCommitPin(t *testing.T) {
	// Released binary: GitCommit is the short SHA, repo is clean, not in dev
	// mode. Result must be the commit-pinned tag — this is what makes binary
	// upgrades roll the K8s pods automatically.
	withVersion(t, "abc1234", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:abc1234"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_EmptyCommitFallsBackToLatest(t *testing.T) {
	withVersion(t, "", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/storefront")
	want := "ghcr.io/obolnetwork/storefront:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_DevModeCaseInsensitive(t *testing.T) {
	// People sometimes set OBOL_DEVELOPMENT=True or =TRUE. Don't penalise
	// them — the env var is binary-true, not a string match.
	withVersion(t, "abc1234", "false")

	for _, val := range []string{"true", "TRUE", "True", "tRuE"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("OBOL_DEVELOPMENT", val)
			if got := Resolve("img"); got != "img:latest" {
				t.Errorf("OBOL_DEVELOPMENT=%q: Resolve = %q, want img:latest", val, got)
			}
		})
	}
}
