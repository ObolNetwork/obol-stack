package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// mkAppDeployment creates a directory with a values.yaml to simulate an app deployment.
func mkAppDeployment(t *testing.T, base, identifier string) {
	t.Helper()
	dir := filepath.Join(base, identifier)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "values.yaml"), []byte("# test"), 0644)
}

func TestListInstanceIDs(t *testing.T) {
	t.Run("no applications directory", func(t *testing.T) {
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
		os.MkdirAll(filepath.Join(cfg.ConfigDir, "applications"), 0755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("expected 0 instances, got %d", len(ids))
		}
	})

	t.Run("single app single deployment", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications")
		mkAppDeployment(t, base, "postgresql/eager-fox")

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 instance, got %d", len(ids))
		}
		if ids[0] != "postgresql/eager-fox" {
			t.Fatalf("expected 'postgresql/eager-fox', got '%s'", ids[0])
		}
	})

	t.Run("multiple apps multiple deployments", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications")
		mkAppDeployment(t, base, "postgresql/eager-fox")
		mkAppDeployment(t, base, "postgresql/prod")
		mkAppDeployment(t, base, "redis/staging")

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 3 {
			t.Fatalf("expected 3 instances, got %d", len(ids))
		}
	})

	t.Run("ignores non-directory entries", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications", "postgresql")
		mkAppDeployment(t, filepath.Join(cfg.ConfigDir, "applications"), "postgresql/eager-fox")
		os.WriteFile(filepath.Join(base, "some-file.txt"), []byte("test"), 0644)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 instance, got %d", len(ids))
		}
	})

	t.Run("skips directories without values.yaml", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications")
		mkAppDeployment(t, base, "postgresql/eager-fox")
		// Simulate an openclaw instance (directory exists but no values.yaml)
		os.MkdirAll(filepath.Join(base, "openclaw", "obol-agent"), 0755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 instance (openclaw excluded), got %d: %v", len(ids), ids)
		}
		if ids[0] != "postgresql/eager-fox" {
			t.Fatalf("expected 'postgresql/eager-fox', got '%s'", ids[0])
		}
	})
}

func TestResolveInstance(t *testing.T) {
	// setupInstances creates a temp config dir with the given "app/id" entries,
	// each containing a values.yaml to simulate real app deployments.
	setupInstances := func(t *testing.T, identifiers ...string) *config.Config {
		t.Helper()
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "applications")
		for _, id := range identifiers {
			mkAppDeployment(t, base, id)
		}
		return cfg
	}

	t.Run("zero instances returns error", func(t *testing.T) {
		cfg := setupInstances(t)
		_, _, err := ResolveInstance(cfg, []string{"postgresql/eager-fox"})
		if err == nil {
			t.Fatal("expected error for zero instances")
		}
		if got := err.Error(); got != "no app deployments found — run 'obol app install <chart>' to create one" {
			t.Fatalf("unexpected error: %s", got)
		}
	})

	t.Run("single instance auto-selects", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox")
		id, remaining, err := ResolveInstance(cfg, []string{"extra-arg"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "postgresql/eager-fox" {
			t.Fatalf("expected id 'postgresql/eager-fox', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra-arg" {
			t.Fatalf("expected remaining args [extra-arg], got %v", remaining)
		}
	})

	t.Run("single instance with no args", func(t *testing.T) {
		cfg := setupInstances(t, "redis/happy-otter")
		id, remaining, err := ResolveInstance(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "redis/happy-otter" {
			t.Fatalf("expected id 'redis/happy-otter', got '%s'", id)
		}
		if len(remaining) != 0 {
			t.Fatalf("expected no remaining args, got %v", remaining)
		}
	})

	t.Run("multiple instances with valid name", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox", "redis/staging")
		id, remaining, err := ResolveInstance(cfg, []string{"redis/staging", "extra"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "redis/staging" {
			t.Fatalf("expected id 'redis/staging', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra" {
			t.Fatalf("expected remaining [extra], got %v", remaining)
		}
	})

	t.Run("multiple instances without name errors", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox", "redis/staging")
		_, _, err := ResolveInstance(cfg, nil)
		if err == nil {
			t.Fatal("expected error for multiple instances without name")
		}
	})

	t.Run("multiple instances with unknown name errors", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox", "redis/staging")
		_, _, err := ResolveInstance(cfg, []string{"mysql/nonexistent"})
		if err == nil {
			t.Fatal("expected error for unknown instance name")
		}
	})

	t.Run("type prefix selects sole instance of that type", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox", "redis/staging")
		id, remaining, err := ResolveInstance(cfg, []string{"redis", "extra"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "redis/staging" {
			t.Fatalf("expected id 'redis/staging', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra" {
			t.Fatalf("expected remaining [extra], got %v", remaining)
		}
	})

	t.Run("type prefix errors when multiple of same type", func(t *testing.T) {
		cfg := setupInstances(t, "postgresql/eager-fox", "postgresql/prod")
		_, _, err := ResolveInstance(cfg, []string{"postgresql"})
		if err == nil {
			t.Fatal("expected error when type prefix matches multiple instances")
		}
	})
}
