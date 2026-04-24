package openclaw

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestListInstanceIDs(t *testing.T) {
	t.Run("no instances directory", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 0 {
			t.Fatalf("expected 0 instances, got %d", len(ids))
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		os.MkdirAll(filepath.Join(cfg.ConfigDir, "applications", "openclaw"), 0o755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 0 {
			t.Fatalf("expected 0 instances, got %d", len(ids))
		}
	})

	t.Run("multiple instances", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications", "openclaw")
		os.MkdirAll(filepath.Join(base, "default"), 0o755)
		os.MkdirAll(filepath.Join(base, "prod"), 0o755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ids) != 2 {
			t.Fatalf("expected 2 instances, got %d", len(ids))
		}
	})
}

func TestResolveInstance(t *testing.T) {
	setupInstances := func(t *testing.T, names ...string) *config.Config {
		t.Helper()
		cfg := &config.Config{ConfigDir: t.TempDir()}

		base := filepath.Join(cfg.ConfigDir, "applications", "openclaw")
		for _, name := range names {
			os.MkdirAll(filepath.Join(base, name), 0o755)
		}

		return cfg
	}

	t.Run("zero instances returns error", func(t *testing.T) {
		cfg := setupInstances(t)

		_, _, err := ResolveInstance(cfg, []string{"sync"})
		if err == nil {
			t.Fatal("expected error for zero instances")
		}

		if got := err.Error(); got != "no OpenClaw instances found — run 'obol openclaw onboard' to create one" {
			t.Fatalf("unexpected error: %s", got)
		}
	})

	t.Run("single instance auto-selects", func(t *testing.T) {
		cfg := setupInstances(t, "default")

		id, remaining, err := ResolveInstance(cfg, []string{"extra-arg"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if id != "default" {
			t.Fatalf("expected id 'default', got '%s'", id)
		}

		if len(remaining) != 1 || remaining[0] != "extra-arg" {
			t.Fatalf("expected remaining args [extra-arg], got %v", remaining)
		}
	})

	t.Run("single instance with no args", func(t *testing.T) {
		cfg := setupInstances(t, "happy-otter")

		id, remaining, err := ResolveInstance(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if id != "happy-otter" {
			t.Fatalf("expected id 'happy-otter', got '%s'", id)
		}

		if len(remaining) != 0 {
			t.Fatalf("expected no remaining args, got %v", remaining)
		}
	})

	t.Run("multiple instances with valid name", func(t *testing.T) {
		cfg := setupInstances(t, "default", "prod")

		id, remaining, err := ResolveInstance(cfg, []string{"prod", "extra"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if id != "prod" {
			t.Fatalf("expected id 'prod', got '%s'", id)
		}

		if len(remaining) != 1 || remaining[0] != "extra" {
			t.Fatalf("expected remaining [extra], got %v", remaining)
		}
	})

	t.Run("multiple instances without name errors", func(t *testing.T) {
		cfg := setupInstances(t, "default", "prod")

		_, _, err := ResolveInstance(cfg, nil)
		if err == nil {
			t.Fatal("expected error for multiple instances without name")
		}
	})

	t.Run("multiple instances with unknown name errors", func(t *testing.T) {
		cfg := setupInstances(t, "default", "prod")

		_, _, err := ResolveInstance(cfg, []string{"nonexistent"})
		if err == nil {
			t.Fatal("expected error for unknown instance name")
		}
	})
}
