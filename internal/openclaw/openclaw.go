package openclaw

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/dns"
	obolembed "github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	petname "github.com/dustinkirkland/golang-petname"
)

// CloudProviderInfo holds the cloud provider selection from interactive setup.
// This is used to configure LiteLLM with the API key separately from the
// OpenClaw overlay (which routes through LiteLLM).
type CloudProviderInfo struct {
	Name    string // "anthropic" or "openai"
	APIKey  string
	ModelID string // e.g. "claude-sonnet-4-5-20250929"
	Display string // e.g. "Claude Sonnet 4.5"
}

const (
	appName                 = "openclaw"
	defaultDomain           = "obol.stack"
	userSecretsFileName     = "values-obol.secrets.json"
	userSecretsK8sSecretRef = "openclaw-user-secrets"
	// chartVersion pins the openclaw Helm chart version from the obol repo.
	// renovate: datasource=helm depName=openclaw registryUrl=https://obolnetwork.github.io/helm-charts/
	chartVersion = "0.1.7"

	// remoteSignerChartVersion pins the remote-signer Helm chart version.
	// renovate: datasource=helm depName=remote-signer registryUrl=https://obolnetwork.github.io/helm-charts/
	remoteSignerChartVersion = "0.3.0"
)

// openclawVersionRaw is the single source of truth for the upstream OpenClaw
// version. It is read by CI (docker-publish-openclaw.yml), obolup.sh, and
// the Go binary at compile time via go:embed.
//
//go:embed OPENCLAW_VERSION
var openclawVersionRaw string

// openclawImageTag returns the image tag derived from OPENCLAW_VERSION,
// stripping the leading "v" and any whitespace/comments.
func openclawImageTag() string {
	for line := range strings.SplitSeq(openclawVersionRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		return strings.TrimPrefix(line, "v")
	}

	return ""
}

// ImageRef returns the pinned OpenClaw image reference used by the local
// binary when it writes overlay values. Empty string means no explicit ref.
func ImageRef() string {
	if tag := openclawImageTag(); tag != "" {
		return "ghcr.io/obolnetwork/openclaw:" + tag
	}
	return ""
}

// OnboardOptions contains options for the onboard command
type OnboardOptions struct {
	ID           string   // Deployment ID (empty = generate petname)
	Force        bool     // Overwrite existing deployment
	Sync         bool     // Also run helmfile sync after install
	Interactive  bool     // true = prompt for provider choice; false = silent defaults
	IsDefault    bool     // true = use fixed ID "default", idempotent on re-run
	AgentMode    bool     // true = obol-agent singleton with heartbeat config
	OllamaModels []string // Available Ollama models detected on host (nil = not queried)
}

// SetupDefault deploys the default OpenClaw instance as part of stack setup.
// This is the single canonical instance that handles both user-facing inference
// and agent-mode monetize/heartbeat reconciliation.
// It is idempotent: if a "default" deployment already exists, it re-syncs.
// When Ollama is not detected on the host and no existing ~/.openclaw config
// is found, it skips provider setup gracefully so the user can configure
// later with `obol openclaw setup`.
func SetupDefault(cfg *config.Config, u *ui.UI) error {
	// Check whether the default deployment already exists (re-sync path).
	// If it does, proceed unconditionally — the overlay was already written.
	deploymentDir := DeploymentPath(cfg, "obol-agent")
	if _, err := os.Stat(deploymentDir); err == nil {
		// Existing deployment — always re-sync regardless of Ollama status.
		return Onboard(cfg, OnboardOptions{
			ID:        "obol-agent",
			Sync:      true,
			IsDefault: true,
			AgentMode: true,
		}, u)
	}

	// Check if there is an existing ~/.openclaw config with providers
	imported, importErr := DetectExistingConfig()
	if importErr != nil {
		u.Warnf("could not read existing config: %v", importErr)
	}

	hasImportedProviders := imported != nil && len(imported.Providers) > 0

	// No imported providers — skip automatic deployment.
	// Local Ollama models are often too small to be useful, and the LiteLLM
	// routing path has sharp edges that are better handled via explicit setup.
	var ollamaModels []string
	if !hasImportedProviders {
		ollamaModels = listOllamaModels()
		if ollamaModels != nil {
			if len(ollamaModels) > 0 {
				u.Successf("Local Ollama detected with %d model(s) at %s", len(ollamaModels), ollamaEndpoint())
			} else {
				u.Successf("Local Ollama detected at %s (no models pulled)", ollamaEndpoint())
				u.Print("  Run 'obol model setup' to configure a cloud provider,")
				u.Print("  or pull a model with: ollama pull qwen3.5:4b")
			}
		} else {
			u.Warnf("Local Ollama not detected on host (%s)", ollamaEndpoint())
			u.Print("  Skipping default OpenClaw model provider setup.")
			u.Print("  Run 'obol model setup' to configure a provider later.")

			return nil
		}
	}

	return Onboard(cfg, OnboardOptions{
		ID:           "obol-agent",
		Sync:         true,
		IsDefault:    true,
		AgentMode:    true,
		OllamaModels: ollamaModels,
	}, u)
}

// Onboard creates and optionally deploys an OpenClaw instance
func Onboard(cfg *config.Config, opts OnboardOptions, u *ui.UI) error {
	id := opts.ID
	if opts.IsDefault {
		id = "obol-agent"
	}

	if id == "" {
		id = petname.Generate(2, "-")
		u.Infof("Generated deployment ID: %s", id)
	} else {
		u.Infof("Using deployment ID: %s", id)
	}

	deploymentDir := DeploymentPath(cfg, id)

	// Idempotent re-run for default deployment: just re-sync
	if opts.IsDefault && !opts.Force {
		if _, err := os.Stat(deploymentDir); err == nil {
			u.Info("Default OpenClaw instance already configured, re-syncing...")
			// Always regenerate helmfile.yaml to pick up chart version bumps.
			// values-obol.yaml (user config) is intentionally left unchanged.
			namespace := fmt.Sprintf("%s-%s", appName, id)

			helmfileContent := generateHelmfile(id, namespace)
			if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0o600); err != nil {
				return fmt.Errorf("failed to update helmfile.yaml: %w", err)
			}

			if opts.Sync {
				if err := doSync(cfg, id, u); err != nil {
					return err
				}
				// Import workspace on re-sync too
				imported, importErr := DetectExistingConfig()
				if importErr != nil {
					u.Warnf("could not read existing config: %v", importErr)
				}

				if imported != nil && imported.WorkspaceDir != "" {
					copyWorkspaceToVolume(cfg, id, imported.WorkspaceDir, u)
				}

				return nil
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

		u.Warnf("Overwriting existing deployment at %s", deploymentDir)
	}

	// Detect existing ~/.openclaw config
	imported, err := DetectExistingConfig()
	if err != nil {
		u.Warnf("failed to read existing config: %v", err)
	}

	if imported != nil {
		PrintImportSummary(imported)
	}

	// Interactive setup: auto-skip prompts when existing config has providers
	if opts.Interactive {
		if imported != nil && len(imported.Providers) > 0 {
			u.Print("\nUsing detected configuration from ~/.openclaw/")
		} else {
			var cloudProvider *CloudProviderInfo

			imported, cloudProvider, err = interactiveSetup(cfg, imported)
			if err != nil {
				return fmt.Errorf("interactive setup failed: %w", err)
			}
			// Push cloud API key to LiteLLM if a cloud provider was selected
			if cloudProvider != nil {
				if llmErr := model.ConfigureLiteLLM(cfg, u, cloudProvider.Name, cloudProvider.APIKey, []string{cloudProvider.ModelID}); llmErr != nil {
					return fmt.Errorf("failed to configure LiteLLM: %w", llmErr)
				}
			}
		}
	}

	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	// Write Obol Stack overlay values (httpRoute, provider config, eRPC, skills)
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
	namespace := fmt.Sprintf("%s-%s", appName, id)

	// Ensure /etc/hosts has an entry for this subdomain.
	// macOS Sequoia's /etc/resolver/ doesn't reliably forward subdomain queries.
	if err := dns.EnsureHostsEntries(collectAllHostnames(cfg, hostname)); err != nil {
		u.Warnf("Could not update /etc/hosts for %s: %v", hostname, err)
	}

	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write OpenClaw secrets metadata: %w", err)
	}
	// If running in agent mode, read tunnel state to inject AGENT_BASE_URL.
	var agentBaseURL string

	if opts.AgentMode {
		st, _ := tunnel.LoadTunnelState(cfg)
		if st != nil && st.Hostname != "" {
			agentBaseURL = "https://" + st.Hostname
		}
		// Agent mode always needs Ollama models for local inference,
		// even when an imported config provides cloud providers.
		if opts.OllamaModels == nil {
			opts.OllamaModels = listOllamaModels()
		}
	}

	overlay := generateOverlayValues(cfg, hostname, imported, len(secretData) > 0, opts.OllamaModels, agentBaseURL)

	// Append heartbeat config for agent mode.
	if opts.AgentMode {
		overlay += `
# Agent mode: periodic heartbeat for monetize reconciliation
agents:
  defaults:
    heartbeat:
      every: "5m"
      target: "none"
`
	}

	if err := os.WriteFile(filepath.Join(deploymentDir, "values-obol.yaml"), []byte(overlay), 0o600); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	// Generate Ethereum signing wallet (key + remote-signer config).
	u.Blank()
	u.Info("Generating Ethereum wallet...")
	wallet, err := GenerateWallet(cfg, id, u)
	if err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to generate wallet: %w", err)
	}

	rsValues := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-remote-signer.yaml"), []byte(rsValues), 0o600); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write remote-signer values: %w", err)
	}

	if err := WriteWalletMetadata(deploymentDir, wallet); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write wallet metadata: %w", err)
	}

	// Generate helmfile.yaml referencing obol/openclaw + remote-signer
	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0o600); err != nil {
		os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	u.Blank()
	u.Success("OpenClaw instance configured!")
	u.Detail("Deployment", fmt.Sprintf("%s/%s", appName, id))
	u.Detail("Namespace", namespace)
	u.Detail("Hostname", hostname)
	u.Detail("Wallet", wallet.Address)
	u.Detail("Location", deploymentDir)
	u.Blank()
	u.Print("Files created:")
	u.Print("  - values-obol.yaml           Obol Stack overlay (httpRoute, providers, eRPC)")
	u.Print("  - values-remote-signer.yaml  Remote-signer config (keystore password)")
	u.Print("  - wallet.json                Wallet metadata (address, keystore UUID)")
	u.Print("  - helmfile.yaml              Deployment configuration")

	if len(secretData) > 0 {
		u.Printf("  - %s  Local secret values (used to create %s in-cluster)", userSecretsFileName, userSecretsK8sSecretRef)
	}

	u.Blank()
	u.Print("  Back up your signing key:")
	u.Printf("    cp -r %s ~/obol-wallet-backup/", KeystoreVolumePath(cfg, id))

	// Stage default skills to deployment directory (immediate, no cluster needed)
	u.Blank()
	u.Info("Staging default skills...")
	stageDefaultSkills(deploymentDir, u)

	if opts.Sync {
		u.Blank()
		u.Info("Deploying to cluster...")
		u.Blank()

		if err := doSync(cfg, id, u); err != nil {
			return err
		}
		// Register default Ollama models in LiteLLM so the proxy can route them.
		// This is only needed when no cloud provider was configured (the overlay
		// points OpenClaw at LiteLLM, but LiteLLM starts with an empty model_list).
		if len(opts.OllamaModels) > 0 && imported == nil {
			if llmErr := model.ConfigureLiteLLM(cfg, u, "ollama", "", opts.OllamaModels); llmErr != nil {
				u.Warnf("Failed to register Ollama models in LiteLLM: %v", llmErr)
				u.Print("  Run 'obol model setup' to configure models manually.")
			}
		}
		// Copy workspace files into the pod after sync succeeds
		if imported != nil && imported.WorkspaceDir != "" {
			copyWorkspaceToVolume(cfg, id, imported.WorkspaceDir, u)
		}

		return nil
	}

	u.Printf("\nTo deploy: obol openclaw sync %s", id)

	return nil
}

// Sync deploys or updates an OpenClaw instance
func Sync(cfg *config.Config, id string, u *ui.UI) error {
	return doSync(cfg, id, u)
}

func doSync(cfg *config.Config, id string, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nDirectory: %s", appName, id, deploymentDir)
	}

	helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	if _, err := os.Stat(helmfileBinary); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s", helmfileBinary)
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)

	if err := applyUserSecretsIfPresent(cfg, namespace, deploymentDir); err != nil {
		return fmt.Errorf("failed to sync OpenClaw user secrets: %w", err)
	}

	// Ensure wallet keystore + remote-signer values exist (handles
	// deployments created before wallet was added, or manual re-syncs).
	ensureWallet(cfg, id, deploymentDir, u)

	// Stage default skills and inject directly to the host-side PVC path.
	// The local-path-provisioner creates the PV directory on the host at a
	// predictable path ($DATA_DIR/openclaw-<id>/openclaw-data/), so we can
	// pre-populate skills before helmfile sync runs. OpenClaw's file watcher
	// on /data/.openclaw/skills/ picks them up at startup or at runtime.
	stageDefaultSkills(deploymentDir, u)
	injectSkillsToVolume(cfg, id, deploymentDir, u)

	if err := refreshObolHelmRepo(cfg); err != nil {
		return err
	}

	u.Infof("Syncing OpenClaw: %s/%s", appName, id)
	u.Detail("Deployment directory", deploymentDir)

	cmd := exec.Command(helmfileBinary, "-f", helmfilePath, "sync")
	cmd.Dir = deploymentDir

	cmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
	)

	if err := u.Exec(ui.ExecConfig{
		Name: "Running helmfile sync",
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	// Patch ConfigMap to inject heartbeat config that the chart template
	// does not render. The chart's _helpers.tpl only outputs
	// agents.defaults.model and agents.defaults.workspace into openclaw.json,
	// so heartbeat config from values-obol.yaml is silently dropped. We read
	// the rendered ConfigMap, merge heartbeat fields, and re-apply.
	patchHeartbeatConfig(cfg, id, deploymentDir)

	// Apply wallet-metadata ConfigMap (namespace now exists after helmfile sync).
	applyWalletMetadataConfigMap(cfg, id, deploymentDir)

	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)

	u.Blank()
	u.Success("OpenClaw installed successfully!")
	u.Detail("Namespace", namespace)
	u.Detail("URL", "http://"+hostname)
	u.Blank()
	u.Dim("[Optional] Retrieve a gateway token:")
	u.Printf("  obol openclaw token %s", id)
	u.Blank()
	u.Dim("[Optional] Port-forward fallback:")
	u.Printf("  obol kubectl -n %s port-forward svc/openclaw 18789:18789", namespace)

	return nil
}

func refreshObolHelmRepo(cfg *config.Config) error {
	helmBinary := filepath.Join(cfg.BinDir, "helm")
	if _, err := os.Stat(helmBinary); os.IsNotExist(err) {
		return fmt.Errorf("helm not found at %s", helmBinary)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	env := append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	addCmd := exec.Command(helmBinary, "repo", "add", "obol", "https://obolnetwork.github.io/helm-charts/")
	addCmd.Env = env

	addOut, addErr := addCmd.CombinedOutput()
	if addErr != nil && !strings.Contains(string(addOut), `"obol" already exists`) {
		return fmt.Errorf("helm repo add obol failed: %w\n%s", addErr, string(addOut))
	}

	updateCmd := exec.Command(helmBinary, "repo", "update", "obol")
	updateCmd.Env = env

	updateOut, updateErr := updateCmd.CombinedOutput()
	if updateErr != nil {
		return fmt.Errorf("helm repo update obol failed: %w\n%s", updateErr, string(updateOut))
	}

	return nil
}

func writeUserSecretsFile(deploymentDir string, secretData map[string]string) error {
	path := filepath.Join(deploymentDir, userSecretsFileName)
	if len(secretData) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}

		return nil
	}

	payload, err := json.MarshalIndent(secretData, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, payload, 0o600)
}

func loadUserSecretsFile(deploymentDir string) (map[string]string, error) {
	path := filepath.Join(deploymentDir, userSecretsFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // file absent means no user secrets; not an error
		}

		return nil, err
	}

	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", userSecretsFileName, err)
	}

	return out, nil
}

func applyUserSecretsIfPresent(cfg *config.Config, namespace, deploymentDir string) error {
	secretData, err := loadUserSecretsFile(deploymentDir)
	if err != nil {
		return err
	}

	if len(secretData) == 0 {
		return nil
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	if err := ensureNamespaceExists(kubectlBinary, kubeconfigPath, namespace); err != nil {
		return err
	}

	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      userSecretsK8sSecretRef,
			"namespace": namespace,
		},
		"type":       "Opaque",
		"stringData": secretData,
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	cmd := exec.Command(kubectlBinary, "apply", "-f", "-")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = bytes.NewReader(raw)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w\n%s", err, stderr.String())
	}

	return nil
}

func ensureNamespaceExists(kubectlBinary, kubeconfigPath, namespace string) error {
	getCmd := exec.Command(kubectlBinary, "get", "namespace", namespace)

	getCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if err := getCmd.Run(); err == nil {
		return nil
	}

	createCmd := exec.Command(kubectlBinary, "create", "namespace", namespace)

	createCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	var stderr bytes.Buffer

	createCmd.Stderr = &stderr
	if err := createCmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "AlreadyExists") {
			return nil
		}

		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}

	return nil
}

// copyWorkspaceToVolume copies the local workspace directory directly to the
// host-side PVC path that maps to /data/.openclaw/workspace/ in the container.
// This is non-fatal: failures print a warning and continue.
func copyWorkspaceToVolume(cfg *config.Config, id, workspaceDir string, u *ui.UI) {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	targetDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "workspace")

	u.Blank()
	u.Infof("Importing workspace from %s...", workspaceDir)

	ensureVolumeWritable(cfg, targetDir, u)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		u.Warnf("could not create workspace directory: %v", err)
		return
	}

	if err := copyDirRecursive(workspaceDir, targetDir); err != nil {
		u.Warnf("workspace copy failed: %v", err)
		return
	}

	fixVolumeOwnership(cfg, targetDir, u)
	u.Success("Imported workspace to volume")
}

// stageDefaultSkills writes embedded Obol skills to the deployment's config
// directory on the host filesystem. These are pushed to the cluster as a
// ConfigMap during doSync — no pod readiness required.
//
// Always re-stages embedded skills so that new skills added to the binary
// (e.g. buy-inference, discovery) reach existing deployments on the next
// sync. CopySkills only writes files from the embedded FS — user-added
// skills with different names are preserved.
func stageDefaultSkills(deploymentDir string, u *ui.UI) {
	skillsDir := filepath.Join(deploymentDir, "skills")

	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		u.Warnf("could not create skills directory: %v", err)
		return
	}

	if err := obolembed.CopySkills(skillsDir); err != nil {
		u.Warnf("could not stage default skills: %v", err)
		return
	}

	names, _ := obolembed.GetEmbeddedSkillNames()
	for _, name := range names {
		u.Successf("Staged skill: %s", name)
	}
}

// skillsVolumePath returns the host-side path to the OpenClaw skills
// directory inside the PVC provisioned by local-path-provisioner.
//
// The local-path-provisioner creates PV directories at:
//
//	$DATA_DIR/<namespace>/<pvc-name>/
//
// The OpenClaw chart creates a PVC named "openclaw-data" mounted at /data
// in the container. OpenClaw watches /data/.openclaw/skills/ for skill files.
//
// Host paths by mode:
//
//	Dev:  .workspace/data/openclaw-<id>/openclaw-data/.openclaw/skills/
//	Prod: ~/.local/share/obol/openclaw-<id>/openclaw-data/.openclaw/skills/
func skillsVolumePath(cfg *config.Config, id string) string {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	return filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "skills")
}

// injectSkillsToVolume copies staged skills directly to the host-side PVC
// path that maps to /data/.openclaw/skills/ inside the OpenClaw container.
// This is called before helmfile sync so skills are present at first pod boot.
// OpenClaw's file watcher detects new/changed skills at runtime.
func injectSkillsToVolume(cfg *config.Config, id string, deploymentDir string, u *ui.UI) {
	skillsSrc := filepath.Join(deploymentDir, "skills")

	info, err := os.Stat(skillsSrc)
	if err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(skillsSrc)
	if err != nil {
		return
	}

	hasSkills := false

	for _, e := range entries {
		if e.IsDir() {
			hasSkills = true
			break
		}
	}

	if !hasSkills {
		return
	}

	targetDir := skillsVolumePath(cfg, id)
	ensureVolumeWritable(cfg, targetDir, u)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		u.Warnf("could not create skills volume directory: %v", err)
		return
	}

	u.Info("Injecting skills to volume...")

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		src := filepath.Join(skillsSrc, e.Name())

		dst := filepath.Join(targetDir, e.Name())
		if err := copyDirRecursive(src, dst); err != nil {
			u.Warnf("could not inject skill %s: %v", e.Name(), err)
			continue
		}

		u.Successf("Injected skill: %s", e.Name())
	}

	fixVolumeOwnership(cfg, targetDir, u)
}

// k3dNodeExec runs a shell command inside the k3d node container, translating
// the host-side path to the in-node path (/data/…).  Returns an error when
// the command fails or the path is outside the data directory.  This is the
// shared core for fixVolumeOwnership and ensureVolumeWritable.
func k3dNodeExec(cfg *config.Config, hostPath, shellCmd string) error {
	stackID := ""
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-id")); err == nil {
		stackID = strings.TrimSpace(string(data))
	}
	if stackID == "" {
		return fmt.Errorf("stack ID not found")
	}

	container := fmt.Sprintf("k3d-obol-stack-%s-server-0", stackID)

	// Convert host path to the in-node path.  k3d mounts $DATA_DIR → /data.
	relPath, err := filepath.Rel(cfg.DataDir, hostPath)
	if err != nil {
		return fmt.Errorf("cannot compute relative path from %s to %s: %w", cfg.DataDir, hostPath, err)
	}
	if strings.HasPrefix(relPath, "..") {
		return fmt.Errorf("path %s is not under DataDir %s", hostPath, cfg.DataDir)
	}
	nodePath := filepath.Join("/data", relPath)

	// Replace the placeholder with the shell-quoted node path.
	quoted := "'" + strings.ReplaceAll(nodePath, "'", "'\"'\"'") + "'"
	expanded := strings.ReplaceAll(shellCmd, "{}", quoted)

	cmd := exec.Command("docker", "exec", container, "sh", "-c", expanded)
	return cmd.Run()
}

// fixVolumeOwnership normalises file ownership on a host-side PVC path so the
// container (UID 1000 / node) can read and write.  On k3d the host path is
// inside a Docker container (the k3d node), so we exec into it as root and
// chown recursively.  On k3s the host IS the node, so we attempt a direct
// chown (works when the CLI runs as root, harmless no-op otherwise).
//
// The optional ui parameter enables user-visible warnings when chown fails.
// Pass nil when no UI context is available (e.g. GenerateWallet).
func fixVolumeOwnership(cfg *config.Config, hostPath string, u *ui.UI) {
	backendName := "k3d"
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-backend")); err == nil {
		backendName = strings.TrimSpace(string(data))
	}

	switch backendName {
	case "k3d":
		if err := k3dNodeExec(cfg, hostPath, "chown -R 1000:1000 {}"); err != nil {
			if u != nil {
				u.Warnf("Failed to fix volume ownership for %s: %v", hostPath, err)
			}
		}
	default:
		// k3s — direct host, try chown (succeeds if root).
		cmd := exec.Command("chown", "-R", "1000:1000", hostPath)
		if err := cmd.Run(); err != nil {
			if u != nil {
				u.Warnf("Failed to fix volume ownership for %s: %v (expected if not root)", hostPath, err)
			}
		}
	}
}

// ensureVolumeWritable pre-creates a host-side PVC directory and chowns it to
// the current (host) user so subsequent host-side writes succeed.  On k3d, the
// local-path-provisioner creates directories as root inside the node container,
// making them root-owned on the host.  Best-effort: failures are logged but do
// not block provisioning.
func ensureVolumeWritable(cfg *config.Config, hostPath string, u *ui.UI) {
	backendName := "k3d"
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-backend")); err == nil {
		backendName = strings.TrimSpace(string(data))
	}

	if backendName != "k3d" {
		return
	}

	uid := os.Getuid()
	gid := os.Getgid()
	shellCmd := fmt.Sprintf("mkdir -p {} && chown -R %d:%d {}", uid, gid)

	if err := k3dNodeExec(cfg, hostPath, shellCmd); err != nil {
		if u != nil {
			u.Warnf("Could not pre-create volume directory %s: %v", hostPath, err)
		}
	}
}

// copyDirRecursive copies a directory tree from src to dst, creating
// directories and copying files with 0755/0644 permissions.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}

		data, err := os.ReadFile(path) //nolint:gosec // G122: local skill files from embedded assets, no symlink risk
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, data, 0o600) //nolint:gosec // G703: targetPath from user's local config dir
	})
}

// waitForPod polls for a Running pod matching the openclaw label and returns its name.
// Returns an error if no ready pod is found within timeoutSec seconds.
func waitForPod(kubectlBinary, kubeconfigPath, namespace string, timeoutSec int) (string, error) {
	labelSelector := "app.kubernetes.io/name=" + appName

	for i := 0; i < timeoutSec; i += 3 {
		cmd := exec.Command(kubectlBinary, "get", "pods",
			"-n", namespace,
			"-l", labelSelector,
			"-o", "jsonpath={.items[?(@.status.phase=='Running')].metadata.name}",
		)

		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

		var stdout bytes.Buffer

		cmd.Stdout = &stdout
		_ = cmd.Run()

		podName := strings.TrimSpace(stdout.String())
		if podName != "" {
			// If multiple pods, take the first
			if idx := strings.Index(podName, " "); idx > 0 {
				podName = podName[:idx]
			}

			return podName, nil
		}

		time.Sleep(3 * time.Second)
	}

	return "", fmt.Errorf("timed out waiting for pod in namespace %s", namespace)
}

// getToken retrieves the gateway token for an OpenClaw instance as a string.
func getToken(cfg *config.Config, id string) (string, error) {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlBinary, "get", "secret", "-n", namespace,
		"-l", "app.kubernetes.io/name="+appName,
		"-o", "json")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get secret: %w\n%s", err, stderr.String())
	}

	var secretList struct {
		Items []struct {
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &secretList); err != nil {
		return "", fmt.Errorf("failed to parse secret: %w", err)
	}

	if len(secretList.Items) == 0 {
		return "", fmt.Errorf("no secrets found in namespace %s. Is OpenClaw deployed?", namespace)
	}

	for _, item := range secretList.Items {
		if encoded, ok := item.Data["OPENCLAW_GATEWAY_TOKEN"]; ok {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", fmt.Errorf("failed to decode token: %w", err)
			}

			return string(decoded), nil
		}
	}

	return "", fmt.Errorf("OPENCLAW_GATEWAY_TOKEN not found in namespace %s secrets", namespace)
}

// Token retrieves the gateway token for an OpenClaw instance and prints it.
func Token(cfg *config.Config, id string, u *ui.UI) error {
	token, err := getToken(cfg, id)
	if err != nil {
		return err
	}

	u.Print(token)

	return nil
}

// RegenerateToken restarts the OpenClaw pod to generate a new gateway token,
// then retrieves and returns the new token.
func RegenerateToken(cfg *config.Config, id string, u *ui.UI) (string, error) {
	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	// Delete the existing secret so a fresh token is generated on restart.
	u.Info("Deleting existing gateway token...")

	deleteCmd := exec.Command(kubectlBinary, "delete", "secret",
		"-n", namespace,
		"-l", "app.kubernetes.io/name="+appName,
		"--ignore-not-found")

	deleteCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := deleteCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to delete secret: %w\n%s", err, string(out))
	}

	// Restart the deployment to regenerate the token.
	u.Info("Restarting OpenClaw to regenerate token...")

	restartCmd := exec.Command(kubectlBinary, "rollout", "restart",
		"deployment/openclaw", "-n", namespace)

	restartCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := restartCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to restart deployment: %w\n%s", err, string(out))
	}

	// Wait for rollout to complete.
	u.Info("Waiting for new pod to start...")

	waitCmd := exec.Command(kubectlBinary, "rollout", "status",
		"deployment/openclaw", "-n", namespace, "--timeout=120s")

	waitCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := waitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("rollout not confirmed: %w\n%s", err, string(out))
	}

	// Wait briefly for the token secret to be created.
	time.Sleep(5 * time.Second)

	// Retrieve the new token.
	newToken, err := getToken(cfg, id)
	if err != nil {
		return "", fmt.Errorf("token regenerated but could not retrieve it: %w", err)
	}

	u.Success("Token regenerated successfully")

	return newToken, nil
}

// findOpenClawBinary locates the openclaw CLI binary.
// Search order: PATH, then cfg.BinDir.
func findOpenClawBinary(cfg *config.Config) (string, error) {
	if p, err := exec.LookPath("openclaw"); err == nil {
		return p, nil
	}

	candidate := filepath.Join(cfg.BinDir, "openclaw")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	return "", errors.New("openclaw CLI not found.\n\nInstall with one of:\n  obolup.sh                                    (re-run bootstrap installer)\n  curl -fsSL https://openclaw.ai/install.sh | bash\n  npm install -g openclaw                      (requires Node.js 22+)")
}

// portForwarder manages a background kubectl port-forward process.
type portForwarder struct {
	cmd       *exec.Cmd
	localPort int
	done      chan error
	cancel    context.CancelFunc
}

// startPortForward launches kubectl port-forward in the background and waits
// until it reports the forwarding address on stdout.
func startPortForward(cfg *config.Config, namespace string, localPort int) (*portForwarder, error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	portArg := fmt.Sprintf("%d:18789", localPort)
	if localPort == 0 {
		portArg = ":18789"
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, kubectlBinary, "port-forward",
		"svc/"+appName, portArg, "-n", namespace)

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	// kubectl prints "Forwarding from ..." to stdout (not stderr)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start port-forward: %w", err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	// Parse the "Forwarding from 127.0.0.1:<port>" line from stdout
	parsedPort := make(chan int, 1)
	parseErr := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			// kubectl prints: "Forwarding from 127.0.0.1:<port> -> 18789"
			if strings.Contains(line, "Forwarding from") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					portPart := strings.Fields(parts[len(parts)-1])[0]

					var p int
					if _, err := fmt.Sscanf(portPart, "%d", &p); err == nil {
						parsedPort <- p
						// Continue draining to prevent pipe blocking
						_, _ = io.Copy(io.Discard, stdoutPipe)

						return
					}
				}
			}
		}

		parseErr <- errors.New("port-forward exited without reporting a local port")
	}()

	select {
	case p := <-parsedPort:
		return &portForwarder{cmd: cmd, localPort: p, done: done, cancel: cancel}, nil
	case err := <-parseErr:
		cancel()
		return nil, err
	case err := <-done:
		cancel()

		if err != nil {
			return nil, fmt.Errorf("port-forward process exited unexpectedly: %w", err)
		}

		return nil, errors.New("port-forward process exited unexpectedly")
	case <-time.After(30 * time.Second):
		cancel()
		return nil, errors.New("timed out waiting for port-forward to become ready")
	}
}

// Stop terminates the port-forward process gracefully.
func (pf *portForwarder) Stop() {
	pf.cancel()

	select {
	case <-pf.done:
	case <-time.After(5 * time.Second):
		if pf.cmd.Process != nil {
			_ = pf.cmd.Process.Kill()
		}
	}
}

// SetupOptions contains options for the setup command.
type SetupOptions struct {
	Port int // kept for backward compat; currently unused
}

// Setup reconfigures model providers for a deployed OpenClaw instance.
// It runs the interactive provider prompt, regenerates the overlay values,
// and syncs via helmfile so the pod picks up the new configuration.
func Setup(cfg *config.Config, id string, _ SetupOptions, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw onboard' first", appName, id)
	}

	// Always show the provider prompt — that's the whole point of setup.
	imported, cloudProvider, err := interactiveSetup(cfg, nil)
	if err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Push cloud API key to LiteLLM if a cloud provider was selected
	if cloudProvider != nil {
		if llmErr := model.ConfigureLiteLLM(cfg, u, cloudProvider.Name, cloudProvider.APIKey, []string{cloudProvider.ModelID}); llmErr != nil {
			return fmt.Errorf("failed to configure LiteLLM: %w", llmErr)
		}
	}

	// Regenerate helmfile to pick up any chart version bumps
	namespace := fmt.Sprintf("%s-%s", appName, id)

	helmfileContent := generateHelmfile(id, namespace)
	if err := os.WriteFile(filepath.Join(deploymentDir, "helmfile.yaml"), []byte(helmfileContent), 0o600); err != nil {
		return fmt.Errorf("failed to write helmfile.yaml: %w", err)
	}

	// Regenerate overlay values with the selected provider
	hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)

	secretData := collectSensitiveData(imported)
	if err := writeUserSecretsFile(deploymentDir, secretData); err != nil {
		return fmt.Errorf("failed to write OpenClaw secrets metadata: %w", err)
	}

	overlay := generateOverlayValues(cfg, hostname, imported, len(secretData) > 0, nil, "")

	overlayPath := filepath.Join(deploymentDir, "values-obol.yaml")
	if err := os.WriteFile(overlayPath, []byte(overlay), 0o600); err != nil {
		return fmt.Errorf("failed to write overlay values: %w", err)
	}

	u.Blank()
	u.Info("Applying configuration...")
	u.Blank()

	if err := doSync(cfg, id, u); err != nil {
		return err
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	u.Blank()
	u.Info("Waiting for the OpenClaw gateway to be ready...")

	if _, err := waitForPod(kubectlBinary, kubeconfigPath, namespace, 90); err != nil {
		u.Warnf("pod not ready yet: %v", err)
		u.Printf("The deployment may still be rolling out. Check with: obol kubectl get pods -n %s", namespace)
		u.Print("Or track the status from http://obol.stack")
	} else {
		u.Blank()
		u.Success("Setup complete!")
		u.Print("  Access the OpenClaw dashboard from http://obol.stack")
	}

	return nil
}

// DashboardOptions contains options for the dashboard command.
type DashboardOptions struct {
	Port      int
	NoBrowser bool
}

// Dashboard port-forwards to the OpenClaw instance and opens the web dashboard.
// The onReady callback is invoked with the dashboard URL; the CLI layer uses it
// to open a browser.
func Dashboard(cfg *config.Config, id string, opts DashboardOptions, onReady func(url string), u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	token, err := getToken(cfg, id)
	if err != nil {
		return err
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)
	u.Infof("Starting port-forward to %s...", namespace)

	pf, err := startPortForward(cfg, namespace, opts.Port)
	if err != nil {
		return fmt.Errorf("port-forward failed: %w", err)
	}
	defer pf.Stop()

	dashboardURL := fmt.Sprintf("http://localhost:%d/#token=%s", pf.localPort, token)
	u.Successf("Port-forward active: localhost:%d -> %s:18789", pf.localPort, namespace)
	u.Blank()
	u.Detail("Dashboard URL", dashboardURL)
	u.Detail("Gateway token", token)
	u.Blank()
	u.Dim("Press Ctrl+C to stop.")

	if onReady != nil {
		onReady(dashboardURL)
	}

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-sigCh:
		u.Blank()
		u.Info("Shutting down...")
	case err := <-pf.done:
		if err != nil {
			return fmt.Errorf("port-forward died unexpectedly: %w", err)
		}
	}

	return nil
}

// List displays installed OpenClaw instances
// openclawInstance is the JSON-serialisable representation of one instance.
type openclawInstance struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
}

func List(cfg *config.Config, u *ui.UI) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		if u.IsJSON() {
			return u.JSON([]openclawInstance{})
		}
		u.Print("No OpenClaw instances installed")
		u.Print("\nTo create one: obol openclaw up")

		return nil
	}

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	if len(entries) == 0 {
		if u.IsJSON() {
			return u.JSON([]openclawInstance{})
		}
		u.Print("No OpenClaw instances installed")
		return nil
	}

	var instances []openclawInstance
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		id := entry.Name()
		namespace := fmt.Sprintf("%s-%s", appName, id)
		hostname := fmt.Sprintf("openclaw-%s.%s", id, defaultDomain)
		instances = append(instances, openclawInstance{
			ID:        id,
			Namespace: namespace,
			URL:       fmt.Sprintf("http://%s", hostname),
		})
	}

	if u.IsJSON() {
		return u.JSON(instances)
	}

	u.Info("OpenClaw instances:")
	u.Blank()

	for _, inst := range instances {
		u.Bold("  " + inst.ID)
		u.Detail("  Namespace", inst.Namespace)
		u.Detail("  URL", inst.URL)
		u.Blank()
	}

	u.Printf("Total: %d instance(s)", len(instances))
	return nil
}

// Delete removes an OpenClaw instance
func Delete(cfg *config.Config, id string, force bool, u *ui.UI) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	deploymentDir := DeploymentPath(cfg, id)

	u.Infof("Deleting OpenClaw: %s/%s", appName, id)
	u.Detail("Namespace", namespace)

	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	namespaceExists := false

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespace)

		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("instance not found: %s", id)
	}

	u.Blank()
	u.Print("Resources to be deleted:")

	if namespaceExists {
		u.Printf("  [x] Kubernetes namespace: %s", namespace)
	} else {
		u.Printf("  [ ] Kubernetes namespace: %s (not found)", namespace)
	}

	if configExists {
		u.Printf("  [x] Configuration: %s", deploymentDir)
	}

	if !force {
		if !u.Confirm("\nProceed with deletion?", false) {
			u.Print("Deletion cancelled")
			return nil
		}
	}

	if namespaceExists {
		// Run helmfile destroy first to cleanly remove Helm releases.
		// This ensures StatefulSet PVCs are properly cleaned up before namespace deletion.
		helmfilePath := filepath.Join(deploymentDir, "helmfile.yaml")

		helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
		if _, err := os.Stat(helmfilePath); err == nil {
			if _, err := os.Stat(helmfileBinary); err == nil {
				destroyCmd := exec.Command(helmfileBinary, "-f", helmfilePath, "destroy")
				destroyCmd.Dir = deploymentDir

				destroyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

				if err := u.Exec(ui.ExecConfig{
					Name: "Removing Helm releases from " + namespace,
					Cmd:  destroyCmd,
				}); err != nil {
					u.Warnf("helmfile destroy failed (will force-delete namespace): %v", err)
				}
			}
		}

		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		deleteCmd := exec.Command(kubectlBinary, "delete", "namespace", namespace,
			"--force", "--grace-period=0")

		deleteCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

		if err := u.Exec(ui.ExecConfig{
			Name: "Deleting namespace " + namespace,
			Cmd:  deleteCmd,
		}); err != nil {
			u.Warnf("namespace deletion may still be in progress: %v", err)
		}
	}

	if configExists {
		u.Info("Deleting configuration...")

		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}

		u.Success("Configuration deleted")

		parentDir := filepath.Join(cfg.ConfigDir, "applications", appName)

		entries, err := os.ReadDir(parentDir)
		if err == nil && len(entries) == 0 {
			os.Remove(parentDir)
		}
	}

	u.Blank()
	u.Successf("OpenClaw %s deleted successfully!", id)

	return nil
}

// SkillsSync copies a local skills directory to the host-side PVC path that
// maps to /data/.openclaw/skills/ inside the OpenClaw container. OpenClaw's
// file watcher detects changes automatically — no pod restart needed.
func SkillsSync(cfg *config.Config, id, skillsDir string, u *ui.UI) error {
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		return fmt.Errorf("skills directory not found: %s", skillsDir)
	}

	targetDir := skillsVolumePath(cfg, id)

	u.Infof("Syncing skills from %s to volume...", skillsDir)

	ensureVolumeWritable(cfg, targetDir, u)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create skills volume directory: %w", err)
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("failed to read skills directory: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		src := filepath.Join(skillsDir, e.Name())

		dst := filepath.Join(targetDir, e.Name())
		if err := copyDirRecursive(src, dst); err != nil {
			return fmt.Errorf("failed to copy skill %s: %w", e.Name(), err)
		}

		u.Successf("Synced skill: %s", e.Name())
	}

	fixVolumeOwnership(cfg, targetDir, u)
	u.Success("Skills synced to volume (file watcher will reload)")

	return nil
}

// SkillAdd adds a skill to a deployed OpenClaw instance by running the native
// openclaw CLI inside the pod via kubectl exec.
func SkillAdd(cfg *config.Config, id string, args []string, u *ui.UI) error {
	_ = u // interactive passthrough — subprocess owns stdout/stderr
	namespace := fmt.Sprintf("%s-%s", appName, id)

	return cliViaKubectlExec(cfg, namespace, append([]string{"skills", "add"}, args...))
}

// SkillRemove removes a skill from a deployed OpenClaw instance by running the
// native openclaw CLI inside the pod via kubectl exec.
func SkillRemove(cfg *config.Config, id string, args []string, u *ui.UI) error {
	_ = u // interactive passthrough — subprocess owns stdout/stderr
	namespace := fmt.Sprintf("%s-%s", appName, id)

	return cliViaKubectlExec(cfg, namespace, append([]string{"skills", "remove"}, args...))
}

// SkillList lists skills installed on a deployed OpenClaw instance by running
// the native openclaw CLI inside the pod via kubectl exec.
func SkillList(cfg *config.Config, id string, u *ui.UI) error {
	_ = u // interactive passthrough — subprocess owns stdout/stderr
	namespace := fmt.Sprintf("%s-%s", appName, id)

	return cliViaKubectlExec(cfg, namespace, []string{"skills", "list"})
}

// remoteCapableCommands lists openclaw subcommands that support --url and --token flags.
var remoteCapableCommands = map[string]bool{
	"gateway": true,
	"acp":     true,
	"browser": true,
	"logs":    true,
}

// CLI runs an openclaw CLI command against a deployed instance.
// Commands that support --url/--token are executed locally with a port-forward;
// others are executed via kubectl exec into the pod.
func CLI(cfg *config.Config, id string, args []string, u *ui.UI) error {
	_ = u // interactive passthrough — subprocess owns stdout/stderr

	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw up' first", appName, id)
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)

	if len(args) == 0 {
		return fmt.Errorf("no openclaw command specified\n\nExamples:\n"+
			"  obol openclaw cli %s -- gateway health\n"+
			"  obol openclaw cli %s -- gateway call config.get\n"+
			"  obol openclaw cli %s -- doctor", id, id, id)
	}

	// Determine if the command supports --url/--token (remote-capable)
	firstArg := args[0]
	if remoteCapableCommands[firstArg] {
		return cliViaPortForward(cfg, id, namespace, args)
	}

	return cliViaKubectlExec(cfg, namespace, args)
}

// cliViaPortForward runs an openclaw command locally with port-forward + --url/--token.
func cliViaPortForward(cfg *config.Config, id, namespace string, args []string) error {
	openclawBinary, err := findOpenClawBinary(cfg)
	if err != nil {
		return err
	}

	token, err := getToken(cfg, id)
	if err != nil {
		return fmt.Errorf("failed to get gateway token: %w", err)
	}

	pf, err := startPortForward(cfg, namespace, 0)
	if err != nil {
		return fmt.Errorf("port-forward failed: %w", err)
	}
	defer pf.Stop()

	// Append --url and --token to the args
	wsURL := fmt.Sprintf("ws://localhost:%d", pf.localPort)
	fullArgs := append(append([]string{}, args...), "--url", wsURL, "--token", token)

	cmd := exec.Command(openclawBinary, fullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Handle signals to clean up port-forward
	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		<-sigCh
		pf.Stop()
	}()

	if err := cmd.Run(); err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus()) //nolint:gocritic // intentional exit to propagate child exit code; defers handle cleanup
			}
		}

		return err
	}

	return nil
}

// cliViaKubectlExec runs an openclaw command inside the pod via kubectl exec.
func cliViaKubectlExec(cfg *config.Config, namespace string, args []string) error {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	// Build: kubectl exec -it -c openclaw -n <ns> deploy/openclaw -- node openclaw.mjs <args>
	// The pod runs `node openclaw.mjs` (no standalone binary in PATH).
	// -c openclaw is required because the pod has an init container (extract-skills).
	execArgs := []string{
		"exec", "-it",
		"-c", "openclaw",
		"-n", namespace,
		"deploy/openclaw",
		"--",
		"node", "openclaw.mjs",
	}
	execArgs = append(execArgs, args...)

	cmd := exec.Command(kubectlBinary, execArgs...)

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}

		return err
	}

	return nil
}

// SyncOverlayModels updates all deployed OpenClaw instances to match the
// current LiteLLM model list. For each LiteLLM-routed instance it:
//  1. Patches the overlay YAML model list (for helm consistency)
//  2. Writes a clean per-agent models.json to the host PVC
//  3. Patches the openclaw-config ConfigMap with the best primary model
//
// Cloud models are promoted to primary (they're added because they're better
// than local defaults). The previous primary becomes a fallback.
func SyncOverlayModels(cfg *config.Config, models []string, u *ui.UI) error {
	ids, err := ListInstanceIDs(cfg)
	if err != nil || len(ids) == 0 {
		return nil //nolint:nilerr // no instances to sync; not an error
	}

	masterKey := litellmMasterKey(cfg)

	for _, id := range ids {
		overlayPath := filepath.Join(DeploymentPath(cfg, id), "values-obol.yaml")

		data, err := os.ReadFile(overlayPath)
		if err != nil {
			continue
		}

		content := string(data)

		// Only patch instances that use the LiteLLM gateway pattern
		if !strings.Contains(content, "litellm.llm.svc") {
			continue
		}

		// Update overlay YAML (for helm consistency on next sync)
		updated, changed := patchOverlayModelList(content, models)
		if changed {
			if err := os.WriteFile(overlayPath, []byte(updated), 0o600); err != nil { //nolint:gosec // G703: path from user's local config dir
				u.Warnf("Failed to update overlay for %s: %v", id, err)
			}
		}

		// Write clean models.json — file watcher handles hot-reload
		if err := patchAgentModelsJSON(cfg, id, models, masterKey); err != nil {
			u.Warnf("Failed to patch models.json for %s: %v", id, err)
		}

		// Patch ConfigMap with best primary model + fallbacks
		primary, fallbacks := rankModels(models)
		if primary != "" {
			patchModelHierarchy(cfg, id, primary, fallbacks, u)
		}

		u.Infof("Updated models for OpenClaw %s (%d models, primary: %s)", id, len(models), primary)
	}

	return nil
}

// rankModels picks the best model as primary and demotes the rest to fallbacks.
// Cloud models (Anthropic, OpenAI) are ranked above local models (Ollama).
// Within a tier, the first model wins.
func rankModels(models []string) (primary string, fallbacks []string) {
	if len(models) == 0 {
		return "", nil
	}

	// Partition into cloud and local
	var cloud, local []string

	for _, m := range models {
		if isCloudModel(m) {
			cloud = append(cloud, m)
		} else {
			local = append(local, m)
		}
	}

	// Best cloud model is primary; rest are fallbacks (cloud first, then local)
	if len(cloud) > 0 {
		primary = cloud[0]
		fallbacks = append(append([]string{}, cloud[1:]...), local...)
	} else {
		primary = local[0]
		fallbacks = local[1:]
	}

	// Prefix with openai/ for LiteLLM routing
	primary = "openai/" + primary

	for i, f := range fallbacks {
		fallbacks[i] = "openai/" + f
	}

	return primary, fallbacks
}

// isCloudModel returns true if the model name looks like a cloud provider model.
func isCloudModel(name string) bool {
	if strings.Contains(name, "claude") {
		return true
	}

	if strings.HasPrefix(name, "gpt") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") {
		return true
	}

	return false
}

// patchModelHierarchy updates the openclaw-config ConfigMap with the given
// primary model and fallbacks. This is what the frontend reads to display
// the current model, and what OpenClaw uses for agent model selection.
func patchModelHierarchy(cfg *config.Config, id, primary string, fallbacks []string, u *ui.UI) {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	// Read current ConfigMap
	getCmd := exec.Command(kubectlBinary, "get", "configmap", "openclaw-config",
		"-n", namespace, "-o", "jsonpath={.data.openclaw\\.json}")

	getCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	var out bytes.Buffer

	getCmd.Stdout = &out
	if err := getCmd.Run(); err != nil {
		return // ConfigMap may not exist yet
	}

	var cfgJSON map[string]any
	if err := json.Unmarshal(out.Bytes(), &cfgJSON); err != nil {
		return
	}

	// Navigate to agents.defaults.model
	agents, ok := cfgJSON["agents"].(map[string]any)
	if !ok {
		agents = map[string]any{}
		cfgJSON["agents"] = agents
	}

	defaults, ok := agents["defaults"].(map[string]any)
	if !ok {
		defaults = map[string]any{}
		agents["defaults"] = defaults
	}

	modelCfg := map[string]any{
		"primary": primary,
	}
	if len(fallbacks) > 0 {
		modelCfg["fallbacks"] = fallbacks
	}

	defaults["model"] = modelCfg

	// Re-serialize and apply
	patched, err := json.MarshalIndent(cfgJSON, "    ", "  ")
	if err != nil {
		return
	}

	applyPayload := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "openclaw-config",
			"namespace": namespace,
		},
		"data": map[string]string{
			"openclaw.json": string(patched),
		},
	}
	applyRaw, _ := json.Marshal(applyPayload) //nolint:errchkjson // map[string]any is safe, keys/values are controlled

	applyCmd := exec.Command(kubectlBinary, "apply", "-f", "-",
		"--server-side", "--field-manager=helm", "--force-conflicts")

	applyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	applyCmd.Stdin = bytes.NewReader(applyRaw)
	if err := applyCmd.Run(); err != nil {
		u.Warnf("Failed to patch model hierarchy in ConfigMap: %v", err)
	}
}

// patchAgentModelsJSON writes a clean models.json to the per-agent directory
// on the host-side PVC. This replaces any stale config and lets OpenClaw's
// file watcher hot-reload the new model list without a pod restart.
//
// Path: $DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/agents/main/agent/models.json
func patchAgentModelsJSON(cfg *config.Config, id string, models []string, masterKey string) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	agentDir := filepath.Join(cfg.DataDir, namespace, "openclaw-data", ".openclaw", "agents", "main", "agent")
	modelsPath := filepath.Join(agentDir, "models.json")

	// Only patch if the agent directory exists (agent has been initialized)
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return nil
	}

	type modelEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	type provider struct {
		BaseURL string       `json:"baseUrl"`
		APIKey  string       `json:"apiKey"`
		API     string       `json:"api"`
		Models  []modelEntry `json:"models"`
	}

	entries := make([]modelEntry, 0, len(models))
	for _, m := range models {
		entries = append(entries, modelEntry{ID: m, Name: m})
	}

	modelsJSON := map[string]any{
		"providers": map[string]any{
			"openai": provider{
				BaseURL: "http://litellm.llm.svc.cluster.local:4000/v1",
				APIKey:  masterKey,
				API:     "openai-completions",
				Models:  entries,
			},
		},
	}

	data, err := json.MarshalIndent(modelsJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal models.json: %w", err)
	}

	data = append(data, '\n')

	return os.WriteFile(modelsPath, data, 0o600)
}

// patchOverlayModelList replaces the model list under the openai provider
// in a values-obol.yaml overlay with the given models. Returns the updated
// content and whether a change was made.
func patchOverlayModelList(content string, models []string) (string, bool) {
	// Find the "    models:" line that follows the openai provider section.
	// The overlay structure is:
	//   models:
	//     openai:
	//       ...
	//       models:
	//         - id: <model>
	//           name: <display>
	lines := strings.Split(content, "\n")

	var result []string

	inOpenAI := false
	inModelList := false

	var modelListIndent string

	var replaced bool

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track when we enter the openai provider section
		if trimmed == "openai:" && strings.Contains(content, "litellm.llm.svc") {
			inOpenAI = true

			result = append(result, line)

			continue
		}

		// Detect the models list under the openai section
		if inOpenAI && !inModelList && (trimmed == "models:" || trimmed == "models: []") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			modelListIndent = indent
			inModelList = true
			replaced = true

			// Write the new model list
			if len(models) == 0 {
				result = append(result, indent+"models: []")
			} else {
				result = append(result, indent+"models:")
				for _, m := range models {
					result = append(result, indent+"  - id: "+m)
					result = append(result, indent+"    name: "+ollamaModelDisplayName(m))
				}
			}

			// Skip old model entries
			if trimmed == "models: []" {
				continue
			}

			for i+1 < len(lines) {
				next := lines[i+1]

				nextTrimmed := strings.TrimSpace(next)
				if nextTrimmed == "" || (strings.HasPrefix(next, modelListIndent+"  ") && (strings.HasPrefix(nextTrimmed, "- id:") || strings.HasPrefix(nextTrimmed, "name:"))) {
					i++
					continue
				}

				break
			}

			continue
		}

		// Detect when we leave the openai section (non-indented line)
		if inOpenAI && !strings.HasPrefix(line, "    ") && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			inOpenAI = false
		}

		result = append(result, line)
	}

	if !replaced {
		return content, false
	}

	return strings.Join(result, "\n"), true
}

// DeploymentPath returns the path to a deployment directory.
func DeploymentPath(cfg *config.Config, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", appName, id)
}

// generateOverlayValues creates the Obol Stack-specific values overlay.
// If imported is non-nil, provider/channel config from the import is used
// instead of the default Ollama configuration.
// litellmMasterKey returns the LiteLLM master key derived from the cluster's stack ID.
func litellmMasterKey(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, ".stack-id")

	data, err := os.ReadFile(stackIDPath)
	if err != nil {
		return "sk-obol-default" // fallback if stack ID not found
	}

	return "sk-obol-" + strings.TrimSpace(string(data))
}

func generateOverlayValues(cfg *config.Config, hostname string, imported *ImportResult, useExternalSecrets bool, ollamaModels []string, agentBaseURL string) string {
	var b strings.Builder

	b.WriteString(`# Obol Stack overlay values for OpenClaw
# This file contains stack-specific defaults. Edit to customize.

# Enable Gateway API HTTPRoute for stack routing
httpRoute:
  enabled: true
  hostnames:
`)
	fmt.Fprintf(&b, "    - %s\n", hostname)
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

	// Override chart default image tag when the binary pins a newer version.
	if tag := openclawImageTag(); tag != "" {
		fmt.Fprintf(&b, "# Override chart default image tag (chart ships %s)\nimage:\n  tag: \"%s\"\n\n", chartVersion, tag)
	}

	// Provider and agent model configuration.
	//
	// All inference is routed through the LiteLLM gateway (openai provider slot).
	// This ensures OpenClaw never tries to call Anthropic/OpenAI natively (which
	// would require its own API keys in the auth store). Instead, LiteLLM handles
	// provider routing and API key management via its own Secret.
	//
	// If the user has an imported ~/.openclaw/openclaw.json, we extract non-model
	// config (channels, etc.) but always override the provider to LiteLLM.

	// Determine agent model: prefer imported model, fallback to preferred Ollama model.
	agentModel := ""
	if imported != nil && imported.AgentModel != "" {
		// Rewrite native provider prefix to openai/ so it routes through LiteLLM.
		// e.g. "anthropic/claude-sonnet-4-6" → "openai/claude-sonnet-4-6"
		agentModel = imported.AgentModel
		if i := strings.Index(agentModel, "/"); i >= 0 {
			agentModel = "openai/" + agentModel[i+1:]
		} else if !strings.HasPrefix(agentModel, "openai/") {
			agentModel = "openai/" + agentModel
		}
	} else if len(ollamaModels) > 0 {
		agentModel = "openai/" + preferredOllamaModel(ollamaModels)
	}

	b.WriteString("# All models route through LiteLLM gateway (openai provider slot).\n")
	b.WriteString("openclaw:\n")

	if agentModel != "" {
		fmt.Fprintf(&b, "  agentModel: %s\n", agentModel)
	}

	b.WriteString(`  gateway:
    # Allow control UI over HTTP behind Traefik (local dev stack).
    # dangerouslyDisableDeviceAuth is needed because Traefik proxies from
    # the k3d bridge IP, not localhost. Token auth is still enforced.
    controlUi:
      allowInsecureAuth: true
      dangerouslyDisableDeviceAuth: true
      dangerouslyAllowHostHeaderOriginFallback: true

# LiteLLM gateway: OpenAI-compatible proxy routing to all configured providers.
# Uses "openai" provider slot — LiteLLM routes by model name, not provider type.
models:
  openai:
    enabled: true
    baseUrl: http://litellm.llm.svc.cluster.local:4000/v1
    api: openai-completions
    apiKeyEnvVar: OPENAI_API_KEY
`)
	fmt.Fprintf(&b, "    apiKeyValue: %s\n", litellmMasterKey(cfg))

	if len(ollamaModels) > 0 {
		b.WriteString("    models:\n")

		for _, m := range ollamaModels {
			fmt.Fprintf(&b, "      - id: %s\n        name: %s\n", m, ollamaModelDisplayName(m))
		}
	} else {
		b.WriteString("    models: []\n")
	}

	b.WriteString("\n")

	// Append non-model imported config (channels, etc.)
	if imported != nil {
		importedOverlay := TranslateToOverlayYAML(imported)
		// Strip the openclaw: and models: sections — we already wrote those above.
		// Keep only channel config and other non-provider settings.
		lines := strings.Split(importedOverlay, "\n")

		var kept []string

		skip := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Skip openclaw: block (agentModel) and models: block (providers)
			if trimmed == "openclaw:" || trimmed == "models:" {
				skip = true
				continue
			}
			// Stop skipping when we hit a new top-level key
			if skip && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				skip = false
			}

			if !skip {
				kept = append(kept, line)
			}
		}

		extra := strings.TrimSpace(strings.Join(kept, "\n"))
		if extra != "" {
			b.WriteString("# Imported from ~/.openclaw/openclaw.json\n")
			b.WriteString(extra)
			b.WriteString("\n\n")
		}
	}

	b.WriteString(`# eRPC integration
erpc:
  url: http://erpc.erpc.svc.cluster.local/rpc

# Remote-signer wallet for Ethereum transaction signing.
# The remote-signer runs in the same namespace as OpenClaw.
extraEnv:
  - name: REMOTE_SIGNER_URL
    value: http://remote-signer:9000
`)

	if agentBaseURL != "" {
		fmt.Fprintf(&b, `  - name: AGENT_BASE_URL
    value: %s
`, agentBaseURL)
	}

	b.WriteString(`
# Skills: injected directly to the host-side PVC path at
# $DATA_DIR/openclaw-<id>/openclaw-data/.openclaw/skills/
# OpenClaw's file watcher picks them up; no ConfigMap needed.
skills:
  enabled: false

# Agent init Job (enable to bootstrap workspace on first deploy)
initJob:
  enabled: false
`)

	if useExternalSecrets {
		b.WriteString(`
# Load instance-local credentials (provider/channel tokens) from a dedicated Secret
secrets:
  extraEnvFromSecrets:
    - ` + userSecretsK8sSecretRef + `
`)
	}

	return b.String()
}

// patchHeartbeatConfig reads the rendered openclaw-config ConfigMap, injects
// heartbeat configuration from values-obol.yaml, and re-applies it. This
// compensates for the upstream Helm chart not rendering agents.defaults.heartbeat.
func patchHeartbeatConfig(cfg *config.Config, id, deploymentDir string) {
	// Read values-obol.yaml to check for heartbeat config.
	valuesPath := filepath.Join(deploymentDir, "values-obol.yaml")

	valuesRaw, err := os.ReadFile(valuesPath)
	if err != nil {
		return // No values file, nothing to patch.
	}

	// Quick check: if no heartbeat in values, skip.
	if !strings.Contains(string(valuesRaw), "heartbeat:") {
		return
	}

	// Extract heartbeat every/target from YAML (simple parsing, not full YAML).
	var every, target string

	for line := range strings.SplitSeq(string(valuesRaw), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "every:"); ok {
			every = strings.TrimSpace(after)
			every = strings.Trim(every, "\"'")
		}

		if after, ok := strings.CutPrefix(trimmed, "target:"); ok {
			target = strings.TrimSpace(after)
			target = strings.Trim(target, "\"'")
		}
	}

	if every == "" {
		return // No heartbeat interval configured.
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	// Read current ConfigMap.
	getCmd := exec.Command(kubectlBinary, "get", "configmap", "openclaw-config",
		"-n", namespace, "-o", "jsonpath={.data.openclaw\\.json}")

	getCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	var out bytes.Buffer

	getCmd.Stdout = &out
	if err := getCmd.Run(); err != nil {
		fmt.Printf("Warning: could not read openclaw-config ConfigMap: %v\n", err)
		return
	}

	// Parse JSON config.
	var cfgJSON map[string]any
	if err := json.Unmarshal(out.Bytes(), &cfgJSON); err != nil {
		fmt.Printf("Warning: could not parse openclaw.json: %v\n", err)
		return
	}

	// Navigate to agents.defaults, inject heartbeat.
	agents, ok := cfgJSON["agents"].(map[string]any)
	if !ok {
		agents = map[string]any{}
		cfgJSON["agents"] = agents
	}

	defaults, ok := agents["defaults"].(map[string]any)
	if !ok {
		defaults = map[string]any{}
		agents["defaults"] = defaults
	}

	heartbeat := map[string]any{
		"every": every,
	}
	if target != "" {
		heartbeat["target"] = target
	}

	defaults["heartbeat"] = heartbeat

	// Re-serialize.
	patched, err := json.MarshalIndent(cfgJSON, "    ", "  ")
	if err != nil {
		fmt.Printf("Warning: could not marshal patched config: %v\n", err)
		return
	}

	// Apply via kubectl apply --server-side with Helm's field manager so that
	// subsequent helm upgrade doesn't conflict on data.openclaw.json.
	applyPayload := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "openclaw-config",
			"namespace": namespace,
		},
		"data": map[string]string{
			"openclaw.json": string(patched),
		},
	}
	applyRaw, _ := json.Marshal(applyPayload) //nolint:errchkjson // map[string]any is safe, keys/values are controlled

	applyCmd := exec.Command(kubectlBinary, "apply", "-f", "-",
		"--server-side", "--field-manager=helm", "--force-conflicts")

	applyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	applyCmd.Stdin = bytes.NewReader(applyRaw)

	var applyErr bytes.Buffer

	applyCmd.Stderr = &applyErr
	if err := applyCmd.Run(); err != nil {
		fmt.Printf("Warning: could not patch heartbeat config: %v\n%s\n", err, applyErr.String())
		return
	}

	fmt.Printf("✓ Heartbeat config injected (every: %s, target: %s)\n", every, target)
}

// ollamaEndpoint returns the base URL where host Ollama should be reachable.
// It respects the OLLAMA_HOST environment variable, falling back to http://localhost:11434.
func ollamaEndpoint() string {
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		// OLLAMA_HOST may be just "host:port" or a full URL.
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}

		return strings.TrimRight(host, "/")
	}

	return "http://localhost:11434"
}

// detectOllama checks whether Ollama is reachable on the host machine by
// hitting the /api/tags endpoint with a short timeout. Returns true if the
// server responds with HTTP 200.
func detectOllama() bool {
	endpoint := ollamaEndpoint()

	tagsURL, err := url.JoinPath(endpoint, "api", "tags")
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(tagsURL)
	if err != nil {
		return false
	}

	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// listOllamaModels queries the local Ollama server for pulled models.
// Returns nil if Ollama is not reachable, empty slice if reachable but no models pulled.
func listOllamaModels() []string {
	endpoint := ollamaEndpoint()

	tagsURL, err := url.JoinPath(endpoint, "api", "tags")
	if err != nil {
		return nil
	}

	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(tagsURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		name := strings.TrimSuffix(m.Name, ":latest")
		names = append(names, name)
	}

	return names
}

// preferredOllamaModel picks the best default model from available Ollama models.
// Models arrive in LiteLLM config order (set by `obol model prefer`), so the
// first model is the user's preferred choice. Falls back to a hardcoded
// preference list only for initial setup when no preference has been set.
func preferredOllamaModel(models []string) string {
	if len(models) > 0 {
		return models[0]
	}

	return ""
}

// ollamaModelDisplayName converts an Ollama model name (e.g. "llama3.2:3b")
// into a human-friendly display name (e.g. "Llama3.2 3b").
func ollamaModelDisplayName(name string) string {
	parts := strings.SplitN(name, ":", 2)

	display := parts[0]
	if len(display) > 0 {
		display = strings.ToUpper(display[:1]) + display[1:]
	}

	if len(parts) > 1 {
		display += " " + parts[1]
	}

	return display
}

// interactiveSetup prompts the user for provider configuration.
// If imported is non-nil, offers to use the detected config.
// Returns the ImportResult for overlay generation, and optionally a CloudProviderInfo
// when a cloud provider was selected (so the caller can configure LiteLLM).
func interactiveSetup(cfg *config.Config, imported *ImportResult) (*ImportResult, *CloudProviderInfo, error) {
	reader := bufio.NewReader(os.Stdin)

	if imported != nil {
		fmt.Print("\nUse detected configuration? [Y/n]: ")

		line, _ := reader.ReadString('\n')

		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" || line == "y" || line == "yes" {
			fmt.Println("Using detected configuration.")
			return imported, nil, nil
		}
	}

	// Detect Ollama on the host to decide whether to offer it as an option
	ollamaAvailable := detectOllama()
	if ollamaAvailable {
		fmt.Printf("  ✓ Ollama detected at %s\n", ollamaEndpoint())
	} else {
		fmt.Printf("  ⚠ Ollama not detected on host (%s)\n", ollamaEndpoint())
	}

	if ollamaAvailable {
		fmt.Println("\nSelect a model provider:")
		fmt.Println("  [1] Local Ollama via the Obol model gateway (default)")
		fmt.Println("  [2] Anthropic API key via the Obol model gateway")
		fmt.Println("  [3] OpenAI API key via the Obol model gateway")
		fmt.Println("  [4] Direct Anthropic API key to the Openclaw gateway")
		fmt.Println("  [5] Direct OpenAI API key to Openclaw gateway")
		fmt.Println("  [6] Custom OpenAI-compatible endpoint to the Openclaw gateway")
		fmt.Print("\nChoice [1]: ")

		line, _ := reader.ReadString('\n')

		choice := strings.TrimSpace(line)
		if choice == "" {
			choice = "1"
		}

		switch choice {
		case "1":
			fmt.Println("Using global Ollama route via LiteLLM.")
			return nil, nil, nil
		case "2":
			cloud, err := promptForCloudProvider(reader, "anthropic", "Anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6")
			if err != nil {
				return nil, nil, err
			}

			result := buildLiteLLMRoutedOverlay(cfg, cloud)

			return result, cloud, nil
		case "3":
			cloud, err := promptForCloudProvider(reader, "openai", "OpenAI", "gpt-5.2", "GPT-5.2")
			if err != nil {
				return nil, nil, err
			}

			result := buildLiteLLMRoutedOverlay(cfg, cloud)

			return result, cloud, nil
		case "4":
			result, err := promptForDirectProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com", "anthropic-messages", envAnthropicAPIKey, "claude-sonnet-4-6", "Claude Sonnet 4.6")
			if err != nil {
				return nil, nil, err
			}

			return result, nil, nil
		case "5":
			result, err := promptForDirectProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "openai-completions", envOpenAIAPIKey, "gpt-5.2", "GPT-5.2")
			if err != nil {
				return nil, nil, err
			}

			return result, nil, nil
		case "6":
			result, err := promptForCustomProvider(reader)
			if err != nil {
				return nil, nil, err
			}

			return result, nil, nil
		default:
			fmt.Printf("Unknown choice '%s', using global Ollama route.\n", choice)
			return nil, nil, nil
		}
	}

	// Ollama not available — offer cloud/global and direct overrides
	fmt.Println("\nSelect a remote model provider:")
	fmt.Println("  [1] Anthropic API key via the Obol model gateway")
	fmt.Println("  [2] OpenAI API key via the Obol model gateway")
	fmt.Println("  [3] Direct Anthropic API key to the Openclaw gateway")
	fmt.Println("  [4] Direct OpenAI API key to Openclaw gateway")
	fmt.Println("  [5] Custom OpenAI-compatible endpoint to the Openclaw gateway")
	fmt.Print("\nChoice [1]: ")

	line, _ := reader.ReadString('\n')

	choice := strings.TrimSpace(line)
	if choice == "" {
		choice = "1"
	}

	switch choice {
	case "1":
		cloud, err := promptForCloudProvider(reader, "anthropic", "Anthropic", "claude-sonnet-4-6", "Claude Sonnet 4.6")
		if err != nil {
			return nil, nil, err
		}

		result := buildLiteLLMRoutedOverlay(cfg, cloud)

		return result, cloud, nil
	case "2":
		cloud, err := promptForCloudProvider(reader, "openai", "OpenAI", "gpt-5.2", "GPT-5.2")
		if err != nil {
			return nil, nil, err
		}

		result := buildLiteLLMRoutedOverlay(cfg, cloud)

		return result, cloud, nil
	case "3":
		result, err := promptForDirectProvider(reader, "anthropic", "Anthropic", "https://api.anthropic.com", "anthropic-messages", envAnthropicAPIKey, "claude-sonnet-4-6", "Claude Sonnet 4.6")
		if err != nil {
			return nil, nil, err
		}

		return result, nil, nil
	case "4":
		result, err := promptForDirectProvider(reader, "openai", "OpenAI", "https://api.openai.com/v1", "openai-completions", envOpenAIAPIKey, "gpt-5.2", "GPT-5.2")
		if err != nil {
			return nil, nil, err
		}

		return result, nil, nil
	case "5":
		result, err := promptForCustomProvider(reader)
		if err != nil {
			return nil, nil, err
		}

		return result, nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown choice '%s'; please select a valid model provider", choice)
	}
}

// promptForCloudProvider asks for an API key and returns cloud provider info.
// The actual overlay (ImportResult) is built separately via buildLiteLLMRoutedOverlay.
func promptForCloudProvider(reader *bufio.Reader, name, display, modelID, modelName string) (*CloudProviderInfo, error) {
	fmt.Printf("\n%s API key: ", display)

	apiKey, _ := reader.ReadString('\n')

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	return &CloudProviderInfo{
		Name:    name,
		APIKey:  apiKey,
		ModelID: modelID,
		Display: modelName,
	}, nil
}

// promptForDirectProvider asks for direct-provider settings for an instance-local override.
func promptForDirectProvider(reader *bufio.Reader, providerName, display, defaultBaseURL, defaultAPI, defaultAPIKeyEnvVar, defaultModelID, defaultModelName string) (*ImportResult, error) {
	fmt.Printf("\n%s API key (instance-local): ", display)

	apiKey, _ := reader.ReadString('\n')

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key is required", display)
	}

	fmt.Printf("%s model ID [%s]: ", display, defaultModelID)

	modelID, _ := reader.ReadString('\n')

	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = defaultModelID
	}

	fmt.Printf("%s model display name [%s]: ", display, defaultModelName)

	modelName, _ := reader.ReadString('\n')

	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = defaultModelName
	}

	fmt.Printf("%s base URL [%s]: ", display, defaultBaseURL)

	baseURL, _ := reader.ReadString('\n')

	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return buildDirectProviderOverlay(providerName, baseURL, defaultAPI, defaultAPIKeyEnvVar, modelID, modelName, apiKey), nil
}

// promptForCustomProvider asks for an OpenAI-compatible custom endpoint override.
func promptForCustomProvider(reader *bufio.Reader) (*ImportResult, error) {
	fmt.Printf("\nCustom base URL (OpenAI-compatible, e.g. https://example.com/v1): ")

	baseURL, _ := reader.ReadString('\n')

	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("custom base URL is required")
	}

	fmt.Printf("Custom model ID: ")

	modelID, _ := reader.ReadString('\n')

	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, errors.New("custom model ID is required")
	}

	fmt.Printf("Custom model display name [%s]: ", modelID)

	modelName, _ := reader.ReadString('\n')

	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = modelID
	}

	fmt.Printf("Custom API type [openai-completions]: ")

	apiType, _ := reader.ReadString('\n')

	apiType = strings.TrimSpace(apiType)
	if apiType == "" {
		apiType = "openai-completions"
	}

	fmt.Printf("API key env var [OPENAI_API_KEY]: ")

	apiKeyEnvVar, _ := reader.ReadString('\n')

	apiKeyEnvVar = strings.TrimSpace(apiKeyEnvVar)
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = envOpenAIAPIKey
	}

	fmt.Printf("API key (optional, leave empty to configure later): ")

	apiKey, _ := reader.ReadString('\n')

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		fmt.Println("  Note: no API key provided; set it later via the OpenClaw user secret.")
	}

	// Custom endpoints use the "openai" slot because the Helm chart only iterates
	// a hardcoded provider list (ollama, anthropic, openai). Any other name would
	// be silently dropped. OpenAI-compatible is the most generic fit.
	return buildDirectProviderOverlay("openai", baseURL, apiType, apiKeyEnvVar, modelID, modelName, apiKey), nil
}

// buildLiteLLMRoutedOverlay creates an ImportResult that routes a cloud model
// through the LiteLLM proxy. Uses "openai" provider slot with api: openai-completions.
// LiteLLM handles upstream routing based on the model name.
func buildLiteLLMRoutedOverlay(cfg *config.Config, cloud *CloudProviderInfo) *ImportResult {
	return &ImportResult{
		AgentModel: "openai/" + cloud.ModelID,
		Providers: []ImportedProvider{
			{
				Name:         "openai",
				BaseURL:      "http://litellm.llm.svc.cluster.local:4000/v1",
				API:          "openai-completions",
				APIKeyEnvVar: envOpenAIAPIKey,
				APIKey:       litellmMasterKey(cfg),
				Models: []ImportedModel{
					{ID: cloud.ModelID, Name: cloud.Display},
				},
			},
			{Name: "anthropic", Disabled: true},
			{Name: "ollama", Disabled: true},
		},
	}
}

// buildDirectProviderOverlay creates an instance-local direct provider configuration.
// Provider name must be one of anthropic/openai/ollama due current chart constraints.
func buildDirectProviderOverlay(providerName, baseURL, api, apiKeyEnvVar, modelID, modelName, apiKey string) *ImportResult {
	var agentPrefix string

	switch providerName {
	case model.ProviderAnthropic:
		agentPrefix = model.ProviderAnthropic
	case model.ProviderOpenAI:
		agentPrefix = model.ProviderOpenAI
	default:
		agentPrefix = providerName
	}

	providers := []ImportedProvider{
		{Name: model.ProviderAnthropic, Disabled: providerName != model.ProviderAnthropic},
		{Name: model.ProviderOpenAI, Disabled: providerName != model.ProviderOpenAI},
		{Name: model.ProviderOllama, Disabled: providerName != model.ProviderOllama},
	}
	for i := range providers {
		if providers[i].Name != providerName {
			continue
		}

		providers[i].Disabled = false
		providers[i].BaseURL = baseURL
		providers[i].API = api
		providers[i].APIKeyEnvVar = apiKeyEnvVar
		providers[i].APIKey = apiKey
		providers[i].Models = []ImportedModel{{ID: modelID, Name: modelName}}
	}

	return &ImportResult{
		AgentModel: agentPrefix + "/" + modelID,
		Providers:  providers,
	}
}

// collectSensitiveData extracts literal secrets from imported config and strips
// them from the in-memory overlay data so values-obol.yaml does not persist them.
// NOTE: This mutates imported in-place (zeroes APIKey/BotToken fields). The caller
// must call this BEFORE generating the overlay YAML.
func collectSensitiveData(imported *ImportResult) map[string]string {
	if imported == nil {
		return nil
	}

	secretData := make(map[string]string)

	for i := range imported.Providers {
		p := &imported.Providers[i]
		if p.APIKey == "" {
			continue
		}

		envVar := p.APIKeyEnvVar
		if envVar == "" {
			envVar = defaultProviderAPIKeyEnvVar(p.Name)
			p.APIKeyEnvVar = envVar
		}

		secretData[envVar] = p.APIKey
		p.APIKey = ""
	}

	if imported.Channels.Telegram != nil && imported.Channels.Telegram.BotToken != "" {
		secretData["TELEGRAM_BOT_TOKEN"] = imported.Channels.Telegram.BotToken
		imported.Channels.Telegram.BotToken = ""
	}

	if imported.Channels.Discord != nil && imported.Channels.Discord.BotToken != "" {
		secretData["DISCORD_BOT_TOKEN"] = imported.Channels.Discord.BotToken
		imported.Channels.Discord.BotToken = ""
	}

	if imported.Channels.Slack != nil {
		if imported.Channels.Slack.BotToken != "" {
			secretData["SLACK_BOT_TOKEN"] = imported.Channels.Slack.BotToken
			imported.Channels.Slack.BotToken = ""
		}

		if imported.Channels.Slack.AppToken != "" {
			secretData["SLACK_APP_TOKEN"] = imported.Channels.Slack.AppToken
			imported.Channels.Slack.AppToken = ""
		}
	}

	if len(secretData) == 0 {
		return nil
	}

	return secretData
}

// generateHelmfile creates a helmfile.yaml referencing the published
// obol/openclaw and obol/remote-signer charts in the same namespace.
func generateHelmfile(id, namespace string) string {
	return fmt.Sprintf(`# OpenClaw instance: %s
# Managed by obol openclaw

repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/

releases:
  - name: openclaw
    namespace: %s
    createNamespace: true
    chart: obol/openclaw
    version: %s
    values:
      - values-obol.yaml

  - name: remote-signer
    namespace: %s
    chart: obol/remote-signer
    version: %s
    values:
      - values-remote-signer.yaml
`, id, namespace, chartVersion, namespace, remoteSignerChartVersion)
}

// collectAllHostnames gathers all openclaw subdomain hostnames that should be
// in /etc/hosts. Scans existing deployments and includes the new hostname.
func collectAllHostnames(cfg *config.Config, newHostname string) []string {
	hostnames := []string{newHostname}
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return hostnames
	}

	seen := map[string]bool{newHostname: true}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		h := fmt.Sprintf("openclaw-%s.%s", e.Name(), defaultDomain)
		if !seen[h] {
			hostnames = append(hostnames, h)
			seen[h] = true
		}
	}

	return hostnames
}
