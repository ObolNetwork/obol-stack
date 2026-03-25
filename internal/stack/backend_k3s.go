package stack

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	k3sConfigFile = "k3s-config.yaml"
	k3sPidFile    = ".k3s.pid"
	k3sLogFile    = "k3s.log"
)

// K3sBackend manages a standalone k3s cluster (bare-metal)
type K3sBackend struct{}

func (b *K3sBackend) Name() string { return BackendK3s }

func (b *K3sBackend) Prerequisites(cfg *config.Config) error {
	if runtime.GOOS != "linux" {
		return errors.New("k3s backend is only supported on Linux")
	}

	// Check sudo access: try non-interactive first (NOPASSWD), fall back to interactive prompt
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		cmd := exec.Command("sudo", "-v")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout

		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return errors.New("k3s backend requires root/sudo access")
		}
	}

	// Check k3s binary exists
	k3sPath := filepath.Join(cfg.BinDir, "k3s")
	if _, err := os.Stat(k3sPath); os.IsNotExist(err) {
		return fmt.Errorf("k3s not found at %s\nRun obolup.sh to install dependencies", k3sPath)
	}

	return nil
}

func (b *K3sBackend) Init(cfg *config.Config, u *ui.UI, stackID string) error {
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	// Template k3s config with actual values
	k3sConfig := embed.K3sConfig
	k3sConfig = strings.ReplaceAll(k3sConfig, "{{STACK_ID}}", stackID)
	k3sConfig = strings.ReplaceAll(k3sConfig, "{{DATA_DIR}}", absDataDir)

	k3sConfigPath := filepath.Join(cfg.ConfigDir, k3sConfigFile)
	if err := os.WriteFile(k3sConfigPath, []byte(k3sConfig), 0o600); err != nil {
		return fmt.Errorf("failed to write k3s config: %w", err)
	}

	return nil
}

func (b *K3sBackend) IsRunning(cfg *config.Config, stackID string) (bool, error) {
	pid, err := b.readPid(cfg)
	if err != nil {
		return false, nil
	}

	return b.isProcessAlive(pid), nil
}

func (b *K3sBackend) Up(cfg *config.Config, u *ui.UI, stackID string) ([]byte, error) {
	running, _ := b.IsRunning(cfg, stackID)
	if running {
		u.Warn("k3s is already running")

		kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

		data, err := os.ReadFile(kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("k3s is running but kubeconfig not found: %w", err)
		}

		return data, nil
	}

	// Clean up stale PID file if it exists
	b.cleanStalePid(cfg, u)

	k3sConfigPath := filepath.Join(cfg.ConfigDir, k3sConfigFile)
	if _, err := os.Stat(k3sConfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("k3s config not found at %s\nRun 'obol stack init --backend k3s' first", k3sConfigPath)
	}

	// Create data directory
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	if err := os.MkdirAll(absDataDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)
	k3sBinary := filepath.Join(cfg.BinDir, "k3s")
	logPath := filepath.Join(cfg.ConfigDir, k3sLogFile)

	// Remove stale kubeconfig so we wait for k3s to write a fresh one
	os.Remove(kubeconfigPath)

	// Open log file for k3s output
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to create k3s log file: %w", err)
	}

	u.Info("Starting k3s server...")

	// Start k3s server as background process via sudo
	cmd := exec.Command("sudo",
		k3sBinary, "server",
		"--config", k3sConfigPath,
		"--write-kubeconfig", kubeconfigPath,
		"--write-kubeconfig-mode", "0600",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("failed to start k3s: %w", err)
	}

	// Save PID before releasing the process handle
	pid := cmd.Process.Pid

	// Write PID file
	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("failed to write k3s PID file: %w", err)
	}

	// Detach the process
	cmd.Process.Release()
	logFile.Close()

	u.Detail("PID", strconv.Itoa(pid))
	u.Detail("Logs", logPath)

	// Wait for kubeconfig to be written by k3s
	var kubeconfigData []byte

	err = u.RunWithSpinner("Waiting for kubeconfig", func() error {
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if info, statErr := os.Stat(kubeconfigPath); statErr == nil && info.Size() > 0 {
				exec.Command("sudo", "chown", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), kubeconfigPath).Run()

				data, readErr := os.ReadFile(kubeconfigPath)
				if readErr == nil && len(data) > 0 {
					kubeconfigData = data
					return nil
				}
			}

			time.Sleep(2 * time.Second)
		}

		return fmt.Errorf("k3s did not write kubeconfig within timeout\nCheck logs: %s", logPath)
	})
	if err != nil {
		return nil, err
	}

	// Wait for API server
	err = u.RunWithSpinner("Waiting for API server", func() error {
		kubectlPath := filepath.Join(cfg.BinDir, "kubectl")

		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			probe := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath, "get", "nodes", "--no-headers")
			if out, probeErr := probe.Output(); probeErr == nil && len(out) > 0 {
				return nil
			}

			time.Sleep(3 * time.Second)
		}

		return errors.New("API server not ready after 90s")
	})
	if err != nil {
		u.Warn("API server not fully ready, proceeding anyway")
	}

	return kubeconfigData, nil
}

func (b *K3sBackend) Down(cfg *config.Config, u *ui.UI, stackID string) error {
	pid, err := b.readPid(cfg)
	if err != nil {
		u.Warn("k3s PID file not found, may not be running")
		return nil
	}

	if !b.isProcessAlive(pid) {
		u.Warn("k3s process not running, cleaning up PID file")
		b.removePidFile(cfg)

		return nil
	}

	u.Infof("Stopping k3s (pid: %d)", pid)

	pidStr := strconv.Itoa(pid)

	stopCmd := exec.Command("sudo", "kill", "-TERM", pidStr)
	if err := u.Exec(ui.ExecConfig{
		Name: "Sending SIGTERM to k3s",
		Cmd:  stopCmd,
	}); err != nil {
		u.Warnf("SIGTERM failed, sending SIGKILL: %v", err)
		exec.Command("sudo", "kill", "-9", pidStr).Run()
	}

	// Wait for process to exit
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !b.isProcessAlive(pid) {
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Clean up orphaned k3s child processes
	killallPath := "/usr/local/bin/k3s-killall.sh"
	if _, err := os.Stat(killallPath); err == nil {
		cleanCmd := exec.Command("sudo", killallPath)
		_ = u.Exec(ui.ExecConfig{
			Name: "Running k3s cleanup",
			Cmd:  cleanCmd,
		})
	} else {
		exec.Command("sudo", "pkill", "-TERM", "-f", "containerd-shim.*k3s").Run()
		time.Sleep(2 * time.Second)
		exec.Command("sudo", "pkill", "-KILL", "-f", "containerd-shim.*k3s").Run()
	}

	b.removePidFile(cfg)
	u.Success("k3s stopped")

	return nil
}

func (b *K3sBackend) Destroy(cfg *config.Config, u *ui.UI, stackID string) error {
	// Stop if running
	b.Down(cfg, u, stackID)

	// Clean up k3s state directories
	absDataDir, _ := filepath.Abs(cfg.DataDir)

	cleanDirs := []string{
		"/var/lib/rancher/k3s",
		"/etc/rancher/k3s",
		filepath.Join(absDataDir, "k3s"),
	}
	for _, dir := range cleanDirs {
		if _, err := os.Stat(dir); err == nil {
			u.Dim("  Cleaning up: " + dir)
			exec.Command("sudo", "rm", "-rf", dir).Run()
		}
	}

	// Run uninstall script if available
	uninstallPath := "/usr/local/bin/k3s-uninstall.sh"
	if _, err := os.Stat(uninstallPath); err == nil {
		uninstallCmd := exec.Command("sudo", uninstallPath)
		_ = u.Exec(ui.ExecConfig{
			Name: "Running k3s uninstall",
			Cmd:  uninstallCmd,
		})
	}

	return nil
}

func (b *K3sBackend) DataDir(cfg *config.Config) string {
	absDataDir, _ := filepath.Abs(cfg.DataDir)
	return absDataDir
}

// readPid reads the k3s PID from the PID file
func (b *K3sBackend) readPid(cfg *config.Config) (int, error) {
	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	if pid <= 0 {
		return 0, fmt.Errorf("invalid PID in %s: %d", pidPath, pid)
	}

	return pid, nil
}

// cleanStalePid removes the PID file if the process is no longer running
func (b *K3sBackend) cleanStalePid(cfg *config.Config, u *ui.UI) {
	pid, err := b.readPid(cfg)
	if err != nil {
		return
	}

	if !b.isProcessAlive(pid) {
		u.Dim(fmt.Sprintf("  Cleaning up stale PID file (pid %d no longer running)", pid))
		b.removePidFile(cfg)
	}
}

// isProcessAlive checks if a root-owned process is still running.
func (b *K3sBackend) isProcessAlive(pid int) bool {
	return exec.Command("sudo", "kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// removePidFile removes the k3s PID file
func (b *K3sBackend) removePidFile(cfg *config.Config) {
	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)
	os.Remove(pidPath)
}
