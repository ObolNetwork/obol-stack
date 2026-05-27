package network

import (
	"fmt"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// freeDiskBytes returns the free disk bytes available at path. Used to
// check whether a network install has room to grow before we let helmfile
// schedule a 4TB PVC that will silently fill the host overnight.
func freeDiskBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	// Bavail is reserved-block-aware (vs Bfree); use it for "what a regular
	// process can actually allocate".
	return stat.Bavail * uint64(stat.Bsize), nil
}

// CheckNetworkDiskSpace warns when the data directory has less free disk
// than the install is expected to need. The default answer is to continue:
// in non-interactive contexts (no TTY, JSON mode) the prompt auto-accepts
// so scripted installs don't deadlock. The user only blocks the install by
// explicitly declining at an interactive prompt.
func CheckNetworkDiskSpace(u *ui.UI, dataDir, network, mode, executionClient string, scope ArchiveScope) error {
	profile := resolveEthereumStorageProfile(network, mode, executionClient, scope)
	requiredGB := profile.DiskRequirementGB

	freeBytes, err := freeDiskBytes(dataDir)
	if err != nil {
		// Best-effort: a statfs failure shouldn't block install.
		u.Warnf("Could not check free disk space at %s: %v", dataDir, err)
		return nil
	}

	freeGB := freeBytes / (1024 * 1024 * 1024)

	u.Detail("Disk space", fmt.Sprintf("%d GB free at %s (this network needs ~%d GB for %s)", freeGB, dataDir, requiredGB, profile.Label))

	if freeGB >= requiredGB {
		return nil
	}

	u.Warnf("Low disk space: %d GB free, ~%d GB recommended for %s/%s",
		freeGB, requiredGB, network, mode)
	if mode != "archive" {
		u.Dim("  (full mode is the lighter option; archive mode would need ~4-5 TB on mainnet)")
	}

	if !u.Confirm("Continue with install anyway?", true) {
		return fmt.Errorf("install cancelled: insufficient disk space (%d GB free, ~%d GB recommended)", freeGB, requiredGB)
	}

	return nil
}
