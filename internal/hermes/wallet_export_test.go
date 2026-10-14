package hermes

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestBuildHostWalletBackup_ReportsKeystoreRoute(t *testing.T) {
	cfg, id, original := setupHermesBackupInstance(t, "route")

	backup, route, err := BuildHostWalletBackup(cfg, HermesHostWallet(cfg, id))
	if err != nil {
		t.Fatalf("build backup: %v", err)
	}
	if route != KeystoreRouteHost {
		t.Fatalf("route = %q, want %q", route, KeystoreRouteHost)
	}
	if got := backup.Wallets[0].Address; got != original.Address {
		t.Fatalf("address = %s, want %s", got, original.Address)
	}

	// Host copy owned by the remote-signer pod: read through the k3d node.
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, ".stack-backend"), []byte("k3d"), 0o600); err != nil {
		t.Fatal(err)
	}
	origRead, origNode := readHostKeystoreFileFn, k3dNodeExecOutputFn
	t.Cleanup(func() { readHostKeystoreFileFn, k3dNodeExecOutputFn = origRead, origNode })
	readHostKeystoreFileFn = func(path string) ([]byte, error) {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	k3dNodeExecOutputFn = func(*config.Config, string, string) ([]byte, error) {
		return []byte(`{"version":3}`), nil
	}
	if _, route, err = BuildHostWalletBackup(cfg, HermesHostWallet(cfg, id)); err != nil || route != KeystoreRouteK3dNode {
		t.Fatalf("route = %q, err = %v, want %q", route, err, KeystoreRouteK3dNode)
	}

	// Not k3d: no route, and the permission error survives for the caller.
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, ".stack-backend"), []byte("k3s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = BuildHostWalletBackup(cfg, HermesHostWallet(cfg, id)); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err = %v, want a permission error", err)
	}
}
