package stackversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

func withVersion(t *testing.T, v string) {
	t.Helper()

	prev := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = prev })
}

func initStack(t *testing.T) *config.Config {
	t.Helper()

	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, stackIDFile), []byte("id"), 0o600); err != nil {
		t.Fatal(err)
	}

	return cfg
}

func TestSkew(t *testing.T) {
	withVersion(t, "0.17.1")

	if _, skewed := Skew(&config.Config{ConfigDir: t.TempDir()}); skewed {
		t.Fatal("no stack: no skew")
	}

	cfg := initStack(t)
	if v, skewed := Skew(cfg); !skewed || v != "" {
		t.Fatalf("stack without a record (earlier obol) = %q %v, want skew", v, skewed)
	}

	Record(cfg)
	if v, skewed := Skew(cfg); skewed || v != "0.17.1" {
		t.Fatalf("after Record = %q %v, want no skew", v, skewed)
	}

	withVersion(t, "0.17.2")
	v, skewed := Skew(cfg)
	if !skewed || v != "0.17.1" {
		t.Fatalf("newer CLI = %q %v, want skew from 0.17.1", v, skewed)
	}

	if msg := SkewMessage(v); !strings.Contains(msg, "obol 0.17.1") || !strings.Contains(msg, "obol stack up") {
		t.Errorf("message = %q", msg)
	}

	if msg := SkewMessage(""); !strings.Contains(msg, "an earlier obol release") {
		t.Errorf("message without a record = %q", msg)
	}

	withVersion(t, "dev")
	if _, skewed := Skew(cfg); skewed {
		t.Fatal("dev builds never report skew")
	}
}
