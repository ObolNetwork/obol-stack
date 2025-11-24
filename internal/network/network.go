package network

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/dustinkirkland/golang-petname"
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

// Install creates a network configuration by executing Go templates and saving to config directory
func Install(cfg *config.Config, network string, id string, overrides map[string]string) error {
	fmt.Printf("Installing network: %s\n", network)

	// Generate deployment ID if not provided (use petname)
	if id == "" {
		id = petname.Generate(2, "-")
		fmt.Printf("Generated deployment ID: %s\n", id)
	}

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
			} else if envVar.Required && value == "" {
				// Required field with no value provided
				return fmt.Errorf("missing required flag: --%s", envVar.FlagName)
			} else if value != "" {
				fmt.Printf("  %s = %s (default)\n", envVar.Name, value)
			} else {
				// Optional field with empty default
				fmt.Printf("  %s = (empty, optional)\n", envVar.Name)
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

	// Split helmfile into sections (separated by ---)
	// Only template the values section, preserve the rest for Helmfile to process
	parts := bytes.Split(helmfileContent, []byte("\n---\n"))

	var templatedParts [][]byte

	// Template only the first part (values section)
	if len(parts) > 0 {
		tmpl, err := template.New("values").Parse(string(parts[0]))
		if err != nil {
			return fmt.Errorf("failed to parse values template: %w", err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, templateData); err != nil {
			return fmt.Errorf("failed to execute values template: %w", err)
		}
		templatedParts = append(templatedParts, buf.Bytes())

		// Append remaining parts unchanged (releases, etc.)
		templatedParts = append(templatedParts, parts[1:]...)
	}

	// Rejoin with ---
	finalContent := bytes.Join(templatedParts, []byte("\n---\n"))

	// Create deployment directory in config: networks/<network>/<id>/
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", network, id)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	fmt.Printf("Saving configuration to: %s\n", deploymentDir)

	// Write the executed template to helmfile.yaml.gotmpl (keep .gotmpl for Helmfile to process)
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml.gotmpl")
	if err := os.WriteFile(helmfilePath, finalContent, 0644); err != nil {
		return fmt.Errorf("failed to write helmfile: %w", err)
	}

	// Copy any additional network files (Chart.yaml, templates/, etc.)
	if err := embed.CopyNetwork(network, deploymentDir); err != nil {
		return fmt.Errorf("failed to copy network files: %w", err)
	}

	// Overwrite the helmfile.yaml.gotmpl with our templated version
	// (CopyNetwork may have copied the original, so we overwrite it)
	if err := os.WriteFile(helmfilePath, finalContent, 0644); err != nil {
		return fmt.Errorf("failed to write helmfile: %w", err)
	}

	fmt.Printf("\nNetwork configuration saved successfully!\n")
	fmt.Printf("Deployment: %s/%s\n", network, id)
	fmt.Printf("Location: %s\n", deploymentDir)
	fmt.Printf("\nTo deploy, run: obol network sync %s/%s\n", network, id)

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
