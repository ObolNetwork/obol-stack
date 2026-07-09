package images

import (
	"strings"
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
	// Resolve itself returns :latest under OBOL_DEVELOPMENT; CopyInfrastructure
	// uses ResolveDev with the per-commit dev tag instead.
	withVersion(t, "abc1234", "false")
	t.Setenv("OBOL_DEVELOPMENT", "true")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_UnknownCommitFallsBackToLatest(t *testing.T) {
	withVersion(t, "unknown", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_DirtyRepoFallsBackToLatest(t *testing.T) {
	withVersion(t, "abc1234", "true")
	t.Setenv("OBOL_DEVELOPMENT", "")

	got := Resolve("ghcr.io/obolnetwork/demo-server")
	want := "ghcr.io/obolnetwork/demo-server:latest"
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolve_ProductionUsesCommitPin(t *testing.T) {
	// Released binary: GitCommit is the short SHA. Digest fetch is skipped so
	// the unit test is offline-deterministic; production apply binds digests.
	withVersion(t, "abc1234", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")

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

func TestResolveDev(t *testing.T) {
	got := ResolveDev("ghcr.io/obolnetwork/x402-verifier", "dev-deadbeef")
	want := "ghcr.io/obolnetwork/x402-verifier:dev-deadbeef"
	if got != want {
		t.Errorf("ResolveDev = %q, want %q", got, want)
	}
}

func TestRewriteBytes_AllPinForms(t *testing.T) {
	// Every historical pin form in the templates must collapse to the
	// resolved ref — including the combo form that left a stray @sha256
	// suffix when the alternation order was wrong (pitfall #12).
	replacers := BuildReplacers(func(repo string) string {
		return repo + ":dev-abc"
	})
	in := strings.Join([]string{
		"image: ghcr.io/obolnetwork/x402-verifier:656e5f6@sha256:bf209f108afefd58b542ae0fe4b92e3ce59b14d3718f641c34080033f3197178",
		"image: ghcr.io/obolnetwork/serviceoffer-controller@sha256:c4f7320e3af65b6d7775c55c4cb257488ecc69ced16130891165423105842a24",
		"image: ghcr.io/obolnetwork/x402-buyer:b13254e",
		"image: ghcr.io/obolnetwork/job-broker:" + PlaceholderTag,
		"image: ghcr.io/obolnetwork/demo-server:latest",
		// third-party / unmanaged must be left alone
		"image: ghcr.io/obolnetwork/litellm:v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}, "\n")
	out := string(RewriteBytes([]byte(in), replacers))

	for _, base := range []string{
		"ghcr.io/obolnetwork/x402-verifier",
		"ghcr.io/obolnetwork/serviceoffer-controller",
		"ghcr.io/obolnetwork/x402-buyer",
		"ghcr.io/obolnetwork/job-broker",
		"ghcr.io/obolnetwork/demo-server",
	} {
		want := base + ":dev-abc"
		if !strings.Contains(out, want) {
			t.Errorf("missing rewrite to %s\n%s", want, out)
		}
		if strings.Contains(out, base+"@sha256:") {
			t.Errorf("left digest pin on %s\n%s", base, out)
		}
		if strings.Contains(out, base+":dev-abc@sha256:") {
			t.Errorf("left orphan @sha256 after rewrite on %s\n%s", base, out)
		}
	}
	if !strings.Contains(out, "ghcr.io/obolnetwork/litellm:v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("unmanaged litellm pin was rewritten:\n%s", out)
	}
}

func TestStampIdentity(t *testing.T) {
	withVersion(t, "abc1234", "false")
	t.Setenv("OBOL_DEVELOPMENT", "")
	if got := StampIdentity(); got != "sha:abc1234" {
		t.Errorf("StampIdentity = %q, want sha:abc1234", got)
	}
	t.Setenv("OBOL_DEVELOPMENT", "true")
	if got := StampIdentity(); got != "dev" {
		t.Errorf("StampIdentity = %q, want dev", got)
	}
}
