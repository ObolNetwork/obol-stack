package network

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
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
//   2. Add(cfg, network) - Copy embedded network to OBOL_CONFIG_DIR/networks
//   3. Sync(cfg, network) - Deploy network via helmfile sync
//   4. Delete(cfg, network) - Remove network config and associated k8s namespaces
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

	l.Info("Listing available networks")
	l.Warn("TODO: Implement network listing")
	l.Warn("  1. Traverse internal/embed/networks directory")
	l.Warn("  2. Display each network name")
	l.Warn("  3. Indicate which networks are already installed")

	return nil
}

// Add copies an embedded network configuration to the config directory
func Add(cfg *config.Config, network string) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Adding network: %s", network))
	l.Warn("TODO: Implement network installation")
	l.Warn("  1. Validate network exists in internal/embed/networks")
	l.Warn("  2. Copy internal/embed/networks/{network} to $OBOL_CONFIG_DIR/networks/{network}")
	l.Warn("  3. Notify user that network is ready for sync")

	return nil
}

// Sync deploys the network using helmfile
func Sync(cfg *config.Config, network string) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Syncing network: %s", network))
	l.Warn("TODO: Implement network sync")
	l.Warn("  1. Verify network exists in $OBOL_CONFIG_DIR/networks/{network}")
	l.Warn("  2. Run: helmfile -f $OBOL_CONFIG_DIR/networks/{network}/helmfile.yaml sync")
	l.Warn("  3. Handle ERPC re-templating if needed")
	l.Warn("  4. Report deployment status")

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
