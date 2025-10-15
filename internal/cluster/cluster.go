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
)

const (
	k3dConfigFile  = "config.yaml"
	kubeconfigFile = "kubeconfig.yaml"
	clusterIDFile  = ".cluster-id"
)

// Init initializes the cluster configuration
func Init(cfg *config.Config, force bool) error {
	// Create cluster config directory
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster", "k3d")
	destPath := filepath.Join(clusterConfigDir, k3dConfigFile)

	// Check if config already exists
	if _, err := os.Stat(destPath); err == nil {
		if !force {
			return fmt.Errorf("cluster configuration already exists at %s\nUse --force to overwrite", destPath)
		}
		fmt.Printf("Overwriting existing cluster configuration at %s\n", destPath)
	}

	if err := os.MkdirAll(clusterConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster config dir: %w", err)
	}

	// Generate unique cluster ID
	clusterID := petname.Generate(2, "-")

	// Replace placeholder in k3d config with actual cluster ID
	k3dConfig := strings.ReplaceAll(embed.K3dConfig, "{{CLUSTER_ID}}", clusterID)

	// Write k3d config with cluster ID to destination
	if err := os.WriteFile(destPath, []byte(k3dConfig), 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	// Create kubeconfig directory
	kubeconfigDir := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig")
	if err := os.MkdirAll(kubeconfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create kubeconfig dir: %w", err)
	}

	// Store cluster ID for later use
	clusterIDPath := filepath.Join(cfg.ConfigDir, "cluster", clusterIDFile)
	if err := os.WriteFile(clusterIDPath, []byte(clusterID), 0644); err != nil {
		return fmt.Errorf("failed to write cluster ID: %w", err)
	}

	fmt.Printf("✓ Initialized cluster configuration at %s\n", destPath)
	fmt.Printf("✓ Cluster ID: %s\n", clusterID)
	return nil
}

// Up starts the k3d cluster
func Up(cfg *config.Config) error {
	k3dConfigPath := filepath.Join(cfg.ConfigDir, "cluster", "k3d", k3dConfigFile)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", kubeconfigFile)

	// Check if config exists
	if _, err := os.Stat(k3dConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster config not found, run 'obol cluster init' first")
	}

	// Get cluster ID and full cluster name
	clusterID := getClusterID(cfg)
	if clusterID == "" {
		return fmt.Errorf("cluster ID not found, run 'obol cluster init' first")
	}
	clusterName := getClusterName(cfg)

	// Check if cluster already exists using cluster list
	cmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	output, _ := cmd.Output()
	if clusterExists(string(output), clusterName) {
		return fmt.Errorf("cluster '%s' already exists, use 'obol cluster down' to stop it first", clusterName)
	}

	fmt.Printf("Starting cluster '%s' [%s]...\n", clusterName, clusterID)

	// Get absolute path to data directory for k3d volume mount
	dataDir := cfg.DataDir
	absDataDir, err := filepath.Abs(dataDir)
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
		"kubeconfig", "get", clusterName,
	)
	kubeconfigData, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	fmt.Printf("✓ Cluster started successfully\n")
	if clusterID != "" {
		fmt.Printf("✓ Cluster ID: %s\n", clusterID)
	}
	fmt.Printf("✓ Kubeconfig saved to %s\n", kubeconfigPath)
	fmt.Printf("\nTo use kubectl with this cluster:\n")
	fmt.Printf("  export KUBECONFIG=%s\n", kubeconfigPath)
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config) error {
	clusterID := getClusterID(cfg)
	if clusterID == "" {
		return fmt.Errorf("cluster ID not found, cluster may not be initialized")
	}
	clusterName := getClusterName(cfg)

	fmt.Printf("Stopping cluster '%s' [%s]...\n", clusterName, clusterID)

	cmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "delete", clusterName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop cluster: %w", err)
	}

	fmt.Printf("✓ Cluster stopped successfully\n")
	return nil
}

// Purge deletes the cluster and all data
func Purge(cfg *config.Config) error {
	// Stop cluster first
	if err := Down(cfg); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// Remove cluster config directory
	clusterConfigDir := filepath.Join(cfg.ConfigDir, "cluster")
	if err := os.RemoveAll(clusterConfigDir); err != nil {
		return fmt.Errorf("failed to remove cluster config: %w", err)
	}

	fmt.Printf("✓ Cluster configuration purged\n")
	return nil
}

// clusterExists checks if cluster name exists in k3d cluster list output
func clusterExists(output, name string) bool {
	// Check if the cluster name appears in the output
	return strings.Contains(output, name)
}

// getClusterID reads the stored cluster ID
func getClusterID(cfg *config.Config) string {
	clusterIDPath := filepath.Join(cfg.ConfigDir, "cluster", clusterIDFile)
	data, err := os.ReadFile(clusterIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// getClusterName returns the full cluster name (obol-stack-{clusterid})
func getClusterName(cfg *config.Config) string {
	clusterID := getClusterID(cfg)
	if clusterID == "" {
		return ""
	}
	return fmt.Sprintf("obol-stack-%s", clusterID)
}
