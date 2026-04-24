package agentruntime

import (
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

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
