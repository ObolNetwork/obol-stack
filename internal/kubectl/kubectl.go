// Package kubectl provides helpers for running kubectl commands with the
// correct KUBECONFIG environment variable set. It centralises the pattern
// that was previously duplicated across network, x402, model, agent, and
// cmd/obol packages.
package kubectl

import (
	"bytes"
	"errors"
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
		return errors.New("cluster not running. Run 'obol stack up' first")
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

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

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

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

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

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

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
	_, err := ApplyOutput(binary, kubeconfig, data)
	return err
}

// ApplyOutput pipes the given data into kubectl apply -f - and returns stdout.
func ApplyOutput(binary, kubeconfig string, data []byte) (string, error) {
	cmd := exec.Command(binary, "apply", "-f", "-")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	cmd.Stdin = bytes.NewReader(data)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("kubectl apply: %w: %s", err, errMsg)
		}

		return "", fmt.Errorf("kubectl apply: %w", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out != "" {
		fmt.Println(out)
	}

	return out, nil
}

// PipeCommands pipes the stdout of the first kubectl command into the stdin
// of the second. Both commands run with the correct KUBECONFIG. This is useful
// for patterns like "kubectl create --dry-run -o yaml | kubectl replace -f -"
// which avoid the 262KB annotation limit that kubectl apply imposes.
func PipeCommands(binary, kubeconfig string, args1, args2 []string) error {
	env := append(os.Environ(), "KUBECONFIG="+kubeconfig)

	cmd1 := exec.Command(binary, args1...)
	cmd1.Env = env

	cmd2 := exec.Command(binary, args2...)
	cmd2.Env = env

	pipe, err := cmd1.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd2.Stdin = pipe

	var stderr1, stderr2 bytes.Buffer
	cmd1.Stderr = &stderr1
	cmd2.Stderr = &stderr2

	if err := cmd1.Start(); err != nil {
		return fmt.Errorf("cmd1 start: %w", err)
	}
	if err := cmd2.Start(); err != nil {
		_ = cmd1.Process.Kill()
		return fmt.Errorf("cmd2 start: %w", err)
	}

	err1 := cmd1.Wait()
	err2 := cmd2.Wait()

	if err1 != nil {
		return fmt.Errorf("cmd1: %w: %s", err1, strings.TrimSpace(stderr1.String()))
	}
	if err2 != nil {
		return fmt.Errorf("cmd2: %w: %s", err2, strings.TrimSpace(stderr2.String()))
	}

	return nil
}
