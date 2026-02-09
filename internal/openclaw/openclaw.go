package openclaw

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/dustinkirkland/golang-petname"
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
		id = petname.Generate(2, "-")
		fmt.Printf("Generated deployment ID: %s\n", id)
	} else {
		fmt.Printf("Using deployment ID: %s\n", id)
	}

	deploymentDir := deploymentPath(cfg, id)

	// Idempotent re-run for default deployment: just re-sync
	if opts.IsDefault && !opts.Force {
		if _, err := os.Stat(deploymentDir); err == nil {
			fmt.Println("Default OpenClaw instance already configured, re-syncing...")
			if opts.Sync {
				return doSync(cfg, id)
			}
			return nil
		}
	}

	if _, err := os.Stat(deploymentDir); err == nil {
		if !opts.Force && !opts.IsDefault {
			return fmt.Errorf("deployment already exists: %s/%s\n"+
				"Directory: %s\n"+
				"Use --force or -f to overwrite", appName, id, deploymentDir)
		}
		fmt.Printf("WARNING: Overwriting existing deployment at %s\n", deploymentDir)
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

	if err := os.MkdirAll(deploymentDir, 0755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Copy embedded chart to deployment/chart/
	chartDir := filepath.Join(deploymentDir, "chart")
	if err := copyEmbeddedChart(chartDir); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to copy chart: %w", err)
	}

	// Write values.yaml from the embedded chart defaults
	defaultValues, err := chartFS.ReadFile("chart/values.yaml")
	if err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to read chart defaults: %w", err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, "values.yaml"), defaultValues, 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write values.yaml: %w", err)
	}

	// Write Obol Stack overlay values (httpRoute, provider config, eRPC, skills)
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)
	overlay := generateOverlayValues(hostname, imported)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	// Generate helmfile.yaml referencing local chart
	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0644); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	fmt.Printf("\n✓ OpenClaw instance configured!\n")
	fmt.Printf("  Deployment: %s/%s\n", appName, id)
	fmt.Printf("  Namespace:  %s\n", namespace)
	fmt.Printf("  Hostname:   %s\n", hostname)
	fmt.Printf("  Location:   %s\n", deploymentDir)
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
	deploymentDir := deploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nDirectory: %s", appName, id, deploymentDir)
	}

	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	fmt.Printf("Syncing OpenClaw: %s/%s\n", appName, id)
	fmt.Printf("Deployment directory: %s\n", deploymentDir)
	fmt.Printf("Running helmfile sync...\n\n")

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
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
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
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlBinary, "get", "secret", "-n", namespace,
		"-l", fmt.Sprintf("app.kubernetes.io/name=%s", appName),
		"-o", "json")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get secret: %w\n%s", err, stderr.String())
	}

	var secretList struct {
		Items []struct {
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &secretList); err != nil {
		return fmt.Errorf("failed to parse secret: %w", err)
	}

	if len(secretList.Items) == 0 {
		return fmt.Errorf("no secrets found in namespace %s. Is OpenClaw deployed?", namespace)
	}

	for _, item := range secretList.Items {
		if encoded, ok := item.Data["OPENCLAW_GATEWAY_TOKEN"]; ok {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return fmt.Errorf("failed to decode token: %w", err)
			}
			fmt.Printf("%s\n", string(decoded))
			return nil
		}
	}

	return fmt.Errorf("OPENCLAW_GATEWAY_TOKEN not found in namespace %s secrets", namespace)
}

// List displays installed OpenClaw instances
func List(cfg *config.Config) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		fmt.Println("No OpenClaw instances installed")
		fmt.Println("\nTo create one: obol openclaw up")
		return nil
	}

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No OpenClaw instances installed")
		return nil
	}

	fmt.Println("OpenClaw instances:")
	fmt.Println()

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		namespace := fmt.Sprintf("%s-%s", appName, id)
		hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
		fmt.Printf("  %s\n", id)
		fmt.Printf("    Namespace: %s\n", namespace)
		fmt.Printf("    URL:       http://%s\n", hostname)
		fmt.Println()
		count++
	}

	fmt.Printf("Total: %d instance(s)\n", count)
	return nil
}

// Delete removes an OpenClaw instance
func Delete(cfg *config.Config, id string, force bool) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := deploymentPath(cfg, id)

	fmt.Printf("Deleting OpenClaw: %s/%s\n", appName, id)
	fmt.Printf("Namespace: %s\n", namespace)

	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	namespaceExists := false
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespace)
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("instance not found: %s", id)
	}

	fmt.Println("\nResources to be deleted:")
	if namespaceExists {
		fmt.Printf("  [x] Kubernetes namespace: %s\n", namespace)
	} else {
		fmt.Printf("  [ ] Kubernetes namespace: %s (not found)\n", namespace)
	}
	if configExists {
		fmt.Printf("  [x] Configuration: %s\n", deploymentDir)
	}

	if !force {
		fmt.Print("\nProceed with deletion? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	if namespaceExists {
		fmt.Printf("\nDeleting namespace %s...\n", namespace)
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespace,
			"--force", "--grace-period=0")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		fmt.Println("Namespace deleted")
	}

	if configExists {
		fmt.Printf("Deleting configuration...\n")
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		fmt.Println("Configuration deleted")

		parentDir := filepath.Join(cfg.ConfigDir, "applications", appName)
		entries, err := os.ReadDir(parentDir)
		if err == nil && len(entries) == 0 {
			os.Remove(parentDir)
		}
	}

	fmt.Printf("\n✓ OpenClaw %s deleted successfully!\n", id)
	return nil
}

// SkillsSync packages a local skills directory into a ConfigMap and rolls the deployment
func SkillsSync(cfg *config.Config, id, skillsDir string) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)

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

// deploymentPath returns the path to a deployment directory
func deploymentPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", appName, id)
}

// copyEmbeddedChart extracts the embedded chart FS to destDir
func copyEmbeddedChart(destDir string) error {
	return fs.WalkDir(chartFS, "chart", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "chart" {
			return nil
		}

		relPath := strings.TrimPrefix(path, "chart/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		data, err := chartFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded %s: %w", path, err)
		}
		return os.WriteFile(destPath, data, 0644)
	})
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
    baseUrl: http://ollama.llm.svc.cluster.local:11434/v1
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

// generateHelmfile creates a helmfile.yaml referencing the local chart
func generateHelmfile(id, namespace string) string {
	return fmt.Sprintf(`# OpenClaw instance: %s
# Managed by obol openclaw

releases:
  - name: openclaw
    namespace: %s
    createNamespace: true
    chart: ./chart
    values:
      - values.yaml
      - values-obol.yaml
`, id, namespace)
}
