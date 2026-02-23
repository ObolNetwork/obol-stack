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
	"github.com/dustinkirkland/golang-petname"
	"gopkg.in/yaml.v3"
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
func Install(cfg *config.Config, network string, overrides map[string]string, force bool) error {
	fmt.Printf("Installing network: %s\n", network)

	// Generate deployment ID if not provided in overrides (use petname)
	id, hasId := overrides["id"]
	if !hasId || id == "" {
		id = petname.Generate(2, "-")
		overrides["id"] = id
		fmt.Printf("Generated deployment ID: %s\n", id)
	} else {
		fmt.Printf("Using deployment ID: %s\n", id)
	}

	// Check if deployment already exists
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", network, id)
	if _, err := os.Stat(deploymentDir); err == nil {
		// Directory exists
		if !force {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite the existing configuration", network, id, deploymentDir)
		}
		fmt.Printf("⚠️  WARNING: Overwriting existing deployment at %s\n", deploymentDir)
	}

	// Parse embedded values template to get fields
	fields, err := ParseTemplateFields(network)
	if err != nil {
		return fmt.Errorf("failed to parse embedded values template: %w", err)
	}

	// Build template data from CLI flags and defaults
	templateData := make(map[string]string)

	fmt.Println("Configuration:")
	fmt.Printf("  deployment id: %s (from directory structure)\n", id)

	// Process parsed fields
	for _, field := range fields {
		value := field.DefaultValue

		// Check if there's an override from CLI flags
		if overrideValue, ok := overrides[field.FlagName]; ok {
			value = overrideValue
			fmt.Printf("  %s = %s (from --%s)\n", field.Name, value, field.FlagName)
		} else if field.Required && value == "" {
			// Required field with no value provided
			return fmt.Errorf("missing required flag: --%s", field.FlagName)
		} else if value != "" {
			fmt.Printf("  %s = %s (default)\n", field.Name, value)
		} else {
			// Optional field with empty default
			fmt.Printf("  %s = (empty, optional)\n", field.Name)
		}

		// Add to template data using field name (e.g., "Network", "ExecutionClient")
		templateData[field.Name] = value
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

	// Validate that the generated content is valid YAML
	var yamlCheck interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &yamlCheck); err != nil {
		return fmt.Errorf("generated values.yaml contains invalid YAML syntax: %w\n"+
			"This may be caused by special characters in your input values.\n"+
			"Generated content:\n%s", err, buf.String())
	}

	// Create deployment directory in config: networks/<network>/<id>/
	// (deploymentDir already defined earlier for existence check)
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	fmt.Printf("Saving configuration to: %s\n", deploymentDir)

	// Write the templated values.yaml (plain YAML, no more templating)
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if err := os.WriteFile(valuesPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Copy network files (helmfile.yaml.gotmpl, Chart.yaml, templates/, etc.)
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
	fmt.Printf("  - helmfile.yaml.gotmpl: Deployment definition\n")
	fmt.Printf("\nTo deploy, run: obol network sync %s/%s\n", network, id)

	return nil
}

// SyncAll syncs all installed network deployments found in the config directory.
func SyncAll(cfg *config.Config) error {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	networkDirs, err := os.ReadDir(networksDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No networks installed.")
			return nil
		}
		return fmt.Errorf("could not read networks directory: %w", err)
	}

	var synced int
	for _, networkDir := range networkDirs {
		if !networkDir.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(networksDir, networkDir.Name()))
		if err != nil {
			continue
		}
		for _, deployment := range deployments {
			if !deployment.IsDir() {
				continue
			}
			identifier := fmt.Sprintf("%s/%s", networkDir.Name(), deployment.Name())
			fmt.Printf("─── Syncing %s ───\n", identifier)
			if err := Sync(cfg, identifier); err != nil {
				fmt.Printf("  Warning: failed to sync %s: %v\n", identifier, err)
				continue
			}
			synced++
			fmt.Println()
		}
	}

	if synced == 0 {
		fmt.Println("No networks installed. Use 'obol network install <network>' first.")
	} else {
		fmt.Printf("✓ Synced %d network deployment(s)\n", synced)
	}
	return nil
}

// Sync deploys or updates a network configuration to the cluster using helmfile
func Sync(cfg *config.Config, deploymentIdentifier string) error {
	// Parse deployment identifier (supports both "ethereum/knowing-wahoo" and "ethereum-knowing-wahoo")
	var networkName, deploymentID string

	// Try slash separator first
	if strings.Contains(deploymentIdentifier, "/") {
		parts := strings.SplitN(deploymentIdentifier, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}
		networkName = parts[0]
		deploymentID = parts[1]
	} else {
		// Try to split by first dash that separates network from ID
		// Network names are expected to be single words (ethereum, aztec)
		parts := strings.SplitN(deploymentIdentifier, "-", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}
		networkName = parts[0]
		deploymentID = parts[1]
	}

	fmt.Printf("Syncing deployment: %s/%s\n", networkName, deploymentID)

	// Locate deployment directory
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", networkName, deploymentID)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s\nDirectory: %s", deploymentIdentifier, deploymentDir)
	}

	// Check if helmfile.yaml.gotmpl or helmfile.yaml exists (prefer .gotmpl for Helmfile v1+)
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml.gotmpl")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		// Fallback to helmfile.yaml for backwards compatibility
		helmfilePath = filepath.Join(deploymentDir, "helmfile.yaml")
		if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
			return fmt.Errorf("helmfile.yaml or helmfile.yaml.gotmpl not found in deployment directory: %s", deploymentDir)
		}
	}

	// Check if values.yaml exists
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("values.yaml not found in deployment directory: %s", deploymentDir)
	}

	// Check if kubeconfig exists (cluster must be running)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	// Get helmfile binary path
	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	fmt.Printf("Deployment directory: %s\n", deploymentDir)
	fmt.Printf("Using: %s\n", filepath.Base(helmfilePath))
	fmt.Printf("Deployment ID: %s (from directory structure)\n", deploymentID)
	fmt.Printf("Running helmfile sync...\n\n")

	// Execute helmfile sync with explicit file, state-values-file, and id from directory structure
	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync",
		"--state-values-file", valuesPath,
		"--state-values-set", fmt.Sprintf("id=%s", deploymentID))
	cmd.Dir = deploymentDir // Run in deployment directory
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	fmt.Printf("\nDeployment synced successfully!\n")
	fmt.Printf("Namespace: %s-%s\n", networkName, deploymentID)

	// Register local node as eRPC upstream so the gateway routes through it
	if err := RegisterERPCUpstream(cfg, networkName, deploymentID); err != nil {
		fmt.Printf("  Warning: could not register eRPC upstream: %v\n", err)
	}

	fmt.Printf("\nTo check status: obol kubectl get all -n %s-%s\n", networkName, deploymentID)
	fmt.Printf("To view logs: obol kubectl logs -n %s-%s <pod-name>\n", networkName, deploymentID)
	fmt.Printf("To access dashboard: obol k9s -n %s-%s\n", networkName, deploymentID)

	return nil
}

// Delete removes the network deployment configuration and cluster resources
func Delete(cfg *config.Config, deploymentIdentifier string) error {
	// Parse deployment identifier (supports both "ethereum/knowing-wahoo" and "ethereum-knowing-wahoo")
	var networkName, deploymentID string

	// Try slash separator first
	if strings.Contains(deploymentIdentifier, "/") {
		parts := strings.SplitN(deploymentIdentifier, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}
		networkName = parts[0]
		deploymentID = parts[1]
	} else {
		// Try to split by first dash that separates network from ID
		parts := strings.SplitN(deploymentIdentifier, "-", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}
		networkName = parts[0]
		deploymentID = parts[1]
	}

	namespaceName := fmt.Sprintf("%s-%s", networkName, deploymentID)
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", networkName, deploymentID)

	fmt.Printf("Deleting deployment: %s/%s\n", networkName, deploymentID)
	fmt.Printf("Namespace: %s\n", namespaceName)
	fmt.Printf("Config directory: %s\n", deploymentDir)

	// Check if config directory exists
	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	// Check if namespace exists in cluster
	namespaceExists := false
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		// Cluster is running, check for namespace
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespaceName)
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	// Display what will be deleted
	fmt.Println("\nResources to be deleted:")
	if namespaceExists {
		fmt.Printf("  ✓ Kubernetes namespace: %s (including all resources)\n", namespaceName)
	} else {
		fmt.Printf("  - Kubernetes namespace: %s (not found)\n", namespaceName)
	}
	if configExists {
		fmt.Printf("  ✓ Configuration directory: %s\n", deploymentDir)
	} else {
		fmt.Printf("  - Configuration directory: %s (not found)\n", deploymentDir)
	}

	// Check if there's anything to delete
	if !namespaceExists && !configExists {
		return fmt.Errorf("deployment not found: %s", deploymentIdentifier)
	}

	// Deregister from eRPC before deleting the namespace
	if err := DeregisterERPCUpstream(cfg, networkName, deploymentID); err != nil {
		fmt.Printf("  Warning: could not deregister eRPC upstream: %v\n", err)
	}

	// Delete Kubernetes namespace
	if namespaceExists {
		fmt.Printf("\nDeleting namespace %s...\n", namespaceName)
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespaceName, "--force", "--grace-period=0")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		fmt.Println("Namespace deleted successfully")
	}

	// Delete configuration directory
	if configExists {
		fmt.Printf("Deleting configuration directory...\n")
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		fmt.Println("Configuration deleted successfully")

		// Check if parent network directory is empty and remove it
		networkDir := filepath.Join(cfg.ConfigDir, "networks", networkName)
		entries, err := os.ReadDir(networkDir)
		if err == nil && len(entries) == 0 {
			os.Remove(networkDir) // Clean up empty network directory
		}
	}

	fmt.Printf("\n✓ Deployment %s/%s deleted successfully!\n", networkName, deploymentID)

	return nil
}
