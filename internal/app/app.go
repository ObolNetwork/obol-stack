package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/dustinkirkland/golang-petname"
)

// InstallOptions contains options for the install command
type InstallOptions struct {
	Name    string // Optional app name override
	Version string // Chart version (empty = latest for repo/chart, extracted for URL)
	ID      string // Deployment ID (empty = generate petname)
	Force   bool   // Overwrite existing deployment
}

// ListOptions contains options for the list command
type ListOptions struct {
	Verbose bool // Show detailed information
}

// Install scaffolds a new application from a Helm chart reference
func Install(cfg *config.Config, chartRef string, opts InstallOptions) error {
	fmt.Printf("Installing application from: %s\n", chartRef)

	// 1. Parse chart reference
	chart, err := ParseChartReference(chartRef)
	if err != nil {
		return err
	}

	// 2. If repo/chart format, resolve via ArtifactHub
	if chart.NeedsResolution() {
		fmt.Printf("Resolving chart via ArtifactHub...\n")
		client := NewArtifactHubClient()
		info, err := client.ResolveChart(chartRef)
		if err != nil {
			return err
		}
		chart.RepoURL = info.RepoURL
		chart.RepoName = info.RepoName
		if chart.Version == "" {
			chart.Version = info.Version
		}
		fmt.Printf("Resolved: %s/%s version %s\n", info.RepoName, info.ChartName, info.Version)
		fmt.Printf("Repository URL: %s\n", info.RepoURL)
	}

	// Apply version override from CLI flag
	if opts.Version != "" {
		chart.Version = opts.Version
	}

	// 3. Determine app name
	appName := opts.Name
	if appName == "" {
		appName = chart.GetChartName()
	}
	fmt.Printf("Application name: %s\n", appName)

	// 4. Generate or use provided ID
	id := opts.ID
	if id == "" {
		id = petname.Generate(2, "-")
		fmt.Printf("Generated deployment ID: %s\n", id)
	} else {
		fmt.Printf("Using deployment ID: %s\n", id)
	}

	// 5. Check if deployment exists
	deploymentDir := filepath.Join(cfg.ConfigDir, "applications", appName, id)
	if _, err := os.Stat(deploymentDir); err == nil {
		if !opts.Force {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, deploymentDir)
		}
		fmt.Printf("WARNING: Overwriting existing deployment at %s\n", deploymentDir)
	}

	// 6. Create deployment directory
	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// 7. Fetch default values using helm show values
	fmt.Printf("Fetching chart default values...\n")
	values, err := fetchChartValues(cfg, chart)
	if err != nil {
		// Clean up on failure
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to fetch chart values: %w", err)
	}

	// 8. Write values.yaml
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if err := os.WriteFile(valuesPath, values, 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// 9. Generate helmfile.yaml (references chart remotely)
	if err := generateRemoteHelmfile(deploymentDir, chart, appName, id); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to generate helmfile: %w", err)
	}

	// 10. Print success message
	fmt.Printf("\n✓ Application installed successfully!\n")
	fmt.Printf("Deployment: %s/%s\n", appName, id)
	fmt.Printf("Location: %s\n", deploymentDir)
	fmt.Printf("\nFiles created:\n")
	fmt.Printf("  - helmfile.yaml: Deployment configuration\n")
	fmt.Printf("  - values.yaml: Chart default values (edit to customize)\n")
	fmt.Printf("\nEdit values.yaml to customize your deployment.\n")
	fmt.Printf("To deploy, run: obol app sync %s/%s\n", appName, id)

	return nil
}

// fetchChartValues retrieves default values from a chart using helm show values
func fetchChartValues(cfg *config.Config, chart *ChartReference) ([]byte, error) {
	helmPath := filepath.Join(cfg.BinDir, "helm")

	var args []string
	switch chart.Format {
	case FormatURL:
		// Direct URL reference
		args = []string{"show", "values", chart.ChartURL}
	case FormatRepoChart:
		// Use repo URL directly without helm repo add
		args = []string{"show", "values", chart.ChartName, "--repo", chart.RepoURL}
		if chart.Version != "" {
			args = append(args, "--version", chart.Version)
		}
	case FormatOCI:
		// OCI reference
		args = []string{"show", "values", chart.ChartURL}
		if chart.Version != "" {
			args = append(args, "--version", chart.Version)
		}
	}

	cmd := exec.Command(helmPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("helm show values failed: %w\n%s", err, stderr.String())
	}

	// Return empty YAML if chart has no default values
	if stdout.Len() == 0 {
		return []byte("# No default values in chart\n"), nil
	}

	return stdout.Bytes(), nil
}

// generateRemoteHelmfile creates a helmfile.yaml that references the chart remotely
func generateRemoteHelmfile(dir string, chart *ChartReference, appName, id string) error {
	var tmpl string

	switch chart.Format {
	case FormatURL:
		// Direct URL reference
		tmpl = `# Installed from: {{ .Original }}

releases:
  - name: {{ .AppName }}
    namespace: {{ .Namespace }}
    createNamespace: true
    chart: {{ .ChartURL }}
{{- if .Version }}
    version: "{{ .Version }}"
{{- end }}
    values:
      - values.yaml
`
	case FormatRepoChart:
		// Repository reference (helmfile handles repo inline)
		tmpl = `# Installed from: {{ .Original }} (resolved via ArtifactHub)

repositories:
  - name: {{ .RepoName }}
    url: {{ .RepoURL }}

releases:
  - name: {{ .AppName }}
    namespace: {{ .Namespace }}
    createNamespace: true
    chart: {{ .RepoName }}/{{ .ChartName }}
    version: "{{ .Version }}"
    values:
      - values.yaml
`
	case FormatOCI:
		// OCI registry reference
		tmpl = `# Installed from: {{ .Original }}

releases:
  - name: {{ .AppName }}
    namespace: {{ .Namespace }}
    createNamespace: true
    chart: {{ .ChartURL }}
{{- if .Version }}
    version: "{{ .Version }}"
{{- end }}
    values:
      - values.yaml
`
	}

	data := map[string]interface{}{
		"Original":  chart.Original,
		"ChartURL":  chart.ChartURL,
		"RepoName":  chart.RepoName,
		"RepoURL":   chart.RepoURL,
		"ChartName": chart.ChartName,
		"Version":   chart.Version,
		"AppName":   appName,
		"Namespace": fmt.Sprintf("%s-%s", appName, id),
	}

	t, err := template.New("helmfile").Parse(tmpl)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "helmfile.yaml"), buf.Bytes(), 0644)
}

// Sync deploys or updates an application to the cluster
func Sync(cfg *config.Config, deploymentIdentifier string) error {
	// Parse deployment identifier: app-name/id
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	fmt.Printf("Syncing application: %s/%s\n", appName, id)

	// Locate deployment directory
	deploymentDir := filepath.Join(cfg.ConfigDir, "applications", appName, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s\nDirectory: %s", deploymentIdentifier, deploymentDir)
	}

	// Check required files exist
	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("values.yaml not found in: %s", deploymentDir)
	}

	// Check kubeconfig exists (cluster must be running)
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
	fmt.Printf("Deployment ID: %s\n", id)
	fmt.Printf("Running helmfile sync...\n\n")

	// Execute helmfile sync
	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync")
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)
	fmt.Printf("\n✓ Application synced successfully!\n")
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("\nTo check status: obol kubectl get all -n %s\n", namespace)
	fmt.Printf("To view logs: obol kubectl logs -n %s <pod-name>\n", namespace)

	return nil
}

// parseDeploymentIdentifier parses "app-name/id" format
func parseDeploymentIdentifier(identifier string) (appName, id string, err error) {
	// Try slash separator
	if strings.Contains(identifier, "/") {
		parts := strings.SplitN(identifier, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("invalid format. Use: <app>/<id>")
		}
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("please use <app>/<id> format (e.g., postgresql/eager-fox)")
}

// List displays installed applications
func List(cfg *config.Config, opts ListOptions) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")

	// Check if applications directory exists
	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		fmt.Println("No applications installed")
		fmt.Println("\nTo install an application:")
		fmt.Println("  obol app install bitnami/redis")
		fmt.Println("  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz")
		fmt.Println("\nFind charts at https://artifacthub.io")
		return nil
	}

	// Walk through applications directory
	apps, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read applications directory: %w", err)
	}

	if len(apps) == 0 {
		fmt.Println("No applications installed")
		return nil
	}

	fmt.Println("Installed applications:")
	fmt.Println()

	count := 0
	for _, appDir := range apps {
		if !appDir.IsDir() {
			continue
		}

		appName := appDir.Name()
		appPath := filepath.Join(appsDir, appName)

		// List deployments for this app
		deployments, err := os.ReadDir(appPath)
		if err != nil {
			continue
		}

		for _, deployment := range deployments {
			if !deployment.IsDir() {
				continue
			}

			id := deployment.Name()
			deploymentPath := filepath.Join(appPath, id)

			// Parse helmfile for chart info
			info, err := ParseHelmfile(deploymentPath)
			if err != nil {
				// Helmfile not found - show basic info
				fmt.Printf("  %s/%s\n", appName, id)
				count++
				continue
			}

			// Show deployment info
			if opts.Verbose {
				fmt.Printf("  %s/%s\n", appName, id)
				fmt.Printf("    Chart: %s\n", info.ChartRef)
				fmt.Printf("    Version: %s\n", info.Version)
				if modTime, err := GetHelmfileModTime(deploymentPath); err == nil {
					fmt.Printf("    Modified: %s\n", modTime)
				}
				fmt.Println()
			} else {
				fmt.Printf("  %s/%s (chart: %s, version: %s)\n",
					appName, id, info.ChartRef, info.Version)
			}
			count++
		}
	}

	fmt.Printf("\nTotal: %d application deployment(s)\n", count)

	return nil
}

// Delete removes an application deployment and its cluster resources
func Delete(cfg *config.Config, deploymentIdentifier string, force bool) error {
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	namespaceName := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := filepath.Join(cfg.ConfigDir, "applications", appName, id)

	fmt.Printf("Deleting application: %s/%s\n", appName, id)
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
		fmt.Printf("  [x] Kubernetes namespace: %s\n", namespaceName)
	} else {
		fmt.Printf("  [ ] Kubernetes namespace: %s (not found)\n", namespaceName)
	}
	if configExists {
		fmt.Printf("  [x] Configuration directory: %s\n", deploymentDir)
	} else {
		fmt.Printf("  [ ] Configuration directory: %s (not found)\n", deploymentDir)
	}

	// Check if there's anything to delete
	if !namespaceExists && !configExists {
		return fmt.Errorf("deployment not found: %s", deploymentIdentifier)
	}

	// Confirm deletion (unless --force)
	if !force {
		fmt.Print("\nProceed with deletion? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	// Delete Kubernetes namespace
	if namespaceExists {
		fmt.Printf("\nDeleting namespace %s...\n", namespaceName)
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespaceName,
			"--force", "--grace-period=0")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		fmt.Println("Namespace deleted")
	}

	// Delete configuration directory
	if configExists {
		fmt.Printf("Deleting configuration directory...\n")
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		fmt.Println("Configuration deleted")

		// Clean up empty parent directories
		appDir := filepath.Join(cfg.ConfigDir, "applications", appName)
		entries, err := os.ReadDir(appDir)
		if err == nil && len(entries) == 0 {
			os.Remove(appDir)
		}
	}

	fmt.Printf("\n✓ Application %s/%s deleted successfully!\n", appName, id)

	return nil
}
