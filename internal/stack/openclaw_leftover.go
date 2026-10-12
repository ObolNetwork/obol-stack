package stack

import (
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// listNamespacesFn lists cluster namespace names. Swappable in tests.
var listNamespacesFn = func(cfg *config.Config) ([]string, error) {
	bin, kubeconfig := kubectl.Paths(cfg)

	out, err := kubectl.Output(bin, kubeconfig, "get", "namespaces",
		"--request-timeout=10s", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}

	return strings.Fields(out), nil
}

// warnLeftoverOpenClawNamespaces tells users still running openclaw-*
// instances (removed in v0.16) how to keep their wallet. The keystore PVC has
// reclaimPolicy Delete, so deleting the namespace deletes the keystore files
// on the host: they must be copied first. Best effort, never fails up.
func warnLeftoverOpenClawNamespaces(cfg *config.Config, u *ui.UI) {
	names, err := listNamespacesFn(cfg)
	if err != nil {
		return
	}

	var leftover []string

	for _, ns := range names {
		if strings.HasPrefix(ns, "openclaw-") {
			leftover = append(leftover, ns)
		}
	}

	if len(leftover) == 0 {
		return
	}

	u.Blank()
	u.Warnf("Found OpenClaw namespace(s): %s. OpenClaw was removed in v0.16; obol no longer manages them.", strings.Join(leftover, ", "))

	for _, ns := range leftover {
		id := strings.TrimPrefix(ns, "openclaw-")
		u.Warn("  " + ns + " wallet: copy these somewhere safe BEFORE deleting the namespace (deleting it deletes the keystore):")
		u.Warn("    keystore: " + filepath.Join(cfg.DataDir, ns, "remote-signer-keystores"))
		u.Warn("    password: " + filepath.Join(cfg.ConfigDir, "applications", "openclaw", id, "values-remote-signer.yaml"))
	}

	u.Warn("  To move a wallet into Hermes: back it up with obol v0.15 ('obol agent wallet backup --runtime openclaw <id> --file w.json'),")
	u.Warn("  then here: 'obol agent wallet restore --input w.json --force' (back up the Hermes wallet first).")
	u.Warn("  Then remove the instance: obol kubectl delete namespace <namespace>")
}
