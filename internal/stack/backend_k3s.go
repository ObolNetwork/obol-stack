package stack

import (
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
		return fmt.Errorf("k3s backend is only supported on Linux")
	}

	// Check sudo access: try non-interactive first (NOPASSWD), fall back to interactive prompt
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		cmd := exec.Command("sudo", "-v")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("k3s backend requires root/sudo access")
		}
	}

	// Check k3s binary exists
	k3sPath := filepath.Join(cfg.BinDir, "k3s")
	if _, err := os.Stat(k3sPath); os.IsNotExist(err) {
		return fmt.Errorf("k3s not found at %s\nRun obolup.sh to install dependencies", k3sPath)
	}

	return nil
}

func (b *K3sBackend) Init(cfg *config.Config, stackID string) error {
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	// Template k3s config with actual values
	k3sConfig := embed.K3sConfig
	k3sConfig = strings.ReplaceAll(k3sConfig, "{{STACK_ID}}", stackID)
	k3sConfig = strings.ReplaceAll(k3sConfig, "{{DATA_DIR}}", absDataDir)

	k3sConfigPath := filepath.Join(cfg.ConfigDir, k3sConfigFile)
	if err := os.WriteFile(k3sConfigPath, []byte(k3sConfig), 0644); err != nil {
		return fmt.Errorf("failed to write k3s config: %w", err)
	}

	fmt.Printf("K3s config saved to: %s\n", k3sConfigPath)
	return nil
}

func (b *K3sBackend) IsRunning(cfg *config.Config, stackID string) (bool, error) {
	pid, err := b.readPid(cfg)
	if err != nil {
		return false, nil
	}

	return b.isProcessAlive(pid), nil
}

func (b *K3sBackend) Up(cfg *config.Config, stackID string) ([]byte, error) {
	running, _ := b.IsRunning(cfg, stackID)
	if running {
		fmt.Println("k3s is already running")
		kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)
		data, err := os.ReadFile(kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("k3s is running but kubeconfig not found: %w", err)
		}
		return data, nil
	}

	// Clean up stale PID file if it exists (QA R6)
	b.cleanStalePid(cfg)

	k3sConfigPath := filepath.Join(cfg.ConfigDir, k3sConfigFile)
	if _, err := os.Stat(k3sConfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("k3s config not found at %s\nRun 'obol stack init --backend k3s' first", k3sConfigPath)
	}

	// Create data directory
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)
	k3sBinary := filepath.Join(cfg.BinDir, "k3s")
	logPath := filepath.Join(cfg.ConfigDir, k3sLogFile)

	// Remove stale kubeconfig so we wait for k3s to write a fresh one
	os.Remove(kubeconfigPath)

	// Open log file for k3s output
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create k3s log file: %w", err)
	}

	fmt.Println("Starting k3s server...")

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
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0600); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("failed to write k3s PID file: %w", err)
	}

	// Detach the process
	cmd.Process.Release()
	logFile.Close()

	fmt.Printf("k3s started (pid: %d)\n", pid)
	fmt.Printf("Logs: %s\n", logPath)

	// Wait for kubeconfig to be written by k3s
	fmt.Println("Waiting for kubeconfig...")
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(kubeconfigPath); err == nil && info.Size() > 0 {
			// Fix ownership: k3s writes kubeconfig as root via sudo
			exec.Command("sudo", "chown", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), kubeconfigPath).Run()

			data, err := os.ReadFile(kubeconfigPath)
			if err == nil && len(data) > 0 {
				fmt.Println("Kubeconfig ready, waiting for API server...")

				// Wait for the API server to actually respond
				apiDeadline := time.Now().Add(90 * time.Second)
				kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
				for time.Now().Before(apiDeadline) {
					probe := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath,
						"get", "nodes", "--no-headers")
					if out, err := probe.Output(); err == nil && len(out) > 0 {
						fmt.Println("API server ready")
						return data, nil
					}
					time.Sleep(3 * time.Second)
				}

				// Return kubeconfig even if API isn't fully ready yet
				fmt.Println("Warning: API server not fully ready, proceeding anyway")
				return data, nil
			}
		}
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("k3s did not write kubeconfig within timeout\nCheck logs: %s", logPath)
}

func (b *K3sBackend) Down(cfg *config.Config, stackID string) error {
	pid, err := b.readPid(cfg)
	if err != nil {
		fmt.Println("k3s PID file not found, may not be running")
		return nil
	}

	if !b.isProcessAlive(pid) {
		fmt.Println("k3s process not running, cleaning up PID file")
		b.removePidFile(cfg)
		return nil
	}

	fmt.Printf("Stopping k3s (pid: %d)...\n", pid)

	// Send SIGTERM to the sudo/k3s process only (not the process group).
	// Using negative PID (process group kill) is unsafe here because the saved PID
	// is the sudo wrapper, whose process group can include unrelated system processes
	// like systemd-logind — killing those crashes the desktop session.
	// sudo forwards SIGTERM to k3s, which handles its own child process cleanup.
	pidStr := strconv.Itoa(pid)
	stopCmd := exec.Command("sudo", "kill", "-TERM", pidStr)
	stopCmd.Stdout = os.Stdout
	stopCmd.Stderr = os.Stderr
	if err := stopCmd.Run(); err != nil {
		fmt.Printf("SIGTERM failed, sending SIGKILL: %v\n", err)
		exec.Command("sudo", "kill", "-9", pidStr).Run()
	}

	// Wait for process to exit (up to 30 seconds)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !b.isProcessAlive(pid) {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Clean up orphaned k3s child processes (containerd-shim, etc.)
	// Use k3s-killall.sh if available, otherwise kill containerd shims directly.
	killallPath := "/usr/local/bin/k3s-killall.sh"
	if _, err := os.Stat(killallPath); err == nil {
		fmt.Println("Running k3s cleanup...")
		cleanCmd := exec.Command("sudo", killallPath)
		cleanCmd.Stdout = os.Stdout
		cleanCmd.Stderr = os.Stderr
		cleanCmd.Run()
	} else {
		// k3s-killall.sh not installed (binary-only install via obolup).
		// Kill orphaned containerd-shim processes that use the k3s socket.
		fmt.Println("Cleaning up k3s child processes...")
		exec.Command("sudo", "pkill", "-TERM", "-f", "containerd-shim.*k3s").Run()
		time.Sleep(2 * time.Second)
		// Force-kill any that survived SIGTERM
		exec.Command("sudo", "pkill", "-KILL", "-f", "containerd-shim.*k3s").Run()
	}

	b.removePidFile(cfg)
	fmt.Println("k3s stopped")
	return nil
}

func (b *K3sBackend) Destroy(cfg *config.Config, stackID string) error {
	// Stop if running
	b.Down(cfg, stackID)

	// Clean up k3s state directories (default + custom data-dir)
	absDataDir, _ := filepath.Abs(cfg.DataDir)
	cleanDirs := []string{
		"/var/lib/rancher/k3s",
		"/etc/rancher/k3s",
		filepath.Join(absDataDir, "k3s"),
	}
	for _, dir := range cleanDirs {
		if _, err := os.Stat(dir); err == nil {
			fmt.Printf("Cleaning up: %s\n", dir)
			exec.Command("sudo", "rm", "-rf", dir).Run()
		}
	}

	// Run uninstall script if available
	uninstallPath := "/usr/local/bin/k3s-uninstall.sh"
	if _, err := os.Stat(uninstallPath); err == nil {
		fmt.Println("Running k3s uninstall...")
		uninstallCmd := exec.Command("sudo", uninstallPath)
		uninstallCmd.Stdout = os.Stdout
		uninstallCmd.Stderr = os.Stderr
		uninstallCmd.Run()
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
func (b *K3sBackend) cleanStalePid(cfg *config.Config) {
	pid, err := b.readPid(cfg)
	if err != nil {
		return
	}
	if !b.isProcessAlive(pid) {
		fmt.Printf("Cleaning up stale PID file (pid %d no longer running)\n", pid)
		b.removePidFile(cfg)
	}
}

// isProcessAlive checks if a root-owned process is still running.
// Uses sudo kill -0 since the k3s process runs as root and direct
// signal(0) from an unprivileged user returns EPERM.
func (b *K3sBackend) isProcessAlive(pid int) bool {
	return exec.Command("sudo", "kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// removePidFile removes the k3s PID file
func (b *K3sBackend) removePidFile(cfg *config.Config) {
	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)
	os.Remove(pidPath)
}
