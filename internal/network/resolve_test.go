package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestListInstanceIDs(t *testing.T) {
	t.Run("no networks directory", func(t *testing.T) {
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
		os.MkdirAll(filepath.Join(cfg.ConfigDir, "networks"), 0755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("expected 0 instances, got %d", len(ids))
		}
	})

	t.Run("single network single deployment", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		os.MkdirAll(filepath.Join(cfg.ConfigDir, "networks", "ethereum", "my-node"), 0755)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 instance, got %d", len(ids))
		}
		if ids[0] != "ethereum/my-node" {
			t.Fatalf("expected 'ethereum/my-node', got '%s'", ids[0])
		}
	})

	t.Run("multiple networks multiple deployments", func(t *testing.T) {
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "networks")
		os.MkdirAll(filepath.Join(base, "ethereum", "my-node"), 0755)
		os.MkdirAll(filepath.Join(base, "ethereum", "hoodi-prod"), 0755)
		os.MkdirAll(filepath.Join(base, "aztec", "testnet"), 0755)

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
		base := filepath.Join(cfg.ConfigDir, "networks", "ethereum")
		os.MkdirAll(filepath.Join(base, "my-node"), 0755)
		os.WriteFile(filepath.Join(base, "some-file.txt"), []byte("test"), 0644)

		ids, err := ListInstanceIDs(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ids) != 1 {
			t.Fatalf("expected 1 instance, got %d", len(ids))
		}
	})
}

func TestResolveInstance(t *testing.T) {
	// setupInstances creates a temp config dir with the given "network/id" entries.
	setupInstances := func(t *testing.T, identifiers ...string) *config.Config {
		t.Helper()
		cfg := &config.Config{ConfigDir: t.TempDir()}
		base := filepath.Join(cfg.ConfigDir, "networks")
		for _, id := range identifiers {
			os.MkdirAll(filepath.Join(base, id), 0755)
		}
		return cfg
	}

	t.Run("zero instances returns error", func(t *testing.T) {
		cfg := setupInstances(t)
		_, _, err := ResolveInstance(cfg, []string{"ethereum/my-node"})
		if err == nil {
			t.Fatal("expected error for zero instances")
		}
		if got := err.Error(); got != "no network deployments found — run 'obol network install <network>' to create one" {
			t.Fatalf("unexpected error: %s", got)
		}
	})

	t.Run("single instance auto-selects", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node")
		id, remaining, err := ResolveInstance(cfg, []string{"extra-arg"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "ethereum/my-node" {
			t.Fatalf("expected id 'ethereum/my-node', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra-arg" {
			t.Fatalf("expected remaining args [extra-arg], got %v", remaining)
		}
	})

	t.Run("single instance with no args", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/happy-otter")
		id, remaining, err := ResolveInstance(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "ethereum/happy-otter" {
			t.Fatalf("expected id 'ethereum/happy-otter', got '%s'", id)
		}
		if len(remaining) != 0 {
			t.Fatalf("expected no remaining args, got %v", remaining)
		}
	})

	t.Run("multiple instances with valid name", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node", "ethereum/hoodi-prod")
		id, remaining, err := ResolveInstance(cfg, []string{"ethereum/hoodi-prod", "extra"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "ethereum/hoodi-prod" {
			t.Fatalf("expected id 'ethereum/hoodi-prod', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra" {
			t.Fatalf("expected remaining [extra], got %v", remaining)
		}
	})

	t.Run("multiple instances without name errors", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node", "aztec/testnet")
		_, _, err := ResolveInstance(cfg, nil)
		if err == nil {
			t.Fatal("expected error for multiple instances without name")
		}
	})

	t.Run("multiple instances with unknown name errors", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node", "aztec/testnet")
		_, _, err := ResolveInstance(cfg, []string{"helios/nonexistent"})
		if err == nil {
			t.Fatal("expected error for unknown instance name")
		}
	})

	t.Run("type prefix selects sole instance of that type", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node", "aztec/testnet")
		id, remaining, err := ResolveInstance(cfg, []string{"ethereum", "extra"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "ethereum/my-node" {
			t.Fatalf("expected id 'ethereum/my-node', got '%s'", id)
		}
		if len(remaining) != 1 || remaining[0] != "extra" {
			t.Fatalf("expected remaining [extra], got %v", remaining)
		}
	})

	t.Run("type prefix errors when multiple of same type", func(t *testing.T) {
		cfg := setupInstances(t, "ethereum/my-node", "ethereum/hoodi-prod")
		_, _, err := ResolveInstance(cfg, []string{"ethereum"})
		if err == nil {
			t.Fatal("expected error when type prefix matches multiple instances")
		}
	})
}
