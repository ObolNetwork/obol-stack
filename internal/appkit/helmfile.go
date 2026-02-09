package appkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// GenerateHelmfile creates a helmfile.yaml referencing a local chart with
// two value layers: values.yaml (chart defaults) and values-obol.yaml (overlay).
func GenerateHelmfile(releaseName, namespace, id string) string {
	return fmt.Sprintf(`# %s instance: %s
# Managed by obol %s

releases:
  - name: %s
    namespace: %s
    createNamespace: true
    chart: ./chart
    values:
      - values.yaml
      - values-obol.yaml
`, releaseName, id, releaseName, releaseName, namespace)
}

// SyncHelmfile runs `helmfile sync` in the given deployment directory.
func SyncHelmfile(cfg *config.Config, deploymentDir string) error {
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync")
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}
	return nil
}
