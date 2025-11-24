package network

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

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

// Install deploys a network by executing Go templates and running helmfile sync
func Install(cfg *config.Config, network string, overrides map[string]string) error {
	fmt.Printf("Installing network: %s\n", network)

	// Parse embedded helmfile to get template fields
	envVars, err := ParseEmbeddedNetworkEnvVars(network)
	if err != nil {
		return fmt.Errorf("failed to parse embedded helmfile: %w", err)
	}

	// Build template data from CLI flags and defaults
	templateData := make(map[string]string)
	if len(envVars) > 0 {
		fmt.Println("Configuration:")
		for _, envVar := range envVars {
			value := envVar.DefaultValue

			// Check if there's an override from CLI flags
			if overrideValue, ok := overrides[envVar.FlagName]; ok {
				value = overrideValue
				fmt.Printf("  %s = %s (from --%s)\n", envVar.Name, value, envVar.FlagName)
			} else if value != "" {
				fmt.Printf("  %s = %s (default)\n", envVar.Name, value)
			} else {
				// Required field with no value
				return fmt.Errorf("missing required flag: --%s", envVar.FlagName)
			}

			// Add to template data using field name (e.g., "Network", "ExecutionClient")
			templateData[envVar.Name] = value
		}
	}

	// Read the embedded helmfile template
	helmfileContent, err := embed.ReadEmbeddedNetworkFile(network, "helmfile.yaml.gotmpl")
	if err != nil {
		return fmt.Errorf("failed to read embedded helmfile: %w", err)
	}

	// Parse and execute the Go template
	tmpl, err := template.New("helmfile").Parse(string(helmfileContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Create temporary directory for network files
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("obol-network-%s-*", network))
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("Preparing network in temporary directory: %s\n", tmpDir)

	// Copy embedded network to temp directory (for any additional files like charts)
	if err := embed.CopyNetwork(network, tmpDir); err != nil {
		return fmt.Errorf("failed to copy network: %w", err)
	}

	// Write the executed template to helmfile.yaml (not .gotmpl since Go templating is done)
	helmfilePath := filepath.Join(tmpDir, "helmfile.yaml")
	if err := os.WriteFile(helmfilePath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write helmfile: %w", err)
	}

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
	fmt.Printf("Deleting network: %s\n", network)
	fmt.Println("TODO: Implement network deletion")
	fmt.Println("  1. Remove $OBOL_CONFIG_DIR/networks/{network}")
	fmt.Println("  2. Identify and delete associated k8s namespaces")
	fmt.Println("  3. Handle ERPC re-configuration if needed")
	fmt.Println("  4. Confirm cleanup completion")

	return nil
}
