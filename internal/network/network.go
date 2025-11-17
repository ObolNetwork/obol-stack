package network

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/executor"
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

	// Display each network
	for _, network := range availableNetworks {
		l.Info(fmt.Sprintf("  • %s", network))
	}

	l.Info("")
	l.Info(fmt.Sprintf("Total: %d network(s) available", len(availableNetworks)))

	return nil
}

// Install deploys a network by extracting it to a temp directory and running helmfile sync
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

	// Parse embedded helmfile to get environment variables
	envVars, err := ParseEmbeddedNetworkEnvVars(network)
	if err != nil {
		l.Error("Failed to parse embedded helmfile", "error", err.Error())
		return fmt.Errorf("failed to parse embedded helmfile: %w", err)
	}

	// Display configuration and set environment variables
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

			// Set environment variable in process for helmfile to read
			os.Setenv(envVar.Name, value)
		}
	}

	// Create temporary directory for network files
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("obol-network-%s-*", network))
	if err != nil {
		l.Error("Failed to create temp directory", "error", err.Error())
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	l.Info(fmt.Sprintf("Extracting network to temporary directory: %s", tmpDir))

	// Copy embedded network to temp directory
	if err := embed.CopyNetwork(network, tmpDir); err != nil {
		l.Error("Failed to copy network", "error", err.Error())
		return fmt.Errorf("failed to copy network: %w", err)
	}

	// Rename helmfile.yaml to helmfile.yaml.gotmpl (required by helmfile v1 for templating)
	oldPath := filepath.Join(tmpDir, "helmfile.yaml")
	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml.gotmpl")
	if err := os.Rename(oldPath, helmfilePath); err != nil {
		l.Error("Failed to rename helmfile", "error", err.Error())
		return fmt.Errorf("failed to rename helmfile: %w", err)
	}

	l.Info(fmt.Sprintf("Deploying network via helmfile sync"))

	// Create executor with binDir for helmfile access
	exec := executor.NewWithBinDir(l.Logger, cfg.BinDir)
	cmd := exec.CommandWithOutput("helmfile", "-f", helmfilePath, "sync")

	if err := cmd.Run(); err != nil {
		l.Error("Helmfile sync failed", "error", err.Error())
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	l.Success(fmt.Sprintf("Network %s installed successfully", network))

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
