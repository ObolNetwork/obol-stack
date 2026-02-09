package openclaw

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/appkit"
	"github.com/ObolNetwork/obol-stack/internal/config"
)

const (
	appName       = "openclaw"
	defaultDomain = "obol.stack"
)

// Embed the OpenClaw Helm chart from the shared charts directory.
// The chart source lives in internal/embed/charts/openclaw/ and is
// referenced here so the openclaw package owns its own chart lifecycle.
//
//go:embed all:chart
var chartFS embed.FS

// UpOptions contains options for the up command
type UpOptions struct {
	ID          string // Deployment ID (empty = generate petname)
	Force       bool   // Overwrite existing deployment
	Sync        bool   // Also run helmfile sync after install
	Interactive bool   // true = prompt for provider choice; false = silent defaults
	IsDefault   bool   // true = use fixed ID "default", idempotent on re-run
}

// SetupDefault deploys a default OpenClaw instance as part of stack setup.
// It is idempotent: if a "default" deployment already exists, it re-syncs.
func SetupDefault(cfg *config.Config) error {
	return Up(cfg, UpOptions{
		ID:        "default",
		Sync:      true,
		IsDefault: true,
	})
}

// Up creates and optionally deploys an OpenClaw instance
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
			fmt.Println("Default OpenClaw instance already configured, re-syncing...")
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

	// Detect existing ~/.openclaw config
	imported, err := DetectExistingConfig()
	if err != nil {
		fmt.Printf("Warning: failed to read existing config: %v\n", err)
	}
	if imported != nil {
		PrintImportSummary(imported)
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

	// Write Obol Stack overlay values (httpRoute, provider config, eRPC, skills)
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

	fmt.Printf("\n✓ OpenClaw instance configured!\n")
	fmt.Printf("  Deployment: %s/%s\n", appName, id)
	fmt.Printf("  Namespace:  %s\n", namespace)
	fmt.Printf("  Hostname:   %s\n", hostname)
	fmt.Printf("  Location:   %s\n", paths.DeploymentDir)
	fmt.Printf("\nFiles created:\n")
	fmt.Printf("  - chart/            Embedded OpenClaw Helm chart\n")
	fmt.Printf("  - values.yaml       Chart defaults (edit to customize)\n")
	fmt.Printf("  - values-obol.yaml  Obol Stack defaults (httpRoute, providers, eRPC)\n")
	fmt.Printf("  - helmfile.yaml     Deployment configuration\n")

	if opts.Sync {
		fmt.Printf("\nDeploying to cluster...\n\n")
		return doSync(cfg, id)
	}

	fmt.Printf("\nTo deploy: obol openclaw sync %s\n", id)
	return nil
}

// Sync deploys or updates an OpenClaw instance
func Sync(cfg *config.Config, id string) error {
	return doSync(cfg, id)
}

func doSync(cfg *config.Config, id string) error {
	paths := appkit.ResolveDeployment(cfg, appName, id)
	if _, err := os.Stat(paths.DeploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nDirectory: %s", appName, id, paths.DeploymentDir)
	}

	fmt.Printf("Syncing OpenClaw: %s/%s\n", appName, id)
	fmt.Printf("Deployment directory: %s\n", paths.DeploymentDir)
	fmt.Printf("Running helmfile sync...\n\n")

	if err := appkit.SyncHelmfile(cfg, paths.DeploymentDir); err != nil {
		return err
	}

	namespace := appkit.Namespace(appName, id)
	hostname := appkit.Hostname(appName, id, defaultDomain)
	fmt.Printf("\n✓ OpenClaw synced successfully!\n")
	fmt.Printf("  Namespace: %s\n", namespace)
	fmt.Printf("  URL:       http://%s\n", hostname)
	fmt.Printf("\nRetrieve gateway token:\n")
	fmt.Printf("  obol openclaw token %s\n", id)
	fmt.Printf("\nPort-forward fallback:\n")
	fmt.Printf("  obol kubectl -n %s port-forward svc/openclaw 18789:18789\n", namespace)

	return nil
}

// Token retrieves the gateway token for an OpenClaw instance from the cluster
func Token(cfg *config.Config, id string) error {
	namespace := appkit.Namespace(appName, id)
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s", appName)

	token, err := appkit.GetSecretValue(cfg, namespace, labelSelector, "OPENCLAW_GATEWAY_TOKEN")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", token)
	return nil
}

// List displays installed OpenClaw instances
func List(cfg *config.Config) error {
	return appkit.ListDeployments(cfg, appName, defaultDomain)
}

// Delete removes an OpenClaw instance
func Delete(cfg *config.Config, id string, force bool) error {
	return appkit.DeleteDeployment(cfg, appName, id, force)
}

// SkillsSync packages a local skills directory into a ConfigMap and rolls the deployment
func SkillsSync(cfg *config.Config, id, skillsDir string) error {
	namespace := appkit.Namespace(appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return fmt.Errorf("skills directory not found: %s", skillsDir)
	}

	configMapName := fmt.Sprintf("openclaw-%s-skills", id)
	archiveKey := "skills.tgz"

	fmt.Printf("Packaging skills from %s...\n", skillsDir)

	var archiveBuf bytes.Buffer
	tarCmd := exec.Command("tar", "-czf", "-", "-C", skillsDir, ".")
	tarCmd.Stdout = &archiveBuf
	var tarStderr bytes.Buffer
	tarCmd.Stderr = &tarStderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to create skills archive: %w\n%s", err, tarStderr.String())
	}

	tmpFile, err := os.CreateTemp("", "openclaw-skills-*.tgz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(archiveBuf.Bytes()); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write archive: %w", err)
	}
	tmpFile.Close()

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	delCmd := exec.Command(kubectlBinary, "delete", "configmap", configMapName,
		"-n", namespace, "--ignore-not-found")
	delCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	delCmd.Run()

	fmt.Printf("Creating ConfigMap %s in namespace %s...\n", configMapName, namespace)
	createCmd := exec.Command(kubectlBinary, "create", "configmap", configMapName,
		"-n", namespace,
		fmt.Sprintf("--from-file=%s=%s", archiveKey, tmpFile.Name()))
	createCmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var createStderr bytes.Buffer
	createCmd.Stderr = &createStderr
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w\n%s", err, createStderr.String())
	}

	fmt.Printf("✓ Skills ConfigMap updated: %s\n", configMapName)
	fmt.Printf("\nTo apply, re-sync: obol openclaw sync %s\n", id)
	return nil
}

// generateOverlayValues creates the Obol Stack-specific values overlay.
// If imported is non-nil, provider/channel config from the import is used
// instead of the default Ollama configuration.
func generateOverlayValues(hostname string, imported *ImportResult) string {
	var b strings.Builder

	b.WriteString(`# Obol Stack overlay values for OpenClaw
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

# SA needs API token mount for K8s read access
serviceAccount:
  automount: true

# Read-only RBAC for K8s API (pods, services, deployments, etc.)
rbac:
  create: true

`)

	// Provider and agent model configuration
	importedOverlay := TranslateToOverlayYAML(imported)
	if importedOverlay != "" {
		b.WriteString("# Imported from ~/.openclaw/openclaw.json\n")
		b.WriteString(importedOverlay)
	} else {
		b.WriteString(`# Route agent traffic to in-cluster Ollama
openclaw:
  agentModel: ollama/glm-4.7-flash

# Default model provider: in-cluster Ollama
models:
  ollama:
    enabled: true
    baseUrl: http://llmspy.llm.svc.cluster.local:8000/v1
    api: openai-completions
    apiKeyEnvVar: OLLAMA_API_KEY
    apiKeyValue: ollama-local
    models:
      - id: glm-4.7-flash
        name: GLM-4.7 Flash

`)
	}

	b.WriteString(`# eRPC integration
erpc:
  url: http://erpc.erpc.svc.cluster.local:4000/rpc

# Skills: chart creates a default empty ConfigMap; populate with obol openclaw skills sync
skills:
  enabled: true
  createDefault: true

# Agent init Job (enable to bootstrap workspace on first deploy)
initJob:
  enabled: false
`)

	return b.String()
}

// interactiveSetup prompts the user for provider configuration.
// If imported is non-nil, offers to use the detected config.
func interactiveSetup(imported *ImportResult) (*ImportResult, error) {
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
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')
	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	switch choice {
	case "1":
		// Ollama defaults — return nil so generateOverlayValues uses built-in defaults
		fmt.Println("Using Ollama (in-cluster) as default provider.")
		return nil, nil
	case "2":
		return promptForProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "", "gpt-4o", "GPT-4o")
	case "3":
		return promptForProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com/v1", "anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5")
	default:
		fmt.Printf("Unknown choice '%s', using Ollama defaults.\n", choice)
		return nil, nil
	}
}

// promptForProvider asks for an API key and builds an ImportResult for a single provider
func promptForProvider(reader *bufio.Reader, name, display, baseURL, api, modelID, modelName string) (*ImportResult, error) {
	fmt.Printf("\n%s API key: ", display)
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	agentModel := fmt.Sprintf("%s/%s", name, modelID)

	return &ImportResult{
		AgentModel: agentModel,
		Providers: []ImportedProvider{
			{
				Name:    name,
				BaseURL: baseURL,
				API:     api,
				APIKey:  apiKey,
				Models: []ImportedModel{
					{ID: modelID, Name: modelName},
				},
			},
		},
	}, nil
}
