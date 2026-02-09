package stack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
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
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Check if stack ID already exists (preserve on --force)
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	var stackID string
	if existingID, err := os.ReadFile(stackIDPath); err == nil {
		stackID = string(existingID)
		fmt.Printf("Preserving existing stack ID: %s (use purge to reset)\n", stackID)
	} else {
		// Generate unique stack ID only if one doesn't exist
		stackID = petname.Generate(2, "-")
	}

	fmt.Println("Initializing cluster configuration")
	fmt.Printf("Cluster ID: %s\n", stackID)

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
		fmt.Printf("Overwriting existing stack configuration: %s\n", k3dConfigPath)
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

	fmt.Printf("K3d config saved to: %s\n", k3dConfigPath)

	// Copy embedded defaults (helmfile + charts for infrastructure)
	// Resolve placeholders: {{OLLAMA_HOST}} → host DNS for the cluster runtime.
	// k3d uses host.k3d.internal; bare k3s would use the node's gateway IP.
	ollamaHost := "host.k3d.internal"
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if err := embed.CopyDefaults(defaultsDir, map[string]string{
		"{{OLLAMA_HOST}}": ollamaHost,
	}); err != nil {
		return fmt.Errorf("failed to copy defaults: %w", err)
	}
	fmt.Printf("Defaults copied to: %s\n", defaultsDir)

	// Store stack ID for later use (stackIDPath already declared above)
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	fmt.Printf("Initialized stack configuration: %s\n", k3dConfigPath)
	fmt.Printf("Stack ID: %s\n", stackID)
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

	stackName := getStackName(cfg)

	// Check if cluster already exists using cluster list
	listCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	listCmdOutput, err := listCmd.Output()
	if err != nil {
		return fmt.Errorf("k3d list command failed: %w", err)
	}

	if stackExists(string(listCmdOutput), stackName) {
		// Cluster exists - check if it's stopped or running
		fmt.Printf("Stack already exists, attempting to start: %s (id: %s)\n", stackName, stackID)
		startCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "start", stackName)
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return fmt.Errorf("failed to start existing cluster: %w", err)
		}

		if err := syncDefaults(cfg, kubeconfigPath); err != nil {
			return err
		}

		fmt.Println("Stack restarted successfully")
		fmt.Printf("Stack ID: %s\n", stackID)
		return nil
	}

	fmt.Printf("Starting stack: %s (id: %s)\n", stackName, stackID)

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
	fmt.Println("Creating k3d cluster...")
	createCmd := exec.Command(
		filepath.Join(cfg.BinDir, "k3d"),
		"cluster", "create", stackName,
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
	)
	createCmd.Stdout = os.Stdout
	createCmd.Stderr = os.Stderr

	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	// Export kubeconfig
	kubeconfigCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "kubeconfig", "get", stackName)
	kubeconfigData, err := kubeconfigCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	if err := syncDefaults(cfg, kubeconfigPath); err != nil {
		return err
	}

	fmt.Println("Stack started successfully")
	fmt.Printf("Stack ID: %s\n", stackID)
	fmt.Printf("export KUBECONFIG=%s\n", kubeconfigPath)
	fmt.Printf("Kubeconfig saved: %s\n", kubeconfigPath)
	return nil
}

// Down stops the k3d cluster
func Down(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
	}
	stackName := getStackName(cfg)

	fmt.Printf("Stopping stack gracefully: %s (id: %s)\n", stackName, stackID)

	// First attempt graceful stop (allows processes to shutdown gracefully)
	stopCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "stop", stackName)
	stopCmd.Stdout = os.Stdout
	stopCmd.Stderr = os.Stderr

	if err := stopCmd.Run(); err != nil {
		fmt.Println("Graceful stop timed out or failed, forcing cluster deletion")
		// Fallback to delete if stop fails
		deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
		deleteCmd.Stdout = os.Stdout
		deleteCmd.Stderr = os.Stderr
		if err := deleteCmd.Run(); err != nil {
			return fmt.Errorf("failed to stop cluster: %w", err)
		}
	}

	fmt.Println("Stack stopped successfully")
	return nil
}

// Purge deletes the cluster config and optionally data
func Purge(cfg *config.Config, force bool) error {
	// Delete cluster containers
	stackName := getStackName(cfg)
	if stackName != "" {
		if force {
			// Force delete without graceful shutdown
			fmt.Printf("Force deleting cluster containers: %s\n", stackName)
			deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
			deleteCmd.Stdout = os.Stdout
			deleteCmd.Stderr = os.Stderr
			if err := deleteCmd.Run(); err != nil {
				fmt.Printf("Failed to delete cluster (may already be deleted): %v\n", err)
			}
			fmt.Println("Cluster containers force deleted")
		} else {
			// Graceful shutdown first to ensure data is written properly
			fmt.Printf("Gracefully stopping cluster before deletion: %s\n", stackName)
			stopCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "stop", stackName)
			stopCmd.Stdout = os.Stdout
			stopCmd.Stderr = os.Stderr
			if err := stopCmd.Run(); err != nil {
				fmt.Println("Graceful stop timed out or failed, proceeding with deletion anyway")
			} else {
				fmt.Println("Cluster stopped gracefully")
			}

			// Now delete the stopped cluster
			fmt.Println("Deleting cluster containers")
			deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
			deleteCmd.Stdout = os.Stdout
			deleteCmd.Stderr = os.Stderr
			if err := deleteCmd.Run(); err != nil {
				fmt.Printf("Failed to delete cluster (may already be deleted): %v\n", err)
			}
			fmt.Println("Cluster containers deleted")
		}
	}

	// Remove stack config directory
	stackConfigDir := filepath.Join(cfg.ConfigDir)
	if err := os.RemoveAll(stackConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}
	fmt.Println("Removed cluster config directory")

	// Remove data directory only if force flag is set
	if force {
		// Use sudo to remove data directory since it may contain root-owned files
		fmt.Println("Removing data directory...")
		rmCmd := exec.Command("sudo", "rm", "-rf", cfg.DataDir)
		rmCmd.Stdout = os.Stdout
		rmCmd.Stderr = os.Stderr
		if err := rmCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove data directory: %w", err)
		}
		fmt.Println("Removed data directory")
		fmt.Println("Cluster fully purged (binaries preserved)")
	} else {
		fmt.Println("Cluster purged (config removed, data preserved)")
		fmt.Printf("To delete persistent data: sudo rm -rf %s\n", cfg.DataDir)
		fmt.Println("Or use 'obol stack purge --force' to remove everything")
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

// syncDefaults deploys the default infrastructure using helmfile
// If deployment fails, the cluster is automatically stopped via Down()
func syncDefaults(cfg *config.Config, kubeconfigPath string) error {
	fmt.Println("Deploying default infrastructure with helmfile")

	// Sync defaults using helmfile (handles Helm hooks properly)
	defaultsHelmfilePath := filepath.Join(cfg.ConfigDir, "defaults")
	helmfilePath := filepath.Join(defaultsHelmfilePath, "helmfile.yaml")

	// Compatibility migration: older defaults pinned HTTPRoutes to `obol.stack` via
	// `spec.hostnames`. This breaks public access for:
	// - quick tunnels (random *.trycloudflare.com host)
	// - user-provided DNS hostnames (e.g. agent.example.com)
	// Removing hostnames makes routes match all hostnames while preserving existing
	// path-based routing.
	if err := migrateDefaultsHTTPRouteHostnames(helmfilePath); err != nil {
		fmt.Printf("Warning: failed to migrate defaults helmfile hostnames: %v\n", err)
	}

	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"sync",
	)
	helmfileCmd.Stdout = os.Stdout
	helmfileCmd.Stderr = os.Stderr

	if err := helmfileCmd.Run(); err != nil {
		fmt.Println("Failed to apply defaults helmfile, stopping cluster")
		// Attempt to stop the cluster to clean up
		if downErr := Down(cfg); downErr != nil {
			fmt.Printf("Failed to stop cluster during cleanup: %v\n", downErr)
		}
		return fmt.Errorf("failed to apply defaults helmfile: %w", err)
	}

	fmt.Println("Default infrastructure deployed")

	// Deploy default OpenClaw instance (non-fatal on failure)
	fmt.Println("Setting up default OpenClaw instance...")
	if err := openclaw.SetupDefault(cfg); err != nil {
		fmt.Printf("Warning: failed to set up default OpenClaw: %v\n", err)
		fmt.Println("You can manually set up OpenClaw later with: obol openclaw up")
	}

	return nil
}

func migrateDefaultsHTTPRouteHostnames(helmfilePath string) error {
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return err
	}

	// Only removes the legacy default single-hostname block; if users customized their
	// helmfile with different hostnames, we leave it alone.
	needle := "              hostnames:\n                - obol.stack\n"
	s := string(data)
	if !strings.Contains(s, needle) {
		return nil
	}
	updated := strings.ReplaceAll(s, needle, "")
	if updated == s {
		return nil
	}
	return os.WriteFile(helmfilePath, []byte(updated), 0644)
}
