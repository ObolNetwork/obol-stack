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
	kubeconfigFile = "kubeconfig.yaml"
	stackIDFile    = ".stack-id"
)

// Init initializes the stack configuration
func Init(cfg *config.Config, force bool, backendName string) error {
	// Check if any stack config already exists
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	backendFilePath := filepath.Join(cfg.ConfigDir, stackBackendFile)

	hasExistingConfig := false
	if _, err := os.Stat(stackIDPath); err == nil {
		hasExistingConfig = true
	}
	if _, err := os.Stat(backendFilePath); err == nil {
		hasExistingConfig = true
	}
	// Also check legacy k3d.yaml for backward compatibility
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, k3dConfigFile)); err == nil {
		hasExistingConfig = true
	}

	if hasExistingConfig && !force {
		return fmt.Errorf("stack configuration already exists at %s\nUse --force to overwrite", cfg.ConfigDir)
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Check if stack ID already exists (preserve on --force)
	var stackID string
	if existingID, err := os.ReadFile(stackIDPath); err == nil {
		stackID = strings.TrimSpace(string(existingID))
		fmt.Printf("Preserving existing stack ID: %s (use purge to reset)\n", stackID)
	} else {
		stackID = petname.Generate(2, "-")
	}

	// Default to k3d if no backend specified
	if backendName == "" {
		backendName = BackendK3d
	}

	backend, err := NewBackend(backendName)
	if err != nil {
		return err
	}

	fmt.Println("Initializing cluster configuration")
	fmt.Printf("Cluster ID: %s\n", stackID)
	fmt.Printf("Backend: %s\n", backend.Name())

	// Check prerequisites
	if err := backend.Prerequisites(cfg); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// Generate backend-specific config
	if err := backend.Init(cfg, stackID); err != nil {
		return err
	}

	// Copy embedded defaults (helmfile + charts for infrastructure)
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if err := embed.CopyDefaults(defaultsDir); err != nil {
		return fmt.Errorf("failed to copy defaults: %w", err)
	}
	fmt.Printf("Defaults copied to: %s\n", defaultsDir)

	// Store stack ID
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	// Save backend choice
	if err := SaveBackend(cfg, backendName); err != nil {
		return fmt.Errorf("failed to save backend choice: %w", err)
	}

	fmt.Printf("Initialized stack configuration\n")
	fmt.Printf("Stack ID: %s\n", stackID)
	return nil
}

// Up starts the cluster using the configured backend
func Up(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

	fmt.Printf("Starting stack (id: %s, backend: %s)\n", stackID, backend.Name())

	kubeconfigData, err := backend.Up(cfg, stackID)
	if err != nil {
		return err
	}

	// Write kubeconfig (backend may have already written it, but ensure consistency)
	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Sync defaults with backend-aware dataDir
	dataDir := backend.DataDir(cfg)
	if err := syncDefaults(cfg, kubeconfigPath, dataDir); err != nil {
		return err
	}

	fmt.Println("Stack started successfully")
	fmt.Printf("Stack ID: %s\n", stackID)
	fmt.Printf("export KUBECONFIG=%s\n", kubeconfigPath)
	fmt.Printf("Kubeconfig saved: %s\n", kubeconfigPath)
	return nil
}

// Down stops the cluster
func Down(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	return backend.Down(cfg, stackID)
}

// Purge deletes the cluster config and optionally data
func Purge(cfg *config.Config, force bool) error {
	stackID := getStackID(cfg)

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	// Destroy cluster if we have a stack ID
	if stackID != "" {
		if force {
			fmt.Printf("Force destroying cluster (id: %s)\n", stackID)
		} else {
			fmt.Printf("Destroying cluster (id: %s)\n", stackID)
		}
		if err := backend.Destroy(cfg, stackID); err != nil {
			fmt.Printf("Failed to destroy cluster (may already be deleted): %v\n", err)
		}
	}

	// Remove stack config directory
	if err := os.RemoveAll(cfg.ConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}
	fmt.Println("Removed cluster config directory")

	// Remove data directory only if force flag is set
	if force {
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

// getStackID reads the stored stack ID
func getStackID(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	data, err := os.ReadFile(stackIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// GetStackID reads the stored stack ID (exported for use in main)
func GetStackID(cfg *config.Config) string {
	return getStackID(cfg)
}

// syncDefaults deploys the default infrastructure using helmfile
// If deployment fails, the cluster is automatically stopped via Down()
func syncDefaults(cfg *config.Config, kubeconfigPath string, dataDir string) error {
	fmt.Println("Deploying default infrastructure with helmfile")

	defaultsHelmfilePath := filepath.Join(cfg.ConfigDir, "defaults")
	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		"--file", filepath.Join(defaultsHelmfilePath, "helmfile.yaml.gotmpl"),
		"--kubeconfig", kubeconfigPath,
		"sync",
	)
	helmfileCmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
		fmt.Sprintf("STACK_DATA_DIR=%s", dataDir),
	)
	helmfileCmd.Stdout = os.Stdout
	helmfileCmd.Stderr = os.Stderr

	if err := helmfileCmd.Run(); err != nil {
		fmt.Println("Failed to apply defaults helmfile, stopping cluster")
		if downErr := Down(cfg); downErr != nil {
			fmt.Printf("Failed to stop cluster during cleanup: %v\n", downErr)
		}
		return fmt.Errorf("failed to apply defaults helmfile: %w", err)
	}

	fmt.Println("Default infrastructure deployed")
	return nil
}
