package agentruntime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// RemoteSignerStrategyPatchArgs builds the kubectl args that pin
// strategy.type=Recreate on the remote-signer deployment while clearing the
// k8s-defaulted strategy.rollingUpdate (the explicit null in a merge patch is
// what removes the field — the API rejects type=Recreate while rollingUpdate
// is still set).
//
// Pure / testable — no side effects.
func RemoteSignerStrategyPatchArgs(namespace string) []string {
	return []string{
		"patch", "deployment/remote-signer",
		"-n", namespace,
		"--type=merge",
		"-p", `{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}`,
	}
}

// EnforceRemoteSignerRecreate pins the remote-signer deployment to the
// Recreate strategy after a helmfile sync.
//
// The obol/remote-signer chart (0.3.3) does not expose spec.strategy, so the
// deployment is created with the default RollingUpdate — but the signer is a
// singleton over a ReadWriteOnce keystore PVC. On a multi-node cluster a
// RollingUpdate surge pod can land on a different node and wedge forever on
// the volume attach; on any cluster it briefly runs two signers over the
// same keystore. Recreate is the same stance hermes and x402-buyer already
// take for RWO-backed singletons. Patching spec.strategy does not touch the
// pod template, so this never triggers a rollout by itself, and helm leaves
// it alone on later upgrades because the chart never renders the field.
//
// Runs after (not before) helmfile sync so a fresh install is patched in the
// same run that created the deployment. Best-effort: a missing deployment
// (wallet-less instance) returns nil; other failures are returned for the
// caller to warn on — sync must not fail over a strategy patch.
func EnforceRemoteSignerRecreate(cfg *config.Config, namespace string) error {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(kubectlBinary, RemoteSignerStrategyPatchArgs(namespace)...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "NotFound") || strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("patch remote-signer strategy in %s: %w\n%s", namespace, err, strings.TrimSpace(string(out)))
	}
	return nil
}
