package stackbackup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func identityTestConfig(t *testing.T, stackID, backend string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if stackID != "" {
		if err := os.WriteFile(filepath.Join(dir, ".stack-id"), []byte(stackID+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if backend != "" {
		if err := os.WriteFile(filepath.Join(dir, ".stack-backend"), []byte(backend), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Config{ConfigDir: dir}
}

func stubNodes(t *testing.T, out string, err error) *int {
	t.Helper()
	calls := 0
	orig := listNodeNamesFn
	listNodeNamesFn = func(*config.Config) (string, error) { calls++; return out, err }
	t.Cleanup(func() { listNodeNamesFn = orig })
	return &calls
}

func TestVerifyClusterIdentity(t *testing.T) {
	t.Run("matching k3d cluster", func(t *testing.T) {
		stubNodes(t, "k3d-obol-stack-eternal-badger-server-0", nil)
		if err := verifyClusterIdentity(identityTestConfig(t, "eternal-badger", "k3d")); err != nil {
			t.Fatalf("expected match, got %v", err)
		}
	})

	// The real incident: prod's stale kubeconfig port was reused by the dev
	// stack's cluster, so export harvested dev agents under prod's stack ID.
	t.Run("other stack's cluster on a reused port", func(t *testing.T) {
		stubNodes(t, "k3d-obol-stack-secure-mustang-server-0", nil)
		err := verifyClusterIdentity(identityTestConfig(t, "eternal-badger", "k3d"))
		if err == nil {
			t.Fatal("expected mismatch error")
		}
		var mm *clusterMismatchError
		if !errors.As(err, &mm) || mm.OtherCluster != "obol-stack-secure-mustang" {
			t.Errorf("want clusterMismatchError naming obol-stack-secure-mustang, got %#v", err)
		}
		for _, want := range []string{"secure-mustang", `"eternal-badger"`, "k3d kubeconfig write obol-stack-eternal-badger"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q missing %q", err, want)
			}
		}
	})

	// A stack ID that is a prefix of another must not match it.
	t.Run("prefix stack id does not match longer id", func(t *testing.T) {
		stubNodes(t, "k3d-obol-stack-big-teal-fish-server-0", nil)
		if err := verifyClusterIdentity(identityTestConfig(t, "big-teal", "k3d")); err == nil {
			t.Fatal("big-teal must not match big-teal-fish's nodes")
		}
	})

	t.Run("missing backend file defaults to k3d", func(t *testing.T) {
		stubNodes(t, "k3d-obol-stack-other-server-0", nil)
		if err := verifyClusterIdentity(identityTestConfig(t, "eternal-badger", "")); err == nil {
			t.Fatal("expected mismatch error with default k3d backend")
		}
	})

	t.Run("k3s backend is not checked", func(t *testing.T) {
		calls := stubNodes(t, "my-laptop", nil)
		if err := verifyClusterIdentity(identityTestConfig(t, "eternal-badger", "k3s")); err != nil {
			t.Fatalf("k3s should skip the check, got %v", err)
		}
		if *calls != 0 {
			t.Error("k3s backend should not list nodes")
		}
	})

	t.Run("no stack id is not checked", func(t *testing.T) {
		calls := stubNodes(t, "anything", nil)
		if err := verifyClusterIdentity(identityTestConfig(t, "", "k3d")); err != nil {
			t.Fatalf("no stack id should skip the check, got %v", err)
		}
		if *calls != 0 {
			t.Error("missing stack id should not list nodes")
		}
	})

	t.Run("node listing failure is an error", func(t *testing.T) {
		stubNodes(t, "", errors.New("forbidden"))
		if err := verifyClusterIdentity(identityTestConfig(t, "eternal-badger", "k3d")); err == nil {
			t.Fatal("expected error when nodes cannot be listed")
		}
	})
}
