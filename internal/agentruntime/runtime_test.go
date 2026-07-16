package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// TestMaxIDLengthKeepsDerivedLabelsWithinDNSLimit guards the DNS-label
// overflow finding: an id at MaxIDLength must keep every label this package
// derives (Namespace/Hostname, and for Hermes DashboardHostname's "-ui"
// suffix) at or under the 63-character RFC 1123 limit.
func TestMaxIDLengthKeepsDerivedLabelsWithinDNSLimit(t *testing.T) {
	for _, rt := range []Runtime{OpenClaw, Hermes} {
		id := strings.Repeat("a", MaxIDLength(rt))

		if n := len(Namespace(rt, id)); n > 63 {
			t.Errorf("%s: Namespace(%q) label is %d chars, want <=63", rt, id, n)
		}
		if n := len(strings.SplitN(Hostname(rt, id), ".", 2)[0]); n > 63 {
			t.Errorf("%s: Hostname(%q) label is %d chars, want <=63", rt, id, n)
		}
		if n := len(strings.SplitN(DashboardHostname(rt, id), ".", 2)[0]); n > 63 {
			t.Errorf("%s: DashboardHostname(%q) label is %d chars, want <=63", rt, id, n)
		}

		// One character longer must overflow at least one derived label —
		// otherwise MaxIDLength is too conservative, not just safe.
		tooLong := id + "a"
		overflow := len(Namespace(rt, tooLong)) > 63 ||
			len(strings.SplitN(Hostname(rt, tooLong), ".", 2)[0]) > 63 ||
			len(strings.SplitN(DashboardHostname(rt, tooLong), ".", 2)[0]) > 63
		if !overflow {
			t.Errorf("%s: MaxIDLength+1 (%d chars) did not overflow any derived DNS label", rt, len(tooLong))
		}
	}
}

func TestHermesPaths(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: "/tmp/obol-config",
		DataDir:   "/tmp/obol-data",
	}

	if got, want := DeploymentPath(cfg, Hermes, DefaultInstanceID), filepath.Join("/tmp/obol-config", "applications", "hermes", DefaultInstanceID); got != want {
		t.Fatalf("DeploymentPath() = %q, want %q", got, want)
	}

	if got, want := Namespace(Hermes, DefaultInstanceID), "hermes-obol-agent"; got != want {
		t.Fatalf("Namespace() = %q, want %q", got, want)
	}

	if got, want := Hostname(Hermes, DefaultInstanceID), "hermes-obol-agent.obol.stack"; got != want {
		t.Fatalf("Hostname() = %q, want %q", got, want)
	}

	if got, want := HomePath(cfg, Hermes, DefaultInstanceID), filepath.Join("/tmp/obol-data", "hermes-obol-agent", "hermes-data", ".hermes"); got != want {
		t.Fatalf("HomePath() = %q, want %q", got, want)
	}
}

func TestDashboardHostname(t *testing.T) {
	tests := []struct {
		name    string
		runtime Runtime
		id      string
		want    string
	}{
		{
			name:    "default hermes",
			runtime: Hermes,
			id:      DefaultInstanceID,
			want:    "obol-agent.obol.stack",
		},
		{
			name:    "named hermes",
			runtime: Hermes,
			id:      "alice",
			want:    "hermes-alice-ui.obol.stack",
		},
		{
			name:    "openclaw",
			runtime: OpenClaw,
			id:      "default",
			want:    "openclaw-default.obol.stack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DashboardHostname(tt.runtime, tt.id); got != tt.want {
				t.Fatalf("DashboardHostname(%q, %q) = %q, want %q", tt.runtime, tt.id, got, tt.want)
			}
		})
	}
}

func TestCollectHostnamesIncludesHermesDashboardsAndOpenClawInstances(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
	}

	for _, path := range []string{
		DeploymentPath(cfg, Hermes, "alice"),
		DeploymentPath(cfg, Hermes, DefaultInstanceID),
		DeploymentPath(cfg, OpenClaw, "default"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
	}

	got := CollectHostnames(cfg, DeploymentRef{
		Runtime: Hermes,
		ID:      "bob",
	})

	want := []string{
		"hermes-bob.obol.stack",
		"hermes-bob-ui.obol.stack",
		"hermes-alice.obol.stack",
		"hermes-alice-ui.obol.stack",
		"hermes-obol-agent.obol.stack",
		"obol-agent.obol.stack",
		"openclaw-default.obol.stack",
	}

	assertSameStringSet(t, got, want)
}

func TestCollectHostnamesDeduplicatesIncludedDeployment(t *testing.T) {
	cfg := &config.Config{
		ConfigDir: t.TempDir(),
		DataDir:   t.TempDir(),
	}

	if err := os.MkdirAll(DeploymentPath(cfg, Hermes, "alice"), 0o755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}

	got := CollectHostnames(cfg, DeploymentRef{
		Runtime: Hermes,
		ID:      "alice",
	})

	want := []string{
		"hermes-alice.obol.stack",
		"hermes-alice-ui.obol.stack",
	}

	assertSameStringSet(t, got, want)
}

func assertSameStringSet(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d hostnames %v, want %d %v", len(got), got, len(want), want)
	}

	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}

	for _, value := range want {
		if seen[value] != 1 {
			t.Fatalf("hostname %q count = %d in %v, want 1", value, seen[value], got)
		}
	}
}
