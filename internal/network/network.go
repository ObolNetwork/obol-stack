package network

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/logging"
	"github.com/ObolNetwork/obol-stack/internal/stack"
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

// getInstalledNetworks returns a list of installed network names
func getInstalledNetworks(cfg *config.Config) []string {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	var installed []string

	// Read installed networks directory if it exists
	if _, err := os.Stat(networksDir); err == nil {
		entries, err := os.ReadDir(networksDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					installed = append(installed, entry.Name())
				}
			}
		}
	}

	return installed
}

// List displays all available networks from the embedded filesystem
func List(cfg *config.Config) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info("Available networks:")

	// Get all available networks from embedded FS
	availableNetworks, err := embed.GetAvailableNetworks()
	if err != nil {
		l.Error("Failed to get available networks", "error", err.Error())
		return fmt.Errorf("failed to get available networks: %w", err)
	}

	if len(availableNetworks) == 0 {
		l.Warn("No embedded networks found")
		return nil
	}

	// Get installed networks
	installedNetworksList := getInstalledNetworks(cfg)
	installedNetworksMap := make(map[string]bool)
	for _, network := range installedNetworksList {
		installedNetworksMap[network] = true
	}

	// Display each network with status
	for _, network := range availableNetworks {
		if installedNetworksMap[network] {
			l.Info(fmt.Sprintf("  • %s (installed)", network))
		} else {
			l.Info(fmt.Sprintf("  • %s", network))
		}
	}

	l.Info("")
	l.Info(fmt.Sprintf("Total: %d network(s) available, %d installed",
		len(availableNetworks), len(installedNetworksList)))

	return nil
}

// ensureNetworkCopied copies an embedded network configuration to the config directory if not present
func ensureNetworkCopied(cfg *config.Config, network string, l *logging.Logger) error {
	// Check if network exists in embedded FS
	availableNetworks, err := embed.GetAvailableNetworks()
	if err != nil {
		return fmt.Errorf("failed to get available networks: %w", err)
	}

	found := false
	for _, n := range availableNetworks {
		if n == network {
			found = true
			break
		}
	}

	if !found {
		l.Error(fmt.Sprintf("Network %s not found", network))
		l.Info("Available networks:")
		for _, n := range availableNetworks {
			l.Info(fmt.Sprintf("  • %s", n))
		}
		return fmt.Errorf("network %s not found", network)
	}

	// Check if already present
	destDir := filepath.Join(cfg.ConfigDir, "networks", network)
	if _, err := os.Stat(destDir); err == nil {
		// Already present, nothing to do
		return nil
	}

	// Copy network from embedded FS to config directory
	l.Info(fmt.Sprintf("Copying network to %s", destDir))
	if err := embed.CopyNetwork(network, destDir); err != nil {
		return fmt.Errorf("failed to copy network: %w", err)
	}

	return nil
}

// Install copies the network to config directory (if needed) and deploys it using helmfile
func Install(cfg *config.Config, network string, overrides map[string]string) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Installing network: %s", network))

	// Ensure network is copied to config directory
	if err := ensureNetworkCopied(cfg, network, l); err != nil {
		l.Error("Failed to copy network", "error", err.Error())
		return err
	}

	// Get helmfile path
	helmfilePath := getNetworkHelmfilePath(cfg.ConfigDir, network)

	// Parse helmfile to get environment variables
	envVars, err := parseHelmfileEnvVars(helmfilePath)
	if err != nil {
		l.Error("Failed to parse helmfile", "error", err.Error())
		return fmt.Errorf("failed to parse helmfile: %w", err)
	}

	// Display configuration
	if len(envVars) > 0 {
		l.Info("Configuration:")
		for _, envVar := range envVars {
			value := envVar.DefaultValue

			// Check if there's an override from CLI flags
			if overrideValue, ok := overrides[envVar.FlagName]; ok {
				value = overrideValue
				l.Info(fmt.Sprintf("  %s = %s (from --%s)", envVar.Name, value, envVar.FlagName))
			} else {
				l.Info(fmt.Sprintf("  %s = %s (default)", envVar.Name, value))
			}

			// Set environment variable for helmfile
			os.Setenv(envVar.Name, value)
		}
	}

	l.Warn("TODO: Execute helmfile sync")
	l.Warn("  1. Run: helmfile -f " + helmfilePath + " sync")
	l.Warn("  2. Handle ERPC re-templating if needed")
	l.Warn("  3. Report deployment status")

	return nil
}

// Delete removes the network configuration and cluster resources
func Delete(cfg *config.Config, network string, force bool) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Deleting network: %s", network))
	l.Warn("TODO: Implement network deletion")
	l.Warn("  1. Remove $OBOL_CONFIG_DIR/networks/{network}")
	l.Warn("  2. Identify and delete associated k8s namespaces")
	l.Warn("  3. Handle ERPC re-configuration if needed")
	l.Warn("  4. Confirm cleanup completion")

	return nil
}
