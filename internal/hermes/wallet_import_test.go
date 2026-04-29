package hermes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Anvil account #1 — well-known Hardhat test mnemonic. Public on every
// install, never used on mainnet, safe to commit.
const testPrivateKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func newTestUI() *ui.UI { return ui.New(false) }

func walletImportTestConfig(t *testing.T, id string) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir, DataDir: dir, BinDir: dir}
	deployDir := DeploymentPath(cfg, id)
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	keystoreDir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id)
	if err := os.MkdirAll(keystoreDir, 0o700); err != nil {
		t.Fatalf("mkdir keystore: %v", err)
	}
	return cfg, deployDir
}

func writeKeyFile(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "key.hex")
	if err := os.WriteFile(f, []byte(testPrivateKeyHex), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return f
}

// stubVolumeOwnership replaces the k3d-node-exec helpers with no-op spies that
// record call order. Tests use this to assert the bookend ordering inside
// archiveReplacedHermesKeystore without needing a real cluster.
func stubVolumeOwnership(t *testing.T) *[]string {
	t.Helper()
	calls := []string{}
	origEnsure := ensureVolumeWritableFn
	origFix := fixRuntimeVolumeOwnershipFn
	t.Cleanup(func() {
		ensureVolumeWritableFn = origEnsure
		fixRuntimeVolumeOwnershipFn = origFix
	})
	ensureVolumeWritableFn = func(_ *config.Config, _ string, _ *ui.UI) {
		calls = append(calls, "ensureVolumeWritable")
	}
	fixRuntimeVolumeOwnershipFn = func(_ *config.Config, _ string, _ *ui.UI) {
		calls = append(calls, "fixRuntimeVolumeOwnership")
	}
	return &calls
}

// stubClusterApply replaces Sync + restartHermesRemoteSigner with spies that
// record invocation. Sync's return value is configurable so tests can drive
// the success and failure branches independently.
func stubClusterApply(t *testing.T, syncErr error) (syncCalled, restartCalled *bool) {
	t.Helper()
	sc, rc := false, false
	origSync := syncFn
	origRestart := restartHermesRemoteSignerFn
	t.Cleanup(func() {
		syncFn = origSync
		restartHermesRemoteSignerFn = origRestart
	})
	syncFn = func(_ *config.Config, _ string, _ *ui.UI) error {
		sc = true
		return syncErr
	}
	restartHermesRemoteSignerFn = func(_ *config.Config, _ string, _ *ui.UI) {
		rc = true
	}
	return &sc, &rc
}

// TestArchiveReplacedHermesKeystore_NilExisting guards the happy path: no
// previous wallet on disk → archive is a no-op and must not call the
// k3d-node-exec helpers.
func TestArchiveReplacedHermesKeystore_NilExisting(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	calls := stubVolumeOwnership(t)
	if err := archiveReplacedHermesKeystore(cfg, "obol-agent", nil, "new-uuid", newTestUI()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected zero ownership calls, got %v", *calls)
	}
}

// TestArchiveReplacedHermesKeystore_SameUUID covers the case where the import
// re-uses the existing keystore UUID (a re-import idempotency case). Archive
// must short-circuit before any filesystem mutation.
func TestArchiveReplacedHermesKeystore_SameUUID(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	calls := stubVolumeOwnership(t)
	existing := &WalletInfo{KeystoreUUID: "same-uuid"}
	if err := archiveReplacedHermesKeystore(cfg, "obol-agent", existing, "same-uuid", newTestUI()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected zero ownership calls, got %v", *calls)
	}
}

// TestArchiveReplacedHermesKeystore_BookendOrder is the regression guard for
// the fix in commit 6c5106a: the bookend pair MUST run in the order
// (ensure → … → fix), and the deferred fix MUST run even when the function
// returns early because the old keystore file is missing.
func TestArchiveReplacedHermesKeystore_BookendOrder(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	calls := stubVolumeOwnership(t)
	// existingWallet points at a UUID whose file does not exist on disk →
	// archive returns early after the os.Stat ENOENT branch. Even on that
	// early-return path the deferred fix call must still fire.
	existing := &WalletInfo{KeystoreUUID: "missing-uuid"}
	if err := archiveReplacedHermesKeystore(cfg, "obol-agent", existing, "new-uuid", newTestUI()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(*calls) != 2 || (*calls)[0] != "ensureVolumeWritable" || (*calls)[1] != "fixRuntimeVolumeOwnership" {
		t.Fatalf("expected [ensureVolumeWritable, fixRuntimeVolumeOwnership], got %v", *calls)
	}
}

// TestArchiveReplacedHermesKeystore_RenamesToReplaced exercises the happy
// path: a real keystore file exists in the volume, archive renames it under
// the `replaced/` subdir with a timestamp suffix, and the original path no
// longer exists.
func TestArchiveReplacedHermesKeystore_RenamesToReplaced(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	stubVolumeOwnership(t)
	dir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, "obol-agent")
	oldUUID := "old-uuid-aaaa"
	oldPath := filepath.Join(dir, oldUUID+".json")
	if err := os.WriteFile(oldPath, []byte(`{"placeholder":"keystore"}`), 0o600); err != nil {
		t.Fatalf("write old keystore: %v", err)
	}

	existing := &WalletInfo{KeystoreUUID: oldUUID}
	if err := archiveReplacedHermesKeystore(cfg, "obol-agent", existing, "new-uuid", newTestUI()); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old keystore should be gone, got err=%v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "replaced"))
	if err != nil {
		t.Fatalf("read replaced dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 archived keystore, got %d", len(entries))
	}
}

// TestImportPrivateKeyWalletCmd_ApplyClusterFalseSkipsCluster guards the
// inverse direction of the ApplyCluster wiring (a214050): when the caller
// did not opt in to cluster-side apply (e.g. pre-cluster-up bootstrap), we
// must NOT call helmfile sync or rollout-restart.
func TestImportPrivateKeyWalletCmd_ApplyClusterFalseSkipsCluster(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	stubVolumeOwnership(t)
	syncCalled, restartCalled := stubClusterApply(t, nil)

	keyFile := writeKeyFile(t)
	if err := ImportPrivateKeyWalletCmd(cfg, "obol-agent", ImportPrivateKeyWalletOptions{
		PrivateKeyFile: keyFile,
		Force:          true,
		ApplyCluster:   false,
	}, newTestUI()); err != nil {
		t.Fatalf("import: %v", err)
	}
	if *syncCalled {
		t.Fatal("Sync was called even though ApplyCluster=false")
	}
	if *restartCalled {
		t.Fatal("restartHermesRemoteSigner was called even though ApplyCluster=false")
	}
}

// TestImportPrivateKeyWalletCmd_ApplyClusterTrueRollsPod is the regression
// guard for the pair of fixes in PR #397: ApplyCluster=true MUST trigger
// helmfile-sync AND the explicit rollout-restart that follows on success
// (helm doesn't roll on Secret-data-only changes, so without the restart
// the pod keeps signing with the chart's bootstrap address).
func TestImportPrivateKeyWalletCmd_ApplyClusterTrueRollsPod(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	stubVolumeOwnership(t)
	syncCalled, restartCalled := stubClusterApply(t, nil)

	keyFile := writeKeyFile(t)
	if err := ImportPrivateKeyWalletCmd(cfg, "obol-agent", ImportPrivateKeyWalletOptions{
		PrivateKeyFile: keyFile,
		Force:          true,
		ApplyCluster:   true,
	}, newTestUI()); err != nil {
		t.Fatalf("import: %v", err)
	}
	if !*syncCalled {
		t.Fatal("Sync was NOT called with ApplyCluster=true")
	}
	if !*restartCalled {
		t.Fatal("restartHermesRemoteSigner was NOT called after successful Sync")
	}
}

// TestImportPrivateKeyWalletCmd_SyncFailureSkipsRestart documents the
// best-effort contract: if helmfile-sync errored, do NOT proceed to
// rollout-restart (it would just roll the pod against a stale Secret).
// The wallet import as a whole still succeeds — the on-disk artifacts
// (wallet.json, keystore, values-remote-signer.yaml) are written, so a
// later `obol hermes sync` can finish the job.
func TestImportPrivateKeyWalletCmd_SyncFailureSkipsRestart(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	stubVolumeOwnership(t)
	syncCalled, restartCalled := stubClusterApply(t, errSyncFailed)

	keyFile := writeKeyFile(t)
	if err := ImportPrivateKeyWalletCmd(cfg, "obol-agent", ImportPrivateKeyWalletOptions{
		PrivateKeyFile: keyFile,
		Force:          true,
		ApplyCluster:   true,
	}, newTestUI()); err != nil {
		t.Fatalf("import should be best-effort, got hard error: %v", err)
	}
	if !*syncCalled {
		t.Fatal("Sync was NOT called")
	}
	if *restartCalled {
		t.Fatal("restartHermesRemoteSigner ran even though Sync failed")
	}
}

// errSyncFailed is a sentinel error used by tests to drive the
// helmfile-sync-failed branch of ImportPrivateKeyWalletCmd.
var errSyncFailed = &syncErr{msg: "test: sync failed"}

type syncErr struct{ msg string }

func (e *syncErr) Error() string { return e.msg }
