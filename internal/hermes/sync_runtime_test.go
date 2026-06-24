package hermes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
)

// TestSyncRuntimeFiles_RestoresContainerOwnership guards the regression where
// syncRuntimeFiles chowned the Hermes home dir to the host UID (for host-side
// config/skill writes) but never handed ownership back to the container UID.
// On legacy hostPath PVs the kubelet does not re-apply fsGroup, so the missing
// chown-back left .hermes owned by the host user and the non-root pod's init
// container died with "mkdir: cannot create directory '/data/.hermes':
// Permission denied" on the next sync/restart (e.g. after `obol model setup`).
func TestSyncRuntimeFiles_RestoresContainerOwnership(t *testing.T) {
	cfg, _ := walletImportTestConfig(t, "obol-agent")
	calls := stubVolumeOwnership(t)

	if err := syncRuntimeFiles(cfg, "obol-agent", []byte("model_list: []\n"), newTestUI()); err != nil {
		t.Fatalf("syncRuntimeFiles: %v", err)
	}

	// Must restore ownership, and only after the host-side writes (ensure first).
	if len(*calls) != 2 || (*calls)[0] != "ensureVolumeWritable" || (*calls)[1] != "fixRuntimeVolumeOwnership" {
		t.Fatalf("expected [ensureVolumeWritable, fixRuntimeVolumeOwnership], got %v", *calls)
	}

	// Sanity: the config was actually written to the home dir.
	home := agentruntime.HomePath(cfg, agentruntime.Hermes, "obol-agent")
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}
}
