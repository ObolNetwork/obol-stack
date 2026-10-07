package kubectl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// LocalPathStorageClass is the default StorageClass rendered by the base
// release (internal/embed/infrastructure/base/templates/local-path.yaml).
const LocalPathStorageClass = "local-path"

// MigrateLocalPathStorageClass deletes a stale local-path StorageClass so the
// following base-release helm sync recreates it with the current parameters.
//
// local-path-provisioner >= v0.0.33 rejects our pathPattern unless the class
// carries allowUnsafePathPattern: "true". StorageClass parameters are
// immutable, so a helm upgrade cannot patch a class created by an older
// release: the update is rejected and new PVCs stay Pending forever.
// Deleting a StorageClass does not touch existing PVs/PVCs (the provisioner
// only reads the class when provisioning new volumes), and Helm 3's upgrade
// recreates a release resource that is missing from the cluster
// (kube.Client.Update creates on NotFound), so the caller MUST run the base
// release sync right after this.
//
// Returns true when the class was deleted. A missing class (fresh cluster)
// is not an error.
func MigrateLocalPathStorageClass(cfg *config.Config) (bool, error) {
	bin, kc := Paths(cfg)

	out, err := Output(bin, kc, "get", "storageclass", LocalPathStorageClass,
		"-o", "json", "--ignore-not-found")
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return false, nil
		}
		return false, fmt.Errorf("get storageclass %s: %w", LocalPathStorageClass, err)
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}

	var sc struct {
		Parameters map[string]string `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(out), &sc); err != nil {
		return false, fmt.Errorf("parse storageclass %s: %w", LocalPathStorageClass, err)
	}
	if sc.Parameters["allowUnsafePathPattern"] == "true" {
		return false, nil
	}

	if err := RunSilent(bin, kc, "delete", "storageclass", LocalPathStorageClass,
		"--ignore-not-found"); err != nil {
		return false, fmt.Errorf("delete stale storageclass %s: %w", LocalPathStorageClass, err)
	}
	return true, nil
}

// PrepareLocalPathStorageClass is the best-effort, user-facing wrapper both
// `obol stack up` and `obol upgrade` call right before syncing the base
// release. It never fails the caller: a failure only means a stale class may
// survive, which the warning tells the operator how to fix by hand.
func PrepareLocalPathStorageClass(cfg *config.Config, u *ui.UI) {
	deleted, err := MigrateLocalPathStorageClass(cfg)
	if err != nil {
		u.Warnf("Could not check StorageClass %s for stale parameters: %v", LocalPathStorageClass, err)
		u.Dim("  If new PVCs stay Pending, run: obol kubectl delete storageclass " + LocalPathStorageClass + " && obol stack up")
		return
	}
	if deleted {
		u.Info("Replacing StorageClass " + LocalPathStorageClass + " (parameters are immutable; existing volumes are unaffected)")
	}
}
