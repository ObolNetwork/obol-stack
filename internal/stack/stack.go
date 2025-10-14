package stack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
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
	stackConfigDir := filepath.Join(cfg.ConfigDir, "cluster")
	k3dConfigPath := filepath.Join(stackConfigDir, k3dConfigFile)

	// Check if config already exists
	if _, err := os.Stat(k3dConfigPath); err == nil {
		if !force {
			return fmt.Errorf("stack configuration already exists at %s\nUse --force to overwrite", k3dConfigPath)
		}
		fmt.Printf("Overwriting existing stack configuration at %s\n", k3dConfigPath)
	}

	if err := os.MkdirAll(stackConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Generate unique stack ID
	stackID := petname.Generate(2, "-")

	// Replace placeholder in k3d config with actual stack ID
	k3dConfig := strings.ReplaceAll(embed.K3dConfig, "{{STACK_ID}}", stackID)

	// Write k3d config with stack ID to destination
	if err := os.WriteFile(k3dConfigPath, []byte(k3dConfig), 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	// Store stack ID for later use
	stackIDPath := filepath.Join(stackConfigDir, stackIDFile)
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	fmt.Printf("✓ Initialized stack configuration at %s\n", k3dConfigPath)
	fmt.Printf("✓ Stack ID: %s\n", stackID)
	return nil
}

// Up starts the k3d cluster
func Up(cfg *config.Config) error {
	stackConfigDir := filepath.Join(cfg.ConfigDir, "cluster")
	k3dConfigPath := filepath.Join(stackConfigDir, k3dConfigFile)
	kubeconfigPath := filepath.Join(stackConfigDir, kubeconfigFile)

	// Check if config exists
	if _, err := os.Stat(k3dConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack config not found, run 'obol stack init' first")
	}

	// Get stack ID and full stack name
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}
	stackName := getStackName(cfg)

	// Check if stack already exists using cluster list
	cmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	output, _ := cmd.Output()
	if stackExists(string(output), stackName) {
		return fmt.Errorf("stack '%s' already exists, use 'obol stack down' to stop it first", stackName)
	}

	fmt.Printf("Starting stack '%s' [%s]...\n", stackName, stackID)

	// Get absolute path to data directory for k3d volume mount
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
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

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	// Export kubeconfig
	cmd = exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"kubeconfig", "get", stackName,
	)
	kubeconfigData, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	fmt.Printf("✓ Stack started successfully\n")
	if stackID != "" {
		fmt.Printf("✓ Stack ID: %s\n", stackID)
	}
	fmt.Printf("✓ Kubeconfig saved to %s\n", kubeconfigPath)
	fmt.Printf("\nTo use kubectl with this stack:\n")
	fmt.Printf("  export KUBECONFIG=%s\n", kubeconfigPath)
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
	}
	stackName := getStackName(cfg)

	fmt.Printf("Stopping stack '%s' [%s]...\n", stackName, stackID)

	cmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "delete", stackName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop stack: %w", err)
	}

	fmt.Printf("✓ Stack stopped successfully\n")
	return nil
}

// Purge deletes the cluster and all data (except binaries)
func Purge(cfg *config.Config) error {
	// Stop stack first
	if err := Down(cfg); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// Remove stack config directory
	stackConfigDir := filepath.Join(cfg.ConfigDir, "cluster")
	if err := os.RemoveAll(stackConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}
	fmt.Printf("✓ Removed cluster config directory\n")

	// Remove data directory
	if err := os.RemoveAll(cfg.DataDir); err != nil {
		return fmt.Errorf("failed to remove data directory: %w", err)
	}
	fmt.Printf("✓ Removed data directory\n")

	// Remove state directory (logs, history)
	if err := os.RemoveAll(cfg.StateDir); err != nil {
		return fmt.Errorf("failed to remove state directory: %w", err)
	}
	fmt.Printf("✓ Removed state directory\n")

	fmt.Printf("✓ Cluster purged (binaries preserved)\n")
	return nil
}

// stackExists checks if stack name exists in k3d cluster list output
func stackExists(output, name string) bool {
	// Check if the stack name appears in the output
	return strings.Contains(output, name)
}

// getStackID reads the stored stack ID
func getStackID(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, "cluster", stackIDFile)
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
