package nanobot

import (
	"bufio"
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/appkit"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/providers"
)

const (
	appName       = "nanobot"
	defaultDomain = "obol.stack"
)

//go:embed all:chart
var chartFS embed.FS

// UpOptions contains options for the up command
type UpOptions struct {
	ID          string
	Force       bool
	Sync        bool
	Interactive bool
	IsDefault   bool
}

// SetupDefault deploys a default Nanobot instance as part of stack setup.
// It is idempotent: if a "default" deployment already exists, it re-syncs.
func SetupDefault(cfg *config.Config) error {
	return Up(cfg, UpOptions{
		ID:        "default",
		Sync:      true,
		IsDefault: true,
	})
}

// Up creates and optionally deploys a Nanobot instance
func Up(cfg *config.Config, opts UpOptions) error {
	id := opts.ID
	if opts.IsDefault {
		id = "default"
	}
	if id == "" {
		id = appkit.GenerateID("")
		fmt.Printf("Generated deployment ID: %s\n", id)
	} else {
		fmt.Printf("Using deployment ID: %s\n", id)
	}

	paths := appkit.ResolveDeployment(cfg, appName, id)

	// Idempotent re-run for default deployment: just re-sync
	if opts.IsDefault && !opts.Force {
		if _, err := os.Stat(paths.DeploymentDir); err == nil {
			fmt.Println("Default Nanobot instance already configured, re-syncing...")
			if opts.Sync {
				return doSync(cfg, id)
			}
			return nil
		}
	}

	if _, err := os.Stat(paths.DeploymentDir); err == nil {
		if !opts.Force && !opts.IsDefault {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, paths.DeploymentDir)
		}
		fmt.Printf("WARNING: Overwriting existing deployment at %s\n", paths.DeploymentDir)
	}

	// Detect existing config from all sources
	imported, err := providers.DetectAll()
	if err != nil {
		fmt.Printf("Warning: failed to read existing config: %v\n", err)
	}
	if imported != nil {
		providers.PrintSummary("~/.openclaw, ~/.nanobot, env", imported)
	}

	// Interactive setup: prompt for provider choice
	if opts.Interactive {
		imported, err = interactiveSetup(imported)
		if err != nil {
			return fmt.Errorf("interactive setup failed: %w", err)
		}
	}

	if err := os.MkdirAll(paths.DeploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Copy embedded chart to deployment/chart/
	if err := appkit.CopyEmbeddedChart(chartFS, "chart", paths.ChartDir); err != nil {
		os.RemoveAll(paths.DeploymentDir)
		return fmt.Errorf("failed to copy chart: %w", err)
	}

	// Write values.yaml from the embedded chart defaults
	if err := appkit.WriteDefaultValues(chartFS, "chart/values.yaml", paths.ValuesPath); err != nil {
		os.RemoveAll(paths.DeploymentDir)
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Write Obol Stack overlay values
	hostname := appkit.Hostname(appName, id, defaultDomain)
	namespace := appkit.Namespace(appName, id)
	overlay := generateOverlayValues(hostname, imported)
	if err := appkit.WriteFile(paths.OverlayPath, overlay); err != nil {
		os.RemoveAll(paths.DeploymentDir)
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	// Generate helmfile.yaml referencing local chart
	helmfileContent := appkit.GenerateHelmfile(appName, namespace, id)
	if err := appkit.WriteFile(paths.HelmfilePath, helmfileContent); err != nil {
		os.RemoveAll(paths.DeploymentDir)
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	fmt.Printf("\n✓ Nanobot instance configured!\n")
	fmt.Printf("  Deployment: %s/%s\n", appName, id)
	fmt.Printf("  Namespace:  %s\n", namespace)
	fmt.Printf("  Hostname:   %s\n", hostname)
	fmt.Printf("  Location:   %s\n", paths.DeploymentDir)
	fmt.Printf("\nFiles created:\n")
	fmt.Printf("  - chart/            Embedded Nanobot Helm chart\n")
	fmt.Printf("  - values.yaml       Chart defaults (edit to customize)\n")
	fmt.Printf("  - values-obol.yaml  Obol Stack defaults (httpRoute, providers)\n")
	fmt.Printf("  - helmfile.yaml     Deployment configuration\n")

	if opts.Sync {
		fmt.Printf("\nDeploying to cluster...\n\n")
		return doSync(cfg, id)
	}

	fmt.Printf("\nTo deploy: obol nanobot sync %s\n", id)
	return nil
}

// Sync deploys or updates a Nanobot instance
func Sync(cfg *config.Config, id string) error {
	return doSync(cfg, id)
}

func doSync(cfg *config.Config, id string) error {
	paths := appkit.ResolveDeployment(cfg, appName, id)
	if _, err := os.Stat(paths.DeploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nDirectory: %s", appName, id, paths.DeploymentDir)
	}

	fmt.Printf("Syncing Nanobot: %s/%s\n", appName, id)
	fmt.Printf("Deployment directory: %s\n", paths.DeploymentDir)
	fmt.Printf("Running helmfile sync...\n\n")

	if err := appkit.SyncHelmfile(cfg, paths.DeploymentDir); err != nil {
		return err
	}

	namespace := appkit.Namespace(appName, id)
	hostname := appkit.Hostname(appName, id, defaultDomain)
	fmt.Printf("\n✓ Nanobot synced successfully!\n")
	fmt.Printf("  Namespace: %s\n", namespace)
	fmt.Printf("  URL:       http://%s\n", hostname)
	fmt.Printf("\nRetrieve gateway token:\n")
	fmt.Printf("  obol nanobot token %s\n", id)
	fmt.Printf("\nPort-forward fallback:\n")
	fmt.Printf("  obol kubectl -n %s port-forward svc/nanobot 18790:18790\n", namespace)

	return nil
}

// Token retrieves the gateway token for a Nanobot instance from the cluster
func Token(cfg *config.Config, id string) error {
	namespace := appkit.Namespace(appName, id)
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s", appName)

	token, err := appkit.GetSecretValue(cfg, namespace, labelSelector, "NANOBOT_GATEWAY_TOKEN")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", token)
	return nil
}

// List displays installed Nanobot instances
func List(cfg *config.Config) error {
	return appkit.ListDeployments(cfg, appName, defaultDomain)
}

// Delete removes a Nanobot instance
func Delete(cfg *config.Config, id string, force bool) error {
	return appkit.DeleteDeployment(cfg, appName, id, force)
}

// generateOverlayValues creates the Obol Stack-specific values overlay for Nanobot.
func generateOverlayValues(hostname string, imported *providers.DetectedConfig) string {
	var b strings.Builder

	b.WriteString(`# Obol Stack overlay values for Nanobot
# This file contains stack-specific defaults. Edit to customize.

# Enable Gateway API HTTPRoute for stack routing
httpRoute:
  enabled: true
  hostnames:
`)
	b.WriteString(fmt.Sprintf("    - %s\n", hostname))
	b.WriteString(`  parentRefs:
    - name: traefik-gateway
      namespace: traefik
      sectionName: web

`)

	// Provider and agent model configuration
	importedOverlay := TranslateToOverlayYAML(imported)
	if importedOverlay != "" {
		b.WriteString("# Imported from detected configuration\n")
		b.WriteString(importedOverlay)
	} else {
		b.WriteString(`# Default model provider: in-cluster Ollama
providers:
  ollama:
    enabled: true
    baseUrl: http://ollama.llm.svc.cluster.local:11434/v1
    apiKeyValue: ollama-local

`)
	}

	return b.String()
}

// interactiveSetup prompts the user for provider configuration.
func interactiveSetup(imported *providers.DetectedConfig) (*providers.DetectedConfig, error) {
	reader := bufio.NewReader(os.Stdin)

	if imported != nil {
		fmt.Print("\nUse detected configuration? [Y/n]: ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" || line == "y" || line == "yes" {
			fmt.Println("Using detected configuration.")
			return imported, nil
		}
	}

	fmt.Println("\nSelect a model provider:")
	fmt.Println("  [1] Ollama (default, runs in-cluster)")
	fmt.Println("  [2] OpenAI")
	fmt.Println("  [3] Anthropic")
	fmt.Println("  [4] OpenRouter")
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	switch choice {
	case "1":
		fmt.Println("Using Ollama (in-cluster) as default provider.")
		return nil, nil
	case "2":
		return promptForProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1")
	case "3":
		return promptForProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com/v1")
	case "4":
		return promptForProvider(reader, "openrouter", "OpenRouter", "https://openrouter.ai/api/v1")
	default:
		fmt.Printf("Unknown choice '%s', using Ollama defaults.\n", choice)
		return nil, nil
	}
}

func promptForProvider(reader *bufio.Reader, name, display, baseURL string) (*providers.DetectedConfig, error) {
	fmt.Printf("\n%s API key: ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	return &providers.DetectedConfig{
		Providers: []providers.ProviderConfig{
			{
				Name:    name,
				BaseURL: baseURL,
				APIKey:  apiKey,
			},
		},
	}, nil
}
