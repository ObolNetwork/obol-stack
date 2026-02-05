package tunnel

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func stackKubeconfigPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

func requireRunningStack(cfg *config.Config) (kubeconfigPath string, err error) {
	kubeconfigPath = stackKubeconfigPath(cfg)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", fmt.Errorf("stack not running, use 'obol stack up' first")
	}
	return kubeconfigPath, nil
}

func kubectlApplyManifest(cfg *config.Config, kubeconfigPath string, manifest []byte) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"apply", "-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}
	return nil
}
