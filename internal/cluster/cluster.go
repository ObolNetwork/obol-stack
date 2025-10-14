package cluster

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/obol/obol-stack/internal/config"
	"github.com/obol/obol-stack/internal/logging"
)

const (
	clusterName      = "obol-stack"
	k3dConfigFile    = "config.yaml"
	kubeconfigFile   = "kubeconfig.yaml"
)

// Init initializes the cluster configuration
func Init(cfg *config.Config, logger *logging.Logger, force bool) error {
	if logger != nil {
		logger.LogCommand("cluster init", []string{fmt.Sprintf("force=%v", force)})
		defer func() {
			logger.LogCommandComplete("cluster init", 0, nil)
		}()
	}
	// Create cluster config directory
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")
	destPath := filepath.Join(clusterConfigDir, k3dConfigFile)

	// Check if config already exists
	if _, err := os.Stat(destPath); err == nil {
		if !force {
			fmt.Printf("✓ Cluster configuration already exists at %s\n", destPath)
			return nil
		}
		fmt.Printf("Overwriting existing cluster configuration at %s\n", destPath)
	}

	// Get the k3d config template path
	templatePath, err := getK3dTemplatePath()
	if err != nil {
		return fmt.Errorf("failed to find k3d config template: %w", err)
	}

	if err := os.MkdirAll(clusterConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster config dir: %w", err)
	}

	// Read template
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read k3d config template: %w", err)
	}

	// Write to destination
	if err := os.WriteFile(destPath, template, 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	// Create kubeconfig directory
	kubeconfigDir := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create kubeconfig dir: %w", err)
	}

	fmt.Printf("✓ Initialized cluster configuration at %s\n", destPath)
	return nil
}

// Up starts the k3d cluster
func Up(cfg *config.Config, logger *logging.Logger) error {
	var cmdErr error
	if logger != nil {
		logger.LogCommand("cluster up", []string{})
		defer func() {
			exitCode := 0
			if cmdErr != nil {
				exitCode = 1
			}
			logger.LogCommandComplete("cluster up", exitCode, cmdErr)
		}()
	}
	k3dConfigPath := filepath.Join(cfg.ConfigDir, "cluster", "k3d", k3dConfigFile)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", kubeconfigFile)

	// Check if config exists
	if _, err := os.Stat(k3dConfigPath); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("cluster config not found, run 'obol cluster init' first")
		return cmdErr
	}

	// Check if cluster already exists using cluster list
	cmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	output, _ := cmd.Output()
	if clusterExists(string(output), clusterName) {
		cmdErr = fmt.Errorf("cluster '%s' already exists, use 'obol cluster down' to stop it first", clusterName)
		return cmdErr
	}

	fmt.Printf("Starting cluster '%s'...\n", clusterName)

	// Get absolute path to data directory for k3d volume mount
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		cmdErr = fmt.Errorf("failed to get absolute path for data directory: %w", err)
		return cmdErr
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		cmdErr = fmt.Errorf("failed to create data directory: %w", err)
		return cmdErr
	}

	// Create cluster using k3d config
	cmd = exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "create",
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
		"--verbose",
	)
	// Set OBOL_DATA_DIR for k3d config expansion (must be absolute path)
	cmd.Env = append(os.Environ(), fmt.Sprintf("OBOL_DATA_DIR=%s", absDataDir))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Using data directory: %s\n", absDataDir)

	// Capture stdout/stderr to logger if available
	if logger != nil {
		logWriter := &logWriter{logger: logger, level: "info"}
		cmd.Stdout = io.MultiWriter(os.Stdout, logWriter)
		cmd.Stderr = io.MultiWriter(os.Stderr, logWriter)
	}

	if err := cmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to create cluster: %w", err)
		return cmdErr
	}

	// Export kubeconfig
	cmd = exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"kubeconfig", "get", clusterName,
	)
	kubeconfigData, err := cmd.Output()
	if err != nil {
		cmdErr = fmt.Errorf("failed to get kubeconfig: %w", err)
		return cmdErr
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		cmdErr = fmt.Errorf("failed to write kubeconfig: %w", err)
		return cmdErr
	}

	fmt.Printf("✓ Cluster started successfully\n")
	fmt.Printf("✓ Kubeconfig saved to %s\n", kubeconfigPath)
	fmt.Printf("\nTo use kubectl with this cluster:\n")
	fmt.Printf("  export KUBECONFIG=%s\n", kubeconfigPath)
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config, logger *logging.Logger) error {
	var cmdErr error
	if logger != nil {
		logger.LogCommand("cluster down", []string{})
		defer func() {
			exitCode := 0
			if cmdErr != nil {
				exitCode = 1
			}
			logger.LogCommandComplete("cluster down", exitCode, cmdErr)
		}()
	}

	fmt.Printf("Stopping cluster '%s'...\n", clusterName)

	cmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "delete", clusterName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to stop cluster: %w", err)
		return cmdErr
	}

	fmt.Printf("✓ Cluster stopped successfully\n")
	return nil
}

// Purge deletes the cluster and all data
func Purge(cfg *config.Config, logger *logging.Logger) error {
	var cmdErr error
	if logger != nil {
		logger.LogCommand("cluster purge", []string{})
		defer func() {
			exitCode := 0
			if cmdErr != nil {
				exitCode = 1
			}
			logger.LogCommandComplete("cluster purge", exitCode, cmdErr)
		}()
	}

	// Stop cluster first
	if err := Down(cfg, logger); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// Remove cluster config directory
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster")
	if err := os.RemoveAll(clusterConfigDir); err != nil {
		cmdErr = fmt.Errorf("failed to remove cluster config: %w", err)
		return cmdErr
	}

	fmt.Printf("✓ Cluster configuration purged\n")
	return nil
}

// Connect opens k9s connected to the cluster
func Connect(cfg *config.Config) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", kubeconfigFile)

	// Check if kubeconfig exists
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running, use 'obol cluster up' first")
	}

	k9sPath := filepath.Join(cfg.BinDir, "k9s")

	// Check if k9s exists
	if _, err := os.Stat(k9sPath); os.IsNotExist(err) {
		return fmt.Errorf("k9s not found, please install it in %s", cfg.BinDir)
	}

	fmt.Printf("Connecting to cluster with k9s...\n")

	cmd := exec.Command(k9sPath)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// getK3dTemplatePath finds the k3d config template
func getK3dTemplatePath() (string, error) {
	// Try relative to current directory (development mode)
	cwd, _ := os.Getwd()
	templatePath := filepath.Join(cwd, "k3d", k3dConfigFile)
	if _, err := os.Stat(templatePath); err == nil {
		return templatePath, nil
	}

	// Try relative to executable (production mode)
	exe, err := os.Executable()
	if err == nil {
		templatePath = filepath.Join(filepath.Dir(exe), "..", "k3d", k3dConfigFile)
		if _, err := os.Stat(templatePath); err == nil {
			return templatePath, nil
		}
	}

	return "", fmt.Errorf("k3d config template not found")
}

// clusterExists checks if cluster name exists in k3d cluster list output
func clusterExists(output, name string) bool {
	// Check if the cluster name appears in the output
	return strings.Contains(output, name)
}

// logWriter wraps a logger to implement io.Writer for capturing command output
type logWriter struct {
	logger *logging.Logger
	level  string
	buffer []byte
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	// Accumulate data and log line by line
	w.buffer = append(w.buffer, p...)

	// Process complete lines
	for {
		idx := strings.IndexByte(string(w.buffer), '\n')
		if idx == -1 {
			break
		}

		line := string(w.buffer[:idx])
		w.buffer = w.buffer[idx+1:]

		if line != "" {
			w.logger.Info("command output", "line", line)
		}
	}

	return len(p), nil
}
