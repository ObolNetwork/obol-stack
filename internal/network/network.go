package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// TODO: Network Management System
//
// The network system manages blockchain network configurations using embedded helmfiles.
//
// Architecture:
//   - Embedded networks: internal/embed/networks/<network>/helmfile.yaml
//   - Installed networks: $OBOL_CONFIG_DIR/networks/<network>/helmfile.yaml
//   - Each network may configure endpoints that are proxied through ERPC
//
// Implementation needed:
//   1. List() - Traverse and display available networks from internal/embed/networks
//   2. Install(cfg, network, overrides) - Copy embedded network to OBOL_CONFIG_DIR/networks and deploy via helmfile sync
//   3. Delete(cfg, network) - Remove network config and associated k8s namespaces
//
// See: plan.md for detailed design

// List displays all available networks from the embedded filesystem
func List(cfg *config.Config) error {
	fmt.Println("Available networks:")

	// Get all available networks from embedded FS
	availableNetworks, err := embed.GetAvailableNetworks()
	if err != nil {
		return fmt.Errorf("failed to get available networks: %w", err)
	}

	if len(availableNetworks) == 0 {
		fmt.Println("No embedded networks found")
		return nil
	}

	// Display each network
	for _, network := range availableNetworks {
		fmt.Printf("  • %s\n", network)
	}

	fmt.Printf("\nTotal: %d network(s) available\n", len(availableNetworks))

	return nil
}

// Install deploys a network by extracting it to a temp directory and running helmfile sync
func Install(cfg *config.Config, network string, overrides map[string]string) error {
	fmt.Printf("Installing network: %s\n", network)

	// Parse embedded helmfile to get environment variables
	envVars, err := ParseEmbeddedNetworkEnvVars(network)
	if err != nil {
		return fmt.Errorf("failed to parse embedded helmfile: %w", err)
	}

	// Display configuration and set environment variables
	if len(envVars) > 0 {
		fmt.Println("Configuration:")
		for _, envVar := range envVars {
			value := envVar.DefaultValue

			// Check if there's an override from CLI flags
			if overrideValue, ok := overrides[envVar.FlagName]; ok {
				value = overrideValue
				fmt.Printf("  %s = %s (from --%s)\n", envVar.Name, value, envVar.FlagName)
			} else {
				fmt.Printf("  %s = %s (default)\n", envVar.Name, value)
			}

			// Set environment variable in process for helmfile to read
			os.Setenv(envVar.Name, value)
		}
	}

	// Create temporary directory for network files
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("obol-network-%s-*", network))
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("Extracting network to temporary directory: %s\n", tmpDir)

	// Copy embedded network to temp directory
	if err := embed.CopyNetwork(network, tmpDir); err != nil {
		return fmt.Errorf("failed to copy network: %w", err)
	}

	// Use .yaml.gotmpl extension so helmfile processes Go templates
	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml.gotmpl")
	fmt.Println("Deploying network via helmfile sync")

	// Get kubeconfig path
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Build helmfile command with PATH including binDir
	cmd := exec.Command("helmfile", "-f", helmfilePath, "sync")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set PATH to include binDir so helmfile can be found
	pathEnv := os.Getenv("PATH")
	if cfg.BinDir != "" {
		if !strings.Contains(pathEnv, cfg.BinDir) {
			pathEnv = cfg.BinDir + string(os.PathListSeparator) + pathEnv
		}
	}

	// Set environment with PATH and KUBECONFIG
	cmd.Env = append(os.Environ(),
		"PATH="+pathEnv,
		"KUBECONFIG="+kubeconfigPath,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	fmt.Printf("Network %s installed successfully\n", network)

	return nil
}

// Delete removes the network configuration and cluster resources
func Delete(cfg *config.Config, network string, force bool) error {
	networkDir := filepath.Join(cfg.ConfigDir, "networks", network)

	// Check if network is installed
	if _, err := os.Stat(networkDir); os.IsNotExist(err) {
		return fmt.Errorf("network %s is not installed", network)
	}

	if !force {
		fmt.Printf("Are you sure you want to delete network '%s'? [y/N]: ", network)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" {
			return fmt.Errorf("operation cancelled")
		}
	}

	fmt.Printf("Deleting network: %s\n", network)

	// 1. Run helmfile destroy
	fmt.Println("Destroying network resources via helmfile...")

	helmfilePath := filepath.Join(networkDir, "helmfile.yaml.gotmpl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Build helmfile command
	cmd := exec.Command("helmfile", "-f", helmfilePath, "destroy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set PATH and KUBECONFIG
	pathEnv := os.Getenv("PATH")
	if cfg.BinDir != "" {
		if !strings.Contains(pathEnv, cfg.BinDir) {
			pathEnv = cfg.BinDir + string(os.PathListSeparator) + pathEnv
		}
	}

	cmd.Env = append(os.Environ(),
		"PATH="+pathEnv,
		"KUBECONFIG="+kubeconfigPath,
	)

	// We attempt destroy even if it might fail, to proceed to cleanup
	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: helmfile destroy failed: %v\n", err)
		if !force {
			fmt.Println("Use --force to delete the configuration anyway.")
			return fmt.Errorf("failed to destroy network resources: %w", err)
		}
		fmt.Println("Proceeding with cleanup due to --force...")
	}

	// 2. Remove network directory
	fmt.Printf("Removing network configuration: %s\n", networkDir)
	if err := os.RemoveAll(networkDir); err != nil {
		return fmt.Errorf("failed to remove network directory: %w", err)
	}

	fmt.Printf("Network %s deleted successfully\n", network)
	return nil
}
