package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/obol/obol-stack/internal/config"
	"github.com/obol/obol-stack/internal/embed"
	"github.com/obol/obol-stack/internal/executor"
	"github.com/obol/obol-stack/internal/logging"
)

const (
	clusterNamePrefix = "obol-stack"
	k3dConfigFile     = "config.yaml"
	kubeconfigFile    = "kubeconfig.yaml"
	clusterIDFile     = ".cluster_id"
)

// getClusterName returns the full cluster name with cluster_id
func getClusterName(clusterID string) string {
	return fmt.Sprintf("%s-%s", clusterNamePrefix, clusterID)
}

// Init initializes the cluster configuration
func Init(cfg *config.Config, _ *logging.Logger, force bool) error {
	// Create cluster config directory early
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")
	if err := os.MkdirAll(clusterConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster config dir: %w", err)
	}

	// Generate or get existing cluster_id first
	clusterID, err := getOrCreateClusterID(clusterConfigDir, force)
	if err != nil {
		return fmt.Errorf("failed to generate cluster_id: %w", err)
	}

	// Always create cluster-specific logger
	logger, err := logging.NewLoggerWithCluster(cfg.StateDir, clusterID)
	if err != nil {
		return fmt.Errorf("failed to create cluster logger: %w", err)
	}
	defer logger.Close()

	// Log command
	logger.LogCommandWithClusterID("cluster init", []string{fmt.Sprintf("force=%v", force)}, clusterID)
	defer func() {
		logger.LogCommandComplete("cluster init", 0, nil)
	}()

	destPath := filepath.Join(clusterConfigDir, k3dConfigFile)

	// Check if config already exists
	if _, err := os.Stat(destPath); err == nil {
		if !force {
			logger.Info("✓ Cluster configuration already exists", "path", destPath)
			logger.Info("Cluster ID", "cluster_id", clusterID)
			return nil
		}
		logger.Info("Overwriting existing cluster configuration", "path", destPath)
	}

	if err := os.MkdirAll(clusterConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster config dir: %w", err)
	}

	// Write embedded k3d config
	if err := embed.WriteK3dConfig(destPath); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	// Copy only default applications (monitoring stack)
	// Non-default applications must be installed via 'obol app install'
	applicationsDestDir := filepath.Join(cfg.ConfigDir, "applications")
	if err := embed.CopyDefaultApplications(applicationsDestDir); err != nil {
		return fmt.Errorf("failed to copy default applications: %w", err)
	}

	// Create kubeconfig directory
	kubeconfigDir := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create kubeconfig dir: %w", err)
	}

	logger.Info("✓ Initialized cluster configuration", "path", destPath)
	logger.Info("Cluster ID", "cluster_id", clusterID)
	return nil
}

// Up starts the k3d cluster
func Up(cfg *config.Config, _ *logging.Logger) error {
	var cmdErr error
	k3dConfigPath := filepath.Join(cfg.ConfigDir, "cluster", "k3d", k3dConfigFile)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", kubeconfigFile)
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")

	// Check if config exists
	if _, err := os.Stat(k3dConfigPath); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("cluster config not found, run 'obol cluster init' first")
		return cmdErr
	}

	// Get cluster_id
	clusterID, err := getClusterID(clusterConfigDir)
	if err != nil {
		cmdErr = fmt.Errorf("failed to read cluster_id: %w", err)
		return cmdErr
	}

	// Always create cluster-specific logger
	logger, err := logging.NewLoggerWithCluster(cfg.StateDir, clusterID)
	if err != nil {
		cmdErr = fmt.Errorf("failed to create cluster logger: %w", err)
		return cmdErr
	}
	defer logger.Close()

	// Create executor
	exec := executor.New(logger)

	// Log command with cluster_id
	logger.LogCommandWithClusterID("cluster up", []string{}, clusterID)
	defer func() {
		exitCode := 0
		if cmdErr != nil {
			exitCode = 1
		}
		logger.LogCommandComplete("cluster up", exitCode, cmdErr)
	}()

	// Build full cluster name
	fullClusterName := getClusterName(clusterID)

	// Check if cluster already exists using cluster list
	listCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	output, err := listCmd.Output()
	if err == nil && clusterExists(string(output), fullClusterName) {
		cmdErr = fmt.Errorf("cluster '%s' already exists, use 'obol cluster down' to stop it first", fullClusterName)
		return cmdErr
	}

	logger.Info("Starting cluster", "name", fullClusterName)

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

	logger.Info("Using data directory", "path", absDataDir)
	logger.Info("Cluster ID", "cluster_id", clusterID)

	// Create cluster using k3d config with custom name
	createCmd := exec.CommandWithLogging(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "create", fullClusterName,
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
		"--verbose",
	)
	// Get absolute path to config directory for manifests mount
	absConfigDir, err := filepath.Abs(cfg.ConfigDir)
	if err != nil {
		cmdErr = fmt.Errorf("failed to get absolute path for config directory: %w", err)
		return cmdErr
	}

	// Set environment variables for k3d config expansion (must be absolute paths)
	createCmd.Env = append(os.Environ(),
		fmt.Sprintf("OBOL_DATA_DIR=%s", absDataDir),
		fmt.Sprintf("OBOL_CONFIG_DIR=%s", absConfigDir),
		fmt.Sprintf("OBOL_CLUSTER_ID=%s", clusterID),
	)

	if err := createCmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to create cluster: %w", err)
		return cmdErr
	}

	// Export kubeconfig
	kubeconfigCmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"kubeconfig", "get", fullClusterName,
	)
	kubeconfigData, err := kubeconfigCmd.Output()
	if err != nil {
		cmdErr = fmt.Errorf("failed to get kubeconfig: %w", err)
		return cmdErr
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		cmdErr = fmt.Errorf("failed to write kubeconfig: %w", err)
		return cmdErr
	}

	logger.Info("✓ Cluster started successfully")
	logger.Info("✓ Kubeconfig saved", "path", kubeconfigPath)
	logger.Info("To use kubectl with this cluster, run:", "command", fmt.Sprintf("export KUBECONFIG=%s", kubeconfigPath))
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config, _ *logging.Logger) error {
	var cmdErr error
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")

	// Get cluster_id (optional - may not exist if cluster was never initialized)
	clusterID, _ := getClusterID(clusterConfigDir)

	// Build full cluster name (use clusterID if available, otherwise use default)
	var fullClusterName string
	if clusterID != "" {
		fullClusterName = getClusterName(clusterID)
	} else {
		// Fallback for legacy clusters without cluster_id
		fullClusterName = clusterNamePrefix
	}

	// Create cluster-specific logger and executor if we have a cluster_id
	var logger *logging.Logger
	var exec *executor.Executor
	if clusterID != "" {
		var err error
		logger, err = logging.NewLoggerWithCluster(cfg.StateDir, clusterID)
		if err == nil {
			defer logger.Close()
			exec = executor.New(logger)
			logger.LogCommand("cluster down", []string{})
			defer func() {
				exitCode := 0
				if cmdErr != nil {
					exitCode = 1
				}
				logger.LogCommandComplete("cluster down", exitCode, cmdErr)
			}()
		}
	}

	// Fallback to nil executor if no logger available
	if exec == nil {
		exec = executor.New(nil)
	}

	// For commands without logger/cluster context, just use executor without logging
	if logger == nil {
		exec = executor.New(nil)
		fmt.Printf("Stopping cluster '%s'...\n", fullClusterName)

		deleteCmd := exec.CommandWithLogging(
			filepath.Join(cfg.BinDir, "k3d"),
			"cluster", "delete", fullClusterName,
		)

		if err := deleteCmd.Run(); err != nil {
			cmdErr = fmt.Errorf("failed to stop cluster: %w", err)
			return cmdErr
		}

		fmt.Printf("✓ Cluster stopped successfully\n")
		return nil
	}

	logger.Info("Stopping cluster", "name", fullClusterName)

	deleteCmd := exec.CommandWithLogging(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "delete", fullClusterName,
	)

	if err := deleteCmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to stop cluster: %w", err)
		return cmdErr
	}

	logger.Info("✓ Cluster stopped successfully")
	return nil
}

// Purge deletes the cluster and all data (except binaries)
func Purge(cfg *config.Config, _ *logging.Logger) error {
	var cmdErr error
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")

	// Get cluster_id (optional - may not exist if cluster was never initialized)
	clusterID, _ := getClusterID(clusterConfigDir)

	// Create cluster-specific logger and executor if we have a cluster_id
	var logger *logging.Logger
	var exec *executor.Executor
	if clusterID != "" {
		var err error
		logger, err = logging.NewLoggerWithCluster(cfg.StateDir, clusterID)
		if err == nil {
			defer logger.Close()
			exec = executor.New(logger)
			logger.LogCommand("cluster purge", []string{})
			defer func() {
				exitCode := 0
				if cmdErr != nil {
					exitCode = 1
				}
				logger.LogCommandComplete("cluster purge", exitCode, cmdErr)
			}()
		}
	}

	// Fallback to nil executor if no logger available
	if exec == nil {
		exec = executor.New(nil)
	}

	// Stop cluster first
	if err := Down(cfg, nil); err != nil {
		if logger != nil {
			logger.Warn("Failed to stop cluster", "error", err)
		} else {
			fmt.Printf("Warning: %v\n", err)
		}
	}

	// Remove entire config directory (includes cluster config, applications, etc.)
	if err := os.RemoveAll(cfg.ConfigDir); err != nil {
		cmdErr = fmt.Errorf("failed to remove config directory: %w", err)
		return cmdErr
	}
	if logger != nil {
		logger.Info("✓ Removed config directory", "path", cfg.ConfigDir)
	} else {
		fmt.Printf("✓ Removed config directory: %s\n", cfg.ConfigDir)
	}

	// Remove data directory
	if err := os.RemoveAll(cfg.DataDir); err != nil {
		cmdErr = fmt.Errorf("failed to remove data directory: %w", err)
		return cmdErr
	}
	if logger != nil {
		logger.Info("✓ Removed data directory")
	} else {
		fmt.Printf("✓ Removed data directory\n")
	}

	// Remove state directory (history only - preserve logs)
	if clusterID != "" {
		clusterStateDir := filepath.Join(cfg.StateDir, clusterID)
		historyFile := filepath.Join(clusterStateDir, "history.jsonl")

		// Remove only the history file, preserve logs directory
		if err := os.Remove(historyFile); err != nil && !os.IsNotExist(err) {
			cmdErr = fmt.Errorf("failed to remove history file: %w", err)
			return cmdErr
		}

		if logger != nil {
			logger.Info("✓ Removed command history (logs preserved)")
		} else {
			fmt.Printf("✓ Removed command history (logs preserved)\n")
		}
	}

	if logger != nil {
		logger.Info("✓ Cluster purged (binaries preserved)")
	} else {
		fmt.Printf("✓ Cluster purged (binaries preserved)\n")
	}
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

// clusterExists checks if cluster name exists in k3d cluster list output
func clusterExists(output, name string) bool {
	// Check if the cluster name appears in the output
	return strings.Contains(output, name)
}

// generateClusterID creates a new cluster ID using petname
// Format: adjective-noun
// Example: wise-phoenix
func generateClusterID() string {
	return petname.Generate(2, "-") // 2 words: adjective-noun
}

// getOrCreateClusterID reads existing cluster_id or generates a new one
func getOrCreateClusterID(clusterConfigDir string, force bool) (string, error) {
	clusterIDPath := filepath.Join(clusterConfigDir, clusterIDFile)

	// Try to read existing cluster_id
	if !force {
		if data, err := os.ReadFile(clusterIDPath); err == nil {
			existingID := strings.TrimSpace(string(data))
			if existingID != "" {
				return existingID, nil
			}
		}
	}

	// Generate new cluster_id
	clusterID := generateClusterID()

	// Write to file
	if err := os.WriteFile(clusterIDPath, []byte(clusterID), 0644); err != nil {
		return "", fmt.Errorf("failed to write cluster_id: %w", err)
	}

	return clusterID, nil
}

// getClusterID reads the cluster_id from the cluster config directory
func getClusterID(clusterConfigDir string) (string, error) {
	clusterIDPath := filepath.Join(clusterConfigDir, clusterIDFile)

	data, err := os.ReadFile(clusterIDPath)
	if err != nil {
		return "", fmt.Errorf("cluster_id not found (cluster may not be initialized): %w", err)
	}

	clusterID := strings.TrimSpace(string(data))
	if clusterID == "" {
		return "", fmt.Errorf("cluster_id file is empty")
	}

	return clusterID, nil
}
