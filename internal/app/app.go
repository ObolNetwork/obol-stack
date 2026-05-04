package app

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
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	petname "github.com/dustinkirkland/golang-petname"
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
func Install(cfg *config.Config, u *ui.UI, chartRef string, opts InstallOptions) error {
	u.Infof("Installing application from: %s", chartRef)

	// 1. Parse chart reference
	chart, err := ParseChartReference(chartRef)
	if err != nil {
		return err
	}

	// 2. If repo/chart format, resolve via ArtifactHub
	if chart.NeedsResolution() {
		u.Info("Resolving chart via ArtifactHub...")

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

		u.Detail("Resolved", fmt.Sprintf("%s/%s version %s", info.RepoName, info.ChartName, info.Version))
		u.Detail("Repository URL", info.RepoURL)
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

	u.Detail("Application name", appName)

	// 4. Generate or use provided ID
	id := opts.ID
	if id == "" {
		id = petname.Generate(2, "-")
		u.Detail("Generated deployment ID", id)
	} else {
		u.Detail("Using deployment ID", id)
	}

	// 5. Check if deployment exists
	deploymentDir := filepath.Join(cfg.ConfigDir, "applications", appName, id)
	if _, err := os.Stat(deploymentDir); err == nil {
		if !opts.Force {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, deploymentDir)
		}

		u.Warnf("Overwriting existing deployment at %s", deploymentDir)
	}

	// 6. Create deployment directory
	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// 7. Fetch default values using helm show values
	u.Info("Fetching chart default values...")

	values, err := fetchChartValues(cfg, chart)
	if err != nil {
		// Clean up on failure
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to fetch chart values: %w", err)
	}

	// 8. Write values.yaml
	valuesPath := filepath.Join(deploymentDir, "values.yaml")
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// 9. Generate helmfile.yaml (references chart remotely)
	if err := generateRemoteHelmfile(deploymentDir, chart, appName, id); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to generate helmfile: %w", err)
	}

	// 10. Print success message
	u.Blank()
	u.Successf("Application installed successfully!")
	u.Detail("Deployment", fmt.Sprintf("%s/%s", appName, id))
	u.Detail("Location", deploymentDir)
	u.Blank()
	u.Print("Files created:")
	u.Print("  - helmfile.yaml: Deployment configuration")
	u.Print("  - values.yaml: Chart default values (edit to customize)")
	u.Blank()
	u.Print("Edit values.yaml to customize your deployment.")
	u.Printf("To deploy, run: obol app sync %s/%s", appName, id)

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

	data := map[string]any{
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

	return os.WriteFile(filepath.Join(dir, "helmfile.yaml"), buf.Bytes(), 0o600)
}

// Sync deploys or updates an application to the cluster
func Sync(cfg *config.Config, u *ui.UI, deploymentIdentifier string) error {
	// Parse deployment identifier: app-name/id
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	u.Infof("Syncing application: %s/%s", appName, id)

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
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	// Get helmfile binary path
	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	u.Detail("Deployment directory", deploymentDir)
	u.Detail("Deployment ID", id)

	// Execute helmfile sync
	syncArgs := append([]string{"-f", helmfilePath, "sync"}, helmcmd.SyncFlagsForVersion(filepath.Join(cfg.BinDir, "helm"))...)
	cmd := exec.Command(helmfileBinary, syncArgs...)
	cmd.Dir = deploymentDir

	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
	)

	if err := u.Exec(ui.ExecConfig{Name: "Running helmfile sync", Cmd: cmd}); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)

	u.Blank()
	u.Success("Application synced successfully!")
	u.Detail("Namespace", namespace)
	u.Blank()
	u.Printf("To check status: obol kubectl get all -n %s", namespace)
	u.Printf("To view logs: obol kubectl logs -n %s <pod-name>", namespace)

	return nil
}

// parseDeploymentIdentifier parses "app-name/id" format
func parseDeploymentIdentifier(identifier string) (appName, id string, err error) {
	// Try slash separator
	if strings.Contains(identifier, "/") {
		parts := strings.SplitN(identifier, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", errors.New("invalid format. Use: <app>/<id>")
		}

		return parts[0], parts[1], nil
	}

	return "", "", errors.New("please use <app>/<id> format (e.g., postgresql/eager-fox)")
}

// List displays installed applications
func List(cfg *config.Config, u *ui.UI, opts ListOptions) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")

	// Check if applications directory exists
	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		u.Print("No applications installed")
		u.Blank()
		u.Print("To install an application:")
		u.Print("  obol app install bitnami/redis")
		u.Print("  obol app install https://charts.bitnami.com/bitnami/redis-19.0.0.tgz")
		u.Blank()
		u.Print("Find charts at https://artifacthub.io")

		return nil
	}

	// Walk through applications directory
	apps, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read applications directory: %w", err)
	}

	if len(apps) == 0 {
		u.Print("No applications installed")
		return nil
	}

	u.Bold("Installed applications:")
	u.Blank()

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
				u.Printf("  %s/%s", appName, id)

				count++

				continue
			}

			// Show deployment info
			if opts.Verbose {
				u.Printf("  %s/%s", appName, id)
				u.Detail("    Chart", info.ChartRef)
				u.Detail("    Version", info.Version)

				if modTime, err := GetHelmfileModTime(deploymentPath); err == nil {
					u.Detail("    Modified", modTime)
				}

				u.Blank()
			} else {
				u.Printf("  %s/%s (chart: %s, version: %s)",
					appName, id, info.ChartRef, info.Version)
			}

			count++
		}
	}

	u.Blank()
	u.Printf("Total: %d application deployment(s)", count)

	return nil
}

// Delete removes an application deployment and its cluster resources
func Delete(cfg *config.Config, u *ui.UI, deploymentIdentifier string, force bool) error {
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	namespaceName := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := filepath.Join(cfg.ConfigDir, "applications", appName, id)

	u.Infof("Deleting application: %s/%s", appName, id)
	u.Detail("Namespace", namespaceName)
	u.Detail("Config directory", deploymentDir)

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

	// Display what will be deleted
	u.Blank()
	u.Print("Resources to be deleted:")

	if namespaceExists {
		u.Printf("  [x] Kubernetes namespace: %s", namespaceName)
	} else {
		u.Printf("  [ ] Kubernetes namespace: %s (not found)", namespaceName)
	}

	if configExists {
		u.Printf("  [x] Configuration directory: %s", deploymentDir)
	} else {
		u.Printf("  [ ] Configuration directory: %s (not found)", deploymentDir)
	}

	// Check if there's anything to delete
	if !namespaceExists && !configExists {
		return fmt.Errorf("deployment not found: %s", deploymentIdentifier)
	}

	// Confirm deletion (unless --force)
	if !force {
		u.Blank()

		if !u.Confirm("Proceed with deletion?", false) {
			u.Print("Deletion cancelled")
			return nil
		}
	}

	// Delete Kubernetes namespace
	if namespaceExists {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespaceName,
			"--force", "--grace-period=0")

		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

		if err := u.Exec(ui.ExecConfig{Name: "Deleting namespace " + namespaceName, Cmd: cmd}); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
	}

	// Delete configuration directory
	if configExists {
		u.Info("Deleting configuration directory...")

		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}

		u.Success("Configuration deleted")

		// Clean up empty parent directories
		appDir := filepath.Join(cfg.ConfigDir, "applications", appName)

		entries, err := os.ReadDir(appDir)
		if err == nil && len(entries) == 0 {
			os.Remove(appDir)
		}
	}

	u.Blank()
	u.Successf("Application %s/%s deleted successfully!", appName, id)

	return nil
}
