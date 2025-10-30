package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/executor"
	"github.com/ObolNetwork/obol-stack/internal/logging"
	petname "github.com/dustinkirkland/golang-petname"
)

const (
	k3dConfigFile  = "k3d.yaml"
	kubeconfigFile = "kubeconfig.yaml"
	stackIDFile    = ".stack-id"
)

// Init initializes the stack configuration
func Init(cfg *config.Config, force bool) error {
	// Create flat stack config directory
	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)

	// Check if config already exists
	if _, err := os.Stat(k3dConfigPath); err == nil {
		if !force {
			return fmt.Errorf("stack configuration already exists at %s\nUse --force to overwrite", k3dConfigPath)
		}
		// Overwrite will be logged after logger is created
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Generate unique stack ID
	stackID := petname.Generate(2, "-")

	// Create logger and executor
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info("Initializing cluster configuration")
	l.Info(fmt.Sprintf("Cluster ID: %s", stackID))

	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	absConfigDir, err := filepath.Abs(cfg.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for config directory: %w", err)
	}

	// Check if overwriting config
	if _, err := os.Stat(k3dConfigPath); err == nil {
		l.Info("Overwriting existing stack configuration", "path", k3dConfigPath)
	}

	// Replace placeholder in k3d config with actual stack ID
	k3dConfig := embed.K3dConfig
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{STACK_ID}}", stackID)
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{DATA_DIR}}", absDataDir)
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{CONFIG_DIR}}", absConfigDir)

	// Write k3d config with stack ID to destination
	if err := os.WriteFile(k3dConfigPath, []byte(k3dConfig), 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	l.Info(fmt.Sprintf("K3d config saved to: %s", k3dConfigPath))

	// Copy root helmfile to config directory for application orchestration
	helmfileDestPath := filepath.Join(cfg.ConfigDir, "helmfile.yaml")
	if err := os.WriteFile(helmfileDestPath, []byte(embed.HelmfileTemplate), 0644); err != nil {
		return fmt.Errorf("failed to write helmfile: %w", err)
	}
	l.Info(fmt.Sprintf("Helmfile copied to: %s", helmfileDestPath))

	// Copy embedded charts (default + examples)
	chartsDir := filepath.Join(cfg.ConfigDir, "charts")
	if err := embed.CopyCharts(chartsDir); err != nil {
		return fmt.Errorf("failed to copy charts: %w", err)
	}
	l.Info(fmt.Sprintf("Charts copied to: %s", chartsDir))

	// Copy embedded manifests (k3s auto-apply manifests)
	manifestsDir := filepath.Join(cfg.ConfigDir, "manifests")
	if err := embed.CopyManifests(manifestsDir); err != nil {
		return fmt.Errorf("failed to copy manifests: %w", err)
	}
	l.Info(fmt.Sprintf("Manifests copied to: %s", manifestsDir))


	// Store stack ID for later use
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	l.Success("Initialized stack configuration", "path", k3dConfigPath)
	l.Success("Stack ID", "id", stackID)
	return nil
}

// Up starts the k3d cluster
func Up(cfg *config.Config) error {
	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

	// Check if config exists
	if _, err := os.Stat(k3dConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack config not found, run 'obol stack init' first")
	}

	// Get stack ID and full stack name
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}

	// Create logger and executor
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	stackName := getStackName(cfg)

	// Create executor for subprocess calls with the logger
	exec := executor.New(l.Logger)
	defer exec.Close()

	// Check if cluster already exists using cluster list
	listCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	listCmdOutput, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("k3d list command failed: %w", err)
	}

	if stackExists(string(listCmdOutput), stackName) {
		// Cluster exists - check if it's stopped or running
		l.Info("Stack already exists, attempting to start", "name", stackName, "id", stackID)
		startCmd := exec.CommandWithOutput(
			filepath.Join(cfg.BinDir, "k3d"),
			"cluster", "start", stackName,
		)
		if err := startCmd.Run(); err != nil {
			return fmt.Errorf("failed to start existing cluster: %w", err)
		}
		l.Success("Stack restarted successfully")
		l.Success("Stack ID", "id", stackID)
		return nil
	}

	l.Info("Starting stack", "name", stackName, "id", stackID)

	// Get absolute path to data directory for k3d volume mount
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Create cluster using k3d config with custom name
	createCmd := exec.CommandWithOutput(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "create", stackName,
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
	)

	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	// Export kubeconfig
	kubeconfigCmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"kubeconfig", "get", stackName,
	)
	kubeconfigData, err := kubeconfigCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	l.Success("Stack started successfully")
	if stackID != "" {
		l.Success("Stack ID", "id", stackID)
	}
	l.Info(fmt.Sprintf("export KUBECONFIG=%s", kubeconfigPath))
	l.Success("Kubeconfig saved", "path", kubeconfigPath)
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
	}
	stackName := getStackName(cfg)

	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	exec := executor.New(l.Logger)
	defer exec.Close()

	l.Info("Stopping stack gracefully", "name", stackName, "id", stackID)

	// First attempt graceful stop (allows processes to shutdown gracefully)
	stopCmd := exec.CommandWithOutput(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "stop", stackName,
	)

	if err := stopCmd.Run(); err != nil {
		l.Warn("Graceful stop timed out or failed, forcing cluster deletion")
		// Fallback to delete if stop fails
		deleteCmd := exec.CommandWithOutput(
			filepath.Join(cfg.BinDir, "k3d"),
			"cluster", "delete", stackName,
		)
		if err := deleteCmd.Run(); err != nil {
			return fmt.Errorf("failed to stop cluster: %w", err)
		}
	}

	l.Success("Stack stopped successfully")
	return nil
}

// Purge deletes the cluster config and optionally data
func Purge(cfg *config.Config, force bool) error {
	// Get stack_id (optional - may not exist if stack was never initialized)
	stackID := getStackID(cfg)

	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	// Create executor for subprocess calls
	exec := executor.New(l.Logger)
	defer exec.Close()

	// Delete cluster containers
	stackName := getStackName(cfg)
	if stackName != "" {
		if force {
			// Force delete without graceful shutdown
			l.Info("Force deleting cluster containers", "name", stackName)
			deleteCmd := exec.CommandWithOutput(
				filepath.Join(cfg.BinDir, "k3d"),
				"cluster", "delete", stackName,
			)
			if err := deleteCmd.Run(); err != nil {
				l.Warn(fmt.Sprintf("Failed to delete cluster (may already be deleted): %v", err))
			}
			l.Success("Cluster containers force deleted")
		} else {
			// Graceful shutdown first to ensure data is written properly
			l.Info("Gracefully stopping cluster before deletion", "name", stackName)
			stopCmd := exec.CommandWithOutput(
				filepath.Join(cfg.BinDir, "k3d"),
				"cluster", "stop", stackName,
			)
			if err := stopCmd.Run(); err != nil {
				l.Warn("Graceful stop timed out or failed, proceeding with deletion anyway")
			} else {
				l.Success("Cluster stopped gracefully")
			}

			// Now delete the stopped cluster
			l.Info("Deleting cluster containers", "name", stackName)
			deleteCmd := exec.CommandWithOutput(
				filepath.Join(cfg.BinDir, "k3d"),
				"cluster", "delete", stackName,
			)
			if err := deleteCmd.Run(); err != nil {
				l.Warn(fmt.Sprintf("Failed to delete cluster (may already be deleted): %v", err))
			}
			l.Success("Cluster containers deleted")
		}
	}

	// Remove stack config directory
	stackConfigDir := filepath.Join(cfg.ConfigDir)
	if err := os.RemoveAll(stackConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}
	l.Success("Removed cluster config directory")

	// Remove data directory only if force flag is set
	if force {
		// Use sudo to remove data directory since it may contain root-owned files
		rmCmd := exec.CommandWithOutput("sudo", "rm", "-rf", cfg.DataDir)
		if err := rmCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove data directory: %w", err)
		}
		l.Success("Removed data directory")
		l.Success("Cluster fully purged (binaries preserved)")
	} else {
		l.Success("Cluster purged (config removed, data preserved)")
		l.Info(fmt.Sprintf("To delete persistent data: sudo rm -rf %s", cfg.DataDir))
		l.Info("Or use 'obol stack purge --force' to remove everything")
	}

	return nil
}

// stackExists checks if stack name exists in k3d cluster list output
func stackExists(output, name string) bool {
	// Check if the stack name appears in the output
	return strings.Contains(output, name)
}

// getStackID reads the stored stack ID
func getStackID(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	data, err := os.ReadFile(stackIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// getStackName returns the full stack name (obol-stack-{stackid})
func getStackName(cfg *config.Config) string {
	stackID := getStackID(cfg)
	if stackID == "" {
		return ""
	}
	return fmt.Sprintf("obol-stack-%s", stackID)
}

// GetStackID reads the stored stack ID (exported for use in main)
func GetStackID(cfg *config.Config) string {
	return getStackID(cfg)
}
