// Package kubectl provides helpers for running kubectl commands with the
// correct KUBECONFIG environment variable set. It centralises the pattern
// that was previously duplicated across network, x402, model, agent, and
// cmd/obol packages.
package kubectl

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// EnsureCluster checks that the kubeconfig file exists, returning a
// descriptive error when the cluster is not running.
func EnsureCluster(cfg *config.Config) error {
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}
	return nil
}

// Paths returns the absolute paths to the kubectl binary and kubeconfig.
func Paths(cfg *config.Config) (binary, kubeconfig string) {
	return filepath.Join(cfg.BinDir, "kubectl"),
		filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

// Run executes kubectl with the given arguments, inheriting stdout and
// capturing stderr. The error message includes stderr output on failure.
func Run(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

// RunSilent executes kubectl without inheriting stdout. Stderr is captured
// and included in the returned error on failure.
func RunSilent(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

// Output executes kubectl and returns the captured stdout. Stderr is
// captured and included in the returned error on failure.
func Output(binary, kubeconfig string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%w: %s", err, errMsg)
		}
		return "", err
	}
	return stdout.String(), nil
}

// Apply pipes the given data into kubectl apply -f -.
func Apply(binary, kubeconfig string, data []byte) error {
	cmd := exec.Command(binary, "apply", "-f", "-")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfig))
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = os.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("kubectl apply: %w: %s", err, errMsg)
		}
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}
