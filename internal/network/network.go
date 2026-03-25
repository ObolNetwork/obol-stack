package network

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	petname "github.com/dustinkirkland/golang-petname"
	"gopkg.in/yaml.v3"
)

// List displays all available networks from the embedded filesystem
func List(cfg *config.Config, u *ui.UI) error {
	availableNetworks, err := embed.GetAvailableNetworks()
	if err != nil {
		return fmt.Errorf("failed to get available networks: %w", err)
	}

	if len(availableNetworks) == 0 {
		u.Print("No embedded networks found")
		return nil
	}

	u.Bold("Available networks:")

	for _, network := range availableNetworks {
		u.Printf("  • %s", network)
	}

	u.Blank()
	u.Dim(fmt.Sprintf("Total: %d network(s) available", len(availableNetworks)))

	return nil
}

// Install creates a network configuration by executing Go templates and saving to config directory
func Install(cfg *config.Config, u *ui.UI, network string, overrides map[string]string, force bool) error {
	u.Infof("Installing network: %s", network)

	// Generate deployment ID if not provided.
	// Default to the network name (e.g., "mainnet", "hoodi", "sepolia") so that
	// the first install of each network type gets a human-readable ID. If that
	// directory already exists, fall back to a petname.
	id, hasId := overrides["id"]
	if !hasId || id == "" {
		// Resolve the network name from --network flag or template default.
		networkValue := overrides["network"]
		if networkValue == "" {
			// Fall back to the template's default value for the "network" field.
			if fields, err := ParseTemplateFields(network); err == nil {
				for _, f := range fields {
					if f.FlagName == "network" && f.DefaultValue != "" {
						networkValue = f.DefaultValue
						break
					}
				}
			}
		}

		if networkValue != "" {
			candidateDir := filepath.Join(cfg.ConfigDir, "networks", network, networkValue)
			if _, err := os.Stat(candidateDir); os.IsNotExist(err) {
				id = networkValue
			}
		}

		if id == "" {
			id = petname.Generate(2, "-")
		}

		overrides["id"] = id
		u.Detail("Deployment ID", id+" (generated)")
	} else {
		u.Detail("Deployment ID", id)
	}

	// Check if deployment already exists
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", network, id)
	if _, err := os.Stat(deploymentDir); err == nil {
		if !force {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite the existing configuration", network, id, deploymentDir)
		}

		u.Warnf("Overwriting existing deployment at %s", deploymentDir)
	}

	// Parse embedded values template to get fields
	fields, err := ParseTemplateFields(network)
	if err != nil {
		return fmt.Errorf("failed to parse embedded values template: %w", err)
	}

	// Build template data from CLI flags and defaults
	templateData := make(map[string]string)

	u.Blank()
	u.Print("Configuration:")
	u.Detail("deployment id", id+" (from directory structure)")

	// Process parsed fields
	for _, field := range fields {
		value := field.DefaultValue

		if overrideValue, ok := overrides[field.FlagName]; ok {
			value = overrideValue
			u.Detail(field.Name, fmt.Sprintf("%s (from --%s)", value, field.FlagName))
		} else if field.Required && value == "" {
			return fmt.Errorf("missing required flag: --%s", field.FlagName)
		} else if value != "" {
			u.Detail(field.Name, value+" (default)")
		} else {
			u.Detail(field.Name, "(empty, optional)")
		}

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
	var yamlCheck any
	if err := yaml.Unmarshal(buf.Bytes(), &yamlCheck); err != nil {
		return fmt.Errorf("generated values.yaml contains invalid YAML syntax: %w\n"+
			"This may be caused by special characters in your input values.\n"+
			"Generated content:\n%s", err, buf.String())
	}

	// Create deployment directory
	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Write the templated values.yaml
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if err := os.WriteFile(valuesPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Copy network files (helmfile.yaml.gotmpl, Chart.yaml, templates/, etc.)
	if err := embed.CopyNetwork(network, deploymentDir); err != nil {
		return fmt.Errorf("failed to copy network files: %w", err)
	}

	// Remove values.yaml.gotmpl if it was copied
	valuesTemplatePath := filepath.Join(deploymentDir, "values.yaml.gotmpl")
	os.Remove(valuesTemplatePath)

	u.Blank()
	u.Successf("Network %s/%s configured", network, id)
	u.Detail("Location", deploymentDir)
	u.Blank()
	u.Printf("To deploy, run: obol network sync %s/%s", network, id)

	return nil
}

// SyncAll syncs all installed network deployments found in the config directory.
func SyncAll(cfg *config.Config, u *ui.UI) error {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")

	networkDirs, err := os.ReadDir(networksDir)
	if err != nil {
		if os.IsNotExist(err) {
			u.Print("No networks installed.")
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
			u.Infof("Syncing %s", identifier)

			if err := Sync(cfg, u, identifier); err != nil {
				u.Warnf("Failed to sync %s: %v", identifier, err)
				continue
			}

			synced++
		}
	}

	if synced == 0 {
		u.Print("No networks installed. Use 'obol network install <network>' first.")
	} else {
		u.Successf("Synced %d network deployment(s)", synced)
	}

	return nil
}

// Sync deploys or updates a network configuration to the cluster using helmfile
func Sync(cfg *config.Config, u *ui.UI, deploymentIdentifier string) error {
	// Parse deployment identifier
	var networkName, deploymentID string

	if strings.Contains(deploymentIdentifier, "/") {
		parts := strings.SplitN(deploymentIdentifier, "/", 2)
		if len(parts) != 2 {
			return errors.New("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}

		networkName = parts[0]
		deploymentID = parts[1]
	} else {
		parts := strings.SplitN(deploymentIdentifier, "-", 2)
		if len(parts) != 2 {
			return errors.New("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}

		networkName = parts[0]
		deploymentID = parts[1]
	}

	// Locate deployment directory
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", networkName, deploymentID)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s\nDirectory: %s", deploymentIdentifier, deploymentDir)
	}

	// Check helmfile exists
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml.gotmpl")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		helmfilePath = filepath.Join(deploymentDir, "helmfile.yaml")
		if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
			return fmt.Errorf("helmfile not found in deployment directory: %s", deploymentDir)
		}
	}

	// Check values.yaml exists
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("values.yaml not found in deployment directory: %s", deploymentDir)
	}

	// Check kubeconfig (cluster must be running)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	// Execute helmfile sync
	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync",
		"--state-values-file", valuesPath,
		"--state-values-set", "id="+deploymentID)
	cmd.Dir = deploymentDir

	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
	)

	if err := u.Exec(ui.ExecConfig{
		Name: fmt.Sprintf("Deploying %s/%s", networkName, deploymentID),
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	// Register local node as eRPC upstream
	if err := RegisterERPCUpstream(cfg, networkName, deploymentID); err != nil {
		u.Warnf("Could not register eRPC upstream: %v", err)
	} else {
		u.Successf("Registered local-%s-%s with eRPC", networkName, deploymentID)
	}

	u.Blank()
	u.Successf("Deployment %s/%s synced", networkName, deploymentID)
	u.Dim(fmt.Sprintf("  Namespace: %s-%s", networkName, deploymentID))
	u.Dim(fmt.Sprintf("  Status:    obol kubectl get all -n %s-%s", networkName, deploymentID))

	return nil
}

// Delete removes the network deployment configuration and cluster resources
func Delete(cfg *config.Config, u *ui.UI, deploymentIdentifier string) error {
	// Parse deployment identifier
	var networkName, deploymentID string

	if strings.Contains(deploymentIdentifier, "/") {
		parts := strings.SplitN(deploymentIdentifier, "/", 2)
		if len(parts) != 2 {
			return errors.New("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}

		networkName = parts[0]
		deploymentID = parts[1]
	} else {
		parts := strings.SplitN(deploymentIdentifier, "-", 2)
		if len(parts) != 2 {
			return errors.New("invalid deployment identifier format. Use: <network>/<id> or <network>-<id>")
		}

		networkName = parts[0]
		deploymentID = parts[1]
	}

	namespaceName := fmt.Sprintf("%s-%s", networkName, deploymentID)
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", networkName, deploymentID)

	u.Infof("Deleting deployment: %s/%s", networkName, deploymentID)

	// Check if config directory exists
	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	// Check if namespace exists in cluster
	namespaceExists := false

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespaceName)

		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("deployment not found: %s", deploymentIdentifier)
	}

	// Deregister from eRPC before deleting the namespace
	if err := DeregisterERPCUpstream(cfg, networkName, deploymentID); err != nil {
		u.Warnf("Could not deregister eRPC upstream: %v", err)
	} else {
		u.Successf("Deregistered local-%s-%s from eRPC", networkName, deploymentID)
	}

	// Delete Kubernetes namespace
	if namespaceExists {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespaceName, "--force", "--grace-period=0")

		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
		if err := u.Exec(ui.ExecConfig{
			Name: "Deleting namespace " + namespaceName,
			Cmd:  cmd,
		}); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
	}

	// Delete configuration directory
	if configExists {
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}

		u.Success("Configuration deleted")

		// Clean up empty parent directory
		networkDir := filepath.Join(cfg.ConfigDir, "networks", networkName)

		entries, err := os.ReadDir(networkDir)
		if err == nil && len(entries) == 0 {
			os.Remove(networkDir)
		}
	}

	u.Successf("Deployment %s/%s deleted", networkName, deploymentID)

	return nil
}
