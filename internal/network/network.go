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
func Install(cfg *config.Config, network string, overrides map[string]string) error {
	fmt.Printf("Installing network: %s\n", network)

	// Generate deployment ID if not provided in overrides (use petname)
	id, hasId := overrides["id"]
	if !hasId || id == "" {
		id = petname.Generate(2, "-")
		overrides["id"] = id
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

	// Read the embedded values template
	valuesContent, err := embed.ReadEmbeddedNetworkFile(network, "values.yaml.gotmpl")
	if err != nil {
		return fmt.Errorf("failed to read embedded values: %w", err)
	}

	// Parse and execute the Go template for values
	tmpl, err := template.New("values").Parse(string(valuesContent))
	if err != nil {
		return fmt.Errorf("failed to parse values template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return fmt.Errorf("failed to execute values template: %w", err)
	}

	// Create deployment directory in config: networks/<network>/<id>/
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", network, id)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	fmt.Printf("Saving configuration to: %s\n", deploymentDir)

	// Write the templated values.yaml (plain YAML, no more templating)
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if err := os.WriteFile(valuesPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Copy network files (helmfile.yaml, Chart.yaml, templates/, etc.)
	// This will copy helmfile.yaml as-is (no templating)
	if err := embed.CopyNetwork(network, deploymentDir); err != nil {
		return fmt.Errorf("failed to copy network files: %w", err)
	}

	// Remove values.yaml.gotmpl if it was copied (we already generated values.yaml)
	valuesTemplatePath := filepath.Join(deploymentDir, "values.yaml.gotmpl")
	os.Remove(valuesTemplatePath) // Ignore error if file doesn't exist

	fmt.Printf("\nNetwork configuration saved successfully!\n")
	fmt.Printf("Deployment: %s/%s\n", network, id)
	fmt.Printf("Location: %s\n", deploymentDir)
	fmt.Printf("\nFiles generated:\n")
	fmt.Printf("  - values.yaml: Configuration values\n")
	fmt.Printf("  - helmfile.yaml: Deployment definition\n")
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
