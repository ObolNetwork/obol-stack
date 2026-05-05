package stack

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agent"
	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	stackdefaults "github.com/ObolNetwork/obol-stack/internal/defaults"
	"github.com/ObolNetwork/obol-stack/internal/dns"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/update"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	petname "github.com/dustinkirkland/golang-petname"
	"gopkg.in/yaml.v3"
)

const (
	kubeconfigFile = "kubeconfig.yaml"
	stackIDFile    = ".stack-id"
)

// Init initializes the stack configuration
func Init(cfg *config.Config, u *ui.UI, force bool, backendName string) error {
	// Check if any stack config already exists (legacy k3d.yaml included).
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	hasExistingConfig := false
	for _, f := range []string{stackIDFile, stackBackendFile, k3dConfigFile} {
		if _, err := os.Stat(filepath.Join(cfg.ConfigDir, f)); err == nil {
			hasExistingConfig = true
			break
		}
	}

	if hasExistingConfig && !force {
		return fmt.Errorf("stack configuration already exists at %s\nUse --force to overwrite", cfg.ConfigDir)
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Check if stack ID already exists (preserve on --force)
	var stackID string
	if existingID, err := os.ReadFile(stackIDPath); err == nil {
		stackID = strings.TrimSpace(string(existingID))
		u.Warnf("Preserving existing stack ID: %s (use purge to reset)", stackID)
	} else {
		stackID = petname.Generate(2, "-")
	}

	// Default to k3d if no backend specified
	if backendName == "" {
		backendName = BackendK3d
	}

	// If switching backends, destroy the old one first to prevent
	// orphaned clusters (e.g., k3d containers still running after
	// switching to k3s, or k3s process still alive after switching to k3d).
	if hasExistingConfig && force {
		destroyOldBackendIfSwitching(cfg, u, backendName, stackID)
	}

	backend, err := NewBackend(backendName)
	if err != nil {
		return err
	}

	u.Info("Initializing cluster configuration")
	u.Detail("Cluster ID", stackID)
	u.Detail("Backend", backend.Name())

	// Check prerequisites
	if err := backend.Prerequisites(cfg); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// Generate backend-specific config
	if err := backend.Init(cfg, u, stackID); err != nil {
		return err
	}

	// Copy embedded defaults (helmfile + charts for infrastructure).
	if err := stackdefaults.CopyInfrastructure(cfg, backendName, stackID); err != nil {
		return fmt.Errorf("failed to copy defaults: %w", err)
	}

	// Store stack ID
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0o600); err != nil { //nolint:gosec // G703: path from user's local config dir
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	// Save backend choice
	if err := SaveBackend(cfg, backendName); err != nil {
		return fmt.Errorf("failed to save backend choice: %w", err)
	}

	u.Success("Stack initialized")

	return nil
}

// destroyOldBackendIfSwitching checks if the backend is changing and tears down
// the old one to prevent orphaned clusters running side by side.
func destroyOldBackendIfSwitching(cfg *config.Config, u *ui.UI, newBackend, stackID string) {
	oldBackend, err := LoadBackend(cfg)
	if err != nil {
		return
	}

	if oldBackend.Name() == newBackend {
		return // same backend, nothing to clean up
	}

	u.Warnf("Switching backend from %s to %s — destroying old cluster", oldBackend.Name(), newBackend)

	// Destroy the old backend's cluster (best-effort, don't block init)
	if stackID != "" {
		if err := oldBackend.Destroy(cfg, u, stackID); err != nil {
			u.Warnf("Failed to destroy old %s cluster: %v", oldBackend.Name(), err)
		}
	}

	// Clean up stale config files from the old backend
	cleanupStaleBackendConfigs(cfg, oldBackend.Name())
}

// cleanupStaleBackendConfigs removes config files belonging to the old backend
// that would otherwise linger and confuse detection.
func cleanupStaleBackendConfigs(cfg *config.Config, oldBackend string) {
	var staleFiles []string

	switch oldBackend {
	case BackendK3d:
		staleFiles = []string{k3dConfigFile}
	case BackendK3s:
		staleFiles = []string{k3sConfigFile, k3sPidFile, k3sLogFile}
	}

	for _, f := range staleFiles {
		path := filepath.Join(cfg.ConfigDir, f)
		if _, err := os.Stat(path); err == nil {
			os.Remove(path)
		}
	}
}

func ollamaHostForBackend(backendName string) string {
	return stackdefaults.OllamaHostForBackend(backendName)
}

func ollamaHostIPForBackend(backendName string) (string, error) {
	return stackdefaults.OllamaHostIPForBackend(backendName)
}

func dockerDesktopGatewayIP() string {
	return stackdefaults.DockerDesktopGatewayIP()
}

func dockerBridgeGatewayIP() (string, error) {
	return stackdefaults.DockerBridgeGatewayIP()
}

func bridgeInterfaceIP(name string) (string, error) {
	return stackdefaults.BridgeInterfaceIP(name)
}

// Up starts the cluster using the configured backend
func Up(cfg *config.Config, u *ui.UI, wildcardDNS bool) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return errors.New("stack ID not found, run 'obol stack init' first")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

	u.Infof("Starting stack (id: %s, backend: %s)", stackID, backend.Name())

	kubeconfigData, err := backend.Up(cfg, u, stackID)
	if err != nil {
		return err
	}

	// Write kubeconfig
	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0o600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	if refreshed, err := stackdefaults.RefreshInfrastructureIfChanged(cfg, backend.Name(), stackID); err != nil {
		return fmt.Errorf("failed to refresh default infrastructure templates: %w", err)
	} else if refreshed {
		u.Dim("Refreshed default infrastructure templates from embedded assets")
	}

	// Ensure the base host before syncing defaults. Include existing agent
	// hostnames so stack up never shrinks the managed /etc/hosts block to only
	// obol.stack when default setup is skipped.
	if err := dns.EnsureHostsEntries(agentruntime.CollectHostnames(cfg)); err != nil {
		u.Warnf("Could not update /etc/hosts for obol.stack: %v", err)
	}

	// Sync defaults with backend-aware dataDir
	dataDir := backend.DataDir(cfg)
	if err := syncDefaults(cfg, u, kubeconfigPath, dataDir); err != nil {
		return err
	}

	// Wildcard *.obol.stack DNS is opt-in (--wildcard-dns) because it
	// modifies system DNS config (NetworkManager/resolv.conf on Linux,
	// /etc/resolver on macOS) which can break host DNS resolution.
	if wildcardDNS {
		if err := dns.EnsureRunning(); err != nil {
			u.Warnf("DNS resolver failed to start: %v", err)
		} else if err := dns.ConfigureSystemResolver(); err != nil {
			u.Warnf("Wildcard DNS configuration failed: %v", err)
		} else {
			u.Success("Wildcard DNS configured (*.obol.stack)")
		}
	}

	u.Blank()
	u.Bold("Stack started successfully.")
	ingressURL := LocalIngressURL(cfg)
	if ingressURL != "http://obol.stack" {
		u.Warnf("Default ingress ports are in use by another process — use %s instead", ingressURL)
	}
	u.Printf("Visit %s in your browser to get started.", ingressURL)
	update.HintIfStale(cfg)

	return nil
}

// Down stops the cluster and the DNS resolver container.
func Down(cfg *config.Config, u *ui.UI) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return errors.New("stack ID not found, stack may not be initialized")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	// Cluster delete invalidates any active quick tunnel URL. Warn first so
	// users with sellers registered against the URL aren't blindsided.
	currentURL, _ := tunnel.GetTunnelURL(cfg)
	if !tunnel.ConfirmQuickTunnelLoss(cfg, u, currentURL, "obol stack down") {
		u.Info("Aborted.")

		return nil
	}

	// Stop the DNS resolver container
	dns.Stop()

	return backend.Down(cfg, u, stackID)
}

// Purge deletes the cluster config and optionally data
func Purge(cfg *config.Config, u *ui.UI, force bool) error {
	// When --force is set, data dir will be deleted — offer wallet backup.
	if force {
		openclaw.PromptBackupBeforePurge(cfg, u)
	}

	stackID := getStackID(cfg)

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	// Destroy cluster if we have a stack ID
	if stackID != "" {
		u.Infof("Destroying cluster (id: %s)", stackID)

		if err := backend.Destroy(cfg, u, stackID); err != nil {
			u.Warnf("Failed to destroy cluster (may already be deleted): %v", err)
		}
	}

	// In dev mode, reclaim any leaked k3d-obol-stack-* networks. The
	// pull-through registry mirrors hold the network open after
	// `k3d cluster delete`, which would otherwise silently exhaust
	// Docker's predefined CIDR pool over repeated dev cycles.
	reclaimLeakedDevK3dNetworks(u)

	// Stop DNS resolver and remove system resolver config
	dns.Stop()
	dns.RemoveSystemResolver()

	// Remove stack config directory
	if err := os.RemoveAll(cfg.ConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}

	u.Success("Removed cluster config")

	// Remove data directory only if force flag is set.
	// Uses Exec instead of RunWithSpinner because sudo may prompt for a password,
	// which requires an interactive terminal (stdin connected).
	if force {
		rmCmd := exec.Command("sudo", "rm", "-rf", cfg.DataDir)

		rmCmd.Stdin = os.Stdin
		if err := u.Exec(ui.ExecConfig{
			Name:        "Removing data directory",
			Cmd:         rmCmd,
			Interactive: true,
		}); err != nil {
			return fmt.Errorf("failed to remove data directory: %w", err)
		}

		u.Blank()
		u.Bold("Cluster fully purged (binaries preserved)")
	} else {
		u.Success("Cluster purged (config removed, data preserved)")
		u.Printf("  To delete persistent data: sudo rm -rf %s", cfg.DataDir)
		u.Print("  Or use 'obol stack purge --force' to remove everything")
	}

	return nil
}

// getStackID reads the stored stack ID
func getStackID(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)

	data, err := os.ReadFile(stackIDPath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// GetStackID reads the stored stack ID (exported for use in main)
func GetStackID(cfg *config.Config) string {
	return getStackID(cfg)
}

// syncDefaults deploys the default infrastructure using helmfile
// If deployment fails, the cluster is automatically stopped via Down()
func syncDefaults(cfg *config.Config, u *ui.UI, kubeconfigPath string, dataDir string) error {
	defaultsHelmfilePath := filepath.Join(cfg.ConfigDir, "defaults")
	helmfilePath := filepath.Join(defaultsHelmfilePath, "helmfile.yaml")

	previousLiteLLMConfig, err := preserveLiteLLMConfigForHelm(cfg, kubeconfigPath)
	if err != nil {
		u.Warnf("Failed to preserve LiteLLM config across Helm sync: %v", err)
	}

	// Compatibility migration
	if err := migrateDefaultsHTTPRouteHostnames(helmfilePath); err != nil {
		u.Warnf("Failed to migrate defaults helmfile hostnames: %v", err)
	}

	helmfileArgs := []string{
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"sync",
	}
	helmfileArgs = append(helmfileArgs, helmcmd.SyncFlagsForVersion(filepath.Join(cfg.BinDir, "helm"))...)
	helmfileCmd := exec.Command(filepath.Join(cfg.BinDir, "helmfile"), helmfileArgs...)
	helmfileCmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"STACK_DATA_DIR="+dataDir,
	)

	// In development mode, build and import local repo images that aren't on a
	// public registry yet. Third-party images use the k3d registry-mirror path
	// configured during cluster creation.
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		buildAndImportLocalImages(cfg, u)
	}

	if err := u.Exec(ui.ExecConfig{
		Name: "Deploying default infrastructure",
		Cmd:  helmfileCmd,
	}); err != nil {
		u.Warn("Helmfile sync failed, stopping cluster")

		if previousLiteLLMConfig != "" {
			if restoreErr := restoreLiteLLMConfig(cfg, kubeconfigPath, previousLiteLLMConfig); restoreErr != nil {
				u.Warnf("Failed to restore LiteLLM config after Helmfile error: %v", restoreErr)
			}
		}

		if downErr := Down(cfg, u); downErr != nil {
			u.Warnf("Failed to stop cluster during cleanup: %v", downErr)
		}

		return fmt.Errorf("failed to apply defaults helmfile: %w", err)
	}

	u.Success("Default infrastructure deployed")

	if previousLiteLLMConfig != "" {
		if err := restoreLiteLLMConfig(cfg, kubeconfigPath, previousLiteLLMConfig); err != nil {
			u.Warnf("Failed to restore LiteLLM config after base migration: %v", err)
		}
	}

	// Populate the x402-verifier CA bundle from the host so TLS verification of
	// the facilitator works without needing to run `obol sell pricing` first.
	// Non-fatal: best-effort, the user can repopulate by running `obol sell pricing`.
	x402verifier.PopulateCABundle(cfg)

	// Auto-configure LiteLLM with Ollama models and any cloud providers
	// whose API keys are found in the environment. This ensures the
	// inference path works out of the box — no separate `obol model setup`
	// step required. Non-fatal: the user can always run `obol model setup` later.
	autoConfigureLLM(cfg, u)

	// Deploy default Hermes instance (non-fatal on failure).
	// Not wrapped in RunWithSpinner because SetupDefault/Onboard produce their
	// own UI output (Info, Detail, Print) and run sub-spinners via u.Exec.
	// An outer spinner would fight with that output and block any sudo password
	// prompt (e.g. EnsureHostsEntries writing /etc/hosts).
	u.Blank()
	u.Info("Setting up default Hermes instance")

	if err := hermes.SetupDefault(cfg, u); err != nil {
		u.Warnf("Failed to set up default Hermes: %v", err)
		u.Dim("  You can manually set up Hermes later with: obol agent new --runtime hermes")
	} else if walletAddr, walletErr := hermes.ResolveWalletAddress(cfg); walletErr == nil {
		u.Blank()
		u.Successf("Default agent wallet: %s", walletAddr)
		u.Dim("  Fund this wallet for x402 buying or direct on-chain registration.")
		u.Dim("  Retrieve later with: obol agent wallet list obol-agent")
	}

	// Apply agent capabilities (RBAC + heartbeat) to the default instance.
	// Non-fatal: the user can always run `obol agent init` later.
	u.Blank()
	u.Info("Applying agent capabilities")

	if err := agent.Init(cfg, u); err != nil {
		u.Warnf("Failed to apply agent capabilities: %v", err)
		u.Dim("  You can manually apply later with: obol agent init")
	}

	// Start the Cloudflare tunnel only if a persistent DNS tunnel is provisioned.
	// Quick tunnels are dormant by default and activate on first `obol sell`.
	u.Blank()

	if st, _ := tunnel.LoadTunnelState(cfg); st != nil && st.Mode == "dns" && st.Hostname != "" {
		u.Info("Starting persistent Cloudflare tunnel")

		if tunnelURL, err := tunnel.EnsureRunning(cfg, u); err != nil {
			u.Warnf("Tunnel not started: %v", err)
			u.Dim("  Start manually with: obol tunnel restart")
		} else {
			u.Successf("Tunnel active: %s", tunnelURL)
		}
	} else {
		u.Dim("Tunnel dormant (activates on first 'obol sell http')")
		u.Dim("  Start manually with: obol tunnel restart")
		u.Dim("  For a persistent URL: obol tunnel login --hostname stack.example.com")
	}

	return nil
}

// autoConfigureLLM detects host Ollama and imported cloud providers, then
// auto-configures LiteLLM so inference works out of the box.
// Patches all providers first, then does a single restart.
func autoConfigureLLM(cfg *config.Config, u *ui.UI) {
	var configured []string // provider names that were patched

	// --- Ollama ---
	ollamaModels, err := model.ListOllamaModels()
	switch {
	case err != nil:
		// Ollama unreachable. Not auto-configurable here; obolup.sh covers
		// install hints, and `obol model setup` covers the manual path.
	case len(ollamaModels) == 0:
		// Ollama is up but the user hasn't pulled anything yet — the agent
		// will boot with no local model to chat with. Warn loudly with a
		// concrete next step instead of silently moving on.
		u.Blank()
		u.Warn("Ollama is running but has no models pulled.")
		u.Dim("  Hermes will not have a local chat model until one is pulled.")
		u.Dim("  Quick fix:  ollama pull " + model.PreferredDefaultOllamaModel)
		u.Dim("  Or pick interactively:  obol model pull")
	case !model.HasConfiguredModels(cfg):
		u.Blank()
		u.Infof("Ollama detected with %d model(s)", len(ollamaModels))

		names := model.AutoConfigOllamaModelNames(ollamaModels)
		if len(names) > 0 {
			if err := model.PatchLiteLLMProvider(cfg, u, "ollama", "", names); err != nil {
				u.Warnf("Auto-configure Ollama failed: %v", err)
			} else {
				configured = append(configured, "ollama")
				// Auto-pick lands on names[0] via Rank(); flag the case
				// where every model needs unconfigured credentials so the
				// operator isn't surprised by an unusable default. The
				// PreferredDefaultOllamaModel bump in AutoConfigOllamaModelNames
				// already handles the typical "many local models" case.
				if model.IsCredentialRequiringOllamaModel(names[0]) {
					u.Warnf("Auto-picked default %q is an Ollama cloud alias that needs an API key.", names[0])
					u.Dim("  Pull a local model to take over as default: ollama pull " + model.PreferredDefaultOllamaModel)
				}
			}
		}
	}

	configured = append(configured, autoDetectLocalProviders(cfg, u)...)

	// --- Cloud provider from ~/.openclaw ---
	if cloudProvider := autoDetectCloudProvider(cfg, u); cloudProvider != "" {
		configured = append(configured, cloudProvider)
	}

	// --- Single restart for all providers ---
	if len(configured) > 0 {
		label := strings.Join(configured, " + ")
		if err := model.RestartLiteLLM(cfg, u, label); err != nil {
			u.Warnf("LiteLLM restart failed: %v", err)
			u.Dim("  Run 'obol model setup' to configure manually.")
		}
	}
}

// autoDetectLocalProviders scans well-known local inference ports
// (vLLM, llama.cpp server, LM Studio, sglang, mlx-lm, ...) and registers
// each discovered server's models with LiteLLM without restarting. Ollama
// and LiteLLM itself are skipped (handled by the Ollama branch above and
// would be circular respectively). Returns the list of registered
// provider labels for inclusion in the single-restart summary line.
//
// Set OBOL_DISABLE_LOCAL_MODEL_DISCOVERY=true to skip this step entirely.
func autoDetectLocalProviders(cfg *config.Config, u *ui.UI) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	discovered, err := model.DiscoverLocalProviders(ctx)
	if err != nil {
		u.Dim(fmt.Sprintf("Local inference discovery skipped: %v", err))
		return nil
	}
	if len(discovered) == 0 {
		return nil
	}

	// Batch all discovered entries into a single ConfigMap PATCH so we
	// avoid N read-modify-write round-trips (and the TOCTOU window
	// between them) when more than one local server is detected.
	var (
		labels  []string
		entries []model.ModelEntry
	)
	for _, p := range discovered {
		u.Blank()
		u.Infof("Detected %s at %s with %d model(s)", p.ServerType, p.HostEndpoint, len(p.Entries))
		entries = append(entries, p.Entries...)
		labels = append(labels, p.Label)
	}

	if err := model.PatchLiteLLMEntries(cfg, u, entries); err != nil {
		u.Warnf("Auto-configure local providers failed: %v", err)
		return nil
	}
	return labels
}

// autoDetectCloudProvider reads ~/.openclaw config, resolves the cloud
// provider API key, and patches LiteLLM (without restart). Returns the
// provider name on success, or "" if nothing was configured.
func autoDetectCloudProvider(cfg *config.Config, u *ui.UI) string {
	imported, err := openclaw.DetectExistingConfig()
	if err != nil || imported == nil {
		return ""
	}

	agentModel := imported.AgentModel
	if agentModel == "" {
		return ""
	}

	// Extract provider and model name from "anthropic/claude-sonnet-4-6".
	provider, modelName := "", agentModel
	if before, after, ok := strings.Cut(agentModel, "/"); ok {
		provider = before
		modelName = after
	}

	if provider == "" {
		provider = model.ProviderFromModelName(agentModel)
	}

	if provider == "" || provider == "ollama" {
		return ""
	}

	// Already configured — skip.
	if model.HasProviderConfigured(cfg, provider) {
		return ""
	}

	// Resolve API key: try primary + alt env vars, then .env in dev mode.
	apiKey, envVarUsed := model.ResolveAPIKey(provider)
	if apiKey == "" && os.Getenv("OBOL_DEVELOPMENT") == "true" {
		envVar := model.ProviderEnvVar(provider)
		dotEnv := model.LoadDotEnv(filepath.Join(".", ".env"))

		apiKey = dotEnv[envVar]
		if apiKey != "" {
			envVarUsed = envVar + " (.env)"
		}
	}

	if apiKey == "" {
		u.Blank()

		primaryEnv := model.ProviderEnvVar(provider)
		u.Warnf("Agent model %s detected but %s is not set", agentModel, primaryEnv)
		u.Dim(fmt.Sprintf("  Set it in your environment: export %s=...", primaryEnv))
		u.Dim("  Or configure after startup: obol model setup --provider " + provider)

		return ""
	}

	u.Blank()

	if envVarUsed != model.ProviderEnvVar(provider) {
		u.Infof("Cloud model %s detected via %s — configuring %s provider", agentModel, envVarUsed, provider)
	} else {
		u.Infof("Cloud model %s detected — configuring %s provider", agentModel, provider)
	}

	if err := model.PatchLiteLLMProvider(cfg, u, provider, apiKey, []string{modelName}); err != nil {
		u.Warnf("Auto-configure %s failed: %v", provider, err)
		u.Dim(fmt.Sprintf("  Run 'obol model setup --provider %s' to configure manually.", provider))

		return ""
	}

	return provider
}

// localImage describes a Docker image built from source in this repo.
type localImage struct {
	tag        string // e.g. "ghcr.io/obolnetwork/x402-verifier:latest"
	dockerfile string // relative to project root or absolute path
	contextDir string // relative to project root or absolute path (empty = project root)
}

// localImages lists images that we build from source in this repo and import
// into k3d. Hermes is NOT here — it has no Obol-side customization, so it's
// pulled directly from `nousresearch/hermes-agent` like any other upstream
// image. Override the tag with OBOL_HERMES_IMAGE if needed.
var baseLocalImages = []localImage{
	{tag: "ghcr.io/obolnetwork/x402-verifier:latest", dockerfile: "Dockerfile.x402-verifier"},
	{tag: "ghcr.io/obolnetwork/serviceoffer-controller:latest", dockerfile: "Dockerfile.serviceoffer-controller"},
	{tag: "ghcr.io/obolnetwork/x402-buyer:latest", dockerfile: "Dockerfile.x402-buyer"},
	{tag: "ghcr.io/obolnetwork/demo-server:latest", dockerfile: "Dockerfile.demo-server"},
	{tag: "ghcr.io/obolnetwork/obol-stack-public-storefront:latest", dockerfile: "Dockerfile.public-storefront"},
}

func devPreloadImages() []string {
	var images []string
	if ref := openclaw.ImageRef(); ref != "" {
		images = append(images, ref)
	}
	if ref := hermes.ImageRef(); ref != "" {
		images = append(images, ref)
	}
	return images
}

func reuseLocalDevImages() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES")), "true")
}

func dockerImageAvailableLocally(tag string) bool {
	inspectCmd := exec.Command("docker", "image", "inspect", tag)
	inspectCmd.Stdout = io.Discard
	inspectCmd.Stderr = io.Discard
	return inspectCmd.Run() == nil
}

// dockerImageDigest returns the local image's content ID (e.g.
// "sha256:abcd…") for use as a cache key. Empty string on any failure;
// callers must treat that as "do not cache".
func dockerImageDigest(tag string) string {
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// k3dServerContainerID returns the docker container ID of the k3d cluster's
// server-0 node. Used as the second half of the import-cache key so that a
// destroy+recreate of the cluster (new container ID, fresh containerd state)
// invalidates cache entries from the previous incarnation. Empty string on
// any failure.
func k3dServerContainerID(clusterName string) string {
	out, err := exec.Command("docker", "inspect", "--format", "{{.Id}}", "k3d-"+clusterName+"-server-0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

const importedImagesCacheFile = ".imported-images.json"

type importedImageEntry struct {
	Digest     string `json:"digest"`
	ClusterCID string `json:"cluster_cid"`
}

type importedImageCache struct {
	Entries map[string]importedImageEntry `json:"entries"`
}

func loadImportedImageCache(cfg *config.Config) importedImageCache {
	cache := importedImageCache{Entries: map[string]importedImageEntry{}}
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, importedImagesCacheFile))
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil || cache.Entries == nil {
		return importedImageCache{Entries: map[string]importedImageEntry{}}
	}
	return cache
}

func saveImportedImageCache(cfg *config.Config, cache importedImageCache) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(cfg.ConfigDir, importedImagesCacheFile), data, 0o600)
}

func importedImageCacheKey(clusterName, tag string) string {
	return clusterName + "|" + tag
}

// importImageWithCache wraps `k3d image import` with a digest+cluster cache so
// repeated `obol stack up` runs skip the multi-second tarball round-trip when
// the same image was already loaded into the same cluster instance. Returns
// true if the image was actually imported (so the caller can count it for the
// summary), false if the cache hit short-circuited the import or the import
// failed.
func importImageWithCache(k3dBinary, clusterName, tag, serverCID string, cache *importedImageCache, u *ui.UI) bool {
	digest := dockerImageDigest(tag)
	key := importedImageCacheKey(clusterName, tag)
	if digest != "" && serverCID != "" {
		if entry, ok := cache.Entries[key]; ok && entry.Digest == digest && entry.ClusterCID == serverCID {
			return false
		}
	}

	if u != nil {
		u.Infof("Importing %s into cluster %s", tag, clusterName)
	}
	if err := importImageToCluster(k3dBinary, clusterName, tag); err != nil {
		if u != nil {
			u.Warnf("Failed to import %s into k3d: %v", tag, err)
		}
		return false
	}

	if digest != "" && serverCID != "" {
		cache.Entries[key] = importedImageEntry{Digest: digest, ClusterCID: serverCID}
	}
	return true
}

// buildAndImportLocalImages builds Docker images from source and imports them
// into the k3d cluster. This ensures images are available even when the GHCR
// publish workflow hasn't run. Non-fatal: logs warnings on failure.
//
// Output policy: stay quiet on the warm path. When every image is already
// built and already loaded into the running cluster (the common case after
// our reuse/cache fixes), we emit a single "  ✓ Local dev images ready"
// summary line so the surrounding spinner-flanked output stays clean. Only
// real work — actual `docker build`, `docker pull`, and tarball imports —
// gets per-image lines, and those go through the UI for consistent styling.
func buildAndImportLocalImages(cfg *config.Config, u *ui.UI) {
	start := time.Now()

	stackID := getStackID(cfg)
	if stackID == "" {
		return
	}

	// Find the project root (where go.mod lives).
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		if u != nil {
			u.Warn("Could not find project root, skipping local image build")
		}
		return
	}

	clusterName := "obol-stack-" + stackID
	k3dBinary := filepath.Join(cfg.BinDir, "k3d")
	reuseCachedImages := reuseLocalDevImages()
	serverCID := k3dServerContainerID(clusterName)
	cache := loadImportedImageCache(cfg)

	var built, pulled, imported, total int

	for _, img := range baseLocalImages {
		contextDir := projectRoot
		if img.contextDir != "" {
			if filepath.IsAbs(img.contextDir) {
				contextDir = img.contextDir
			} else {
				contextDir = filepath.Join(projectRoot, img.contextDir)
			}
		}

		dockerfilePath := img.dockerfile
		if !filepath.IsAbs(dockerfilePath) {
			dockerfilePath = filepath.Join(projectRoot, img.dockerfile)
		}
		if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
			continue // Dockerfile not present (production install without source)
		}

		total++

		if !(reuseCachedImages && dockerImageAvailableLocally(img.tag)) {
			if u != nil {
				u.Infof("Building %s from %s", img.tag, img.dockerfile)
			}
			buildCmd := exec.Command("docker", "build",
				"-f", dockerfilePath,
				"-t", img.tag,
				contextDir,
			)
			buildCmd.Stdout = os.Stdout
			buildCmd.Stderr = os.Stderr
			if err := buildCmd.Run(); err != nil {
				if u != nil {
					u.Warnf("Failed to build %s: %v", img.tag, err)
				}
				continue
			}
			built++
		}

		if importImageWithCache(k3dBinary, clusterName, img.tag, serverCID, &cache, u) {
			imported++
		}
	}

	for _, ref := range devPreloadImages() {
		total++

		if !(reuseCachedImages && dockerImageAvailableLocally(ref)) {
			if u != nil {
				u.Infof("Pulling %s", ref)
			}
			pullCmd := exec.Command("docker", "pull", ref)
			pullCmd.Stdout = os.Stdout
			pullCmd.Stderr = os.Stderr
			if err := pullCmd.Run(); err != nil {
				if u != nil {
					u.Warnf("Failed to pull %s: %v", ref, err)
				}
				continue
			}
			pulled++
		}

		if importImageWithCache(k3dBinary, clusterName, ref, serverCID, &cache, u) {
			imported++
		}
	}

	saveImportedImageCache(cfg, cache)

	if u != nil && total > 0 {
		cached := total - built - pulled
		elapsed := time.Since(start).Round(time.Second)
		switch {
		case built == 0 && pulled == 0 && imported == 0:
			u.Successf("Local dev images ready (%d cached) (%s)", total, elapsed)
		case built == 0 && pulled == 0:
			u.Successf("Local dev images ready (%d imported, %d cached) (%s)", imported, cached, elapsed)
		default:
			u.Successf("Local dev images ready (%d built, %d pulled, %d imported, %d cached) (%s)",
				built, pulled, imported, cached, elapsed)
		}
	}
}

func importImageToCluster(k3dBinary, clusterName, tag string) error {
	importCmd := exec.Command(k3dBinary, "image", "import", tag, "-c", clusterName)
	importCmd.Stdout = os.Stdout
	importCmd.Stderr = os.Stderr

	return importCmd.Run()
}

// findProjectRoot walks up from the current directory to find go.mod.
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
}

// LocalIngressURL returns the best local HTTP base URL for the current stack.
// For k3d, it prefers the first host port mapped to container port 80 in the
// generated k3d config. For historical/default setups it falls back to
// http://obol.stack or http://obol.stack:8080.
func LocalIngressURL(cfg *config.Config) string {
	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)
	if data, err := os.ReadFile(k3dConfigPath); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- port:") {
				continue
			}

			portSpec := strings.TrimSpace(strings.TrimPrefix(line, "- port:"))
			parts := strings.Split(portSpec, ":")
			if len(parts) != 2 || parts[1] != "80" {
				continue
			}

			if parts[0] == "80" {
				return "http://obol.stack"
			}
			return fmt.Sprintf("http://obol.stack:%s", parts[0])
		}
	}

	if checkPortsAvailable([]int{80}) == nil {
		return "http://obol.stack"
	}

	return "http://obol.stack:8080"
}

// checkPortsAvailable verifies that all required ports can be bound.
func checkPortsAvailable(ports []int) error {
	var blocked []int

	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			continue
		}

		if strings.Contains(err.Error(), "permission denied") {
			// On Linux, binding privileged ports (< 1024) without root always
			// returns "permission denied" — even when the port is free. We
			// can't tell "free but needs root" from "occupied" via bind alone.
			// Fall back to /proc/net/tcp{,6} which is readable without root.
			if runtime.GOOS == "linux" && isPortOccupiedProc(port) {
				blocked = append(blocked, port)
			}
			continue
		}

		blocked = append(blocked, port)
	}

	if len(blocked) > 0 {
		return fmt.Errorf(
			"port(s) %s already in use — "+
				"Obol Stack needs these ports for HTTP/HTTPS access; "+
				"find what's using them with: sudo lsof -i :%d, "+
				"then stop the conflicting service and retry 'obol stack up'",
			formatPorts(blocked), blocked[0],
		)
	}

	return nil
}

// isPortOccupiedProc checks whether a TCP port has a listener by scanning
// /proc/net/tcp and /proc/net/tcp6. This works without root and is the only
// reliable way to detect occupancy of privileged ports on Linux where
// net.Listen returns "permission denied" regardless of whether the port is free.
func isPortOccupiedProc(port int) bool {
	hexPort := fmt.Sprintf("%04X", port)
	for _, path := range []string{"/proc/net/tcp6", "/proc/net/tcp"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		found := false
		scanner := bufio.NewScanner(f)
		scanner.Scan() // skip header line
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			// /proc/net/tcp fields: sl local_address rem_address st ...
			// state 0A = TCP_LISTEN; local_address = hexIP:hexPort
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			parts := strings.SplitN(fields[1], ":", 2)
			if len(parts) == 2 && strings.EqualFold(parts[1], hexPort) {
				found = true
				break
			}
		}
		f.Close()

		if found {
			return true
		}
	}
	return false
}

func formatPorts(ports []int) string {
	strs := make([]string, len(ports))
	for i, p := range ports {
		strs[i] = strconv.Itoa(p)
	}

	return strings.Join(strs, ", ")
}

func migrateDefaultsHTTPRouteHostnames(helmfilePath string) error {
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return err
	}

	needle := "              hostnames:\n                - obol.stack\n"

	s := string(data)
	if !strings.Contains(s, needle) {
		return nil
	}

	updated := strings.ReplaceAll(s, needle, "")
	if updated == s {
		return nil
	}

	return os.WriteFile(helmfilePath, []byte(updated), 0o600) //nolint:gosec // G703: path from user's local config dir
}

// preserveLiteLLMConfigForHelm snapshots the LiteLLM config.yaml ConfigMap
// before helmfile sync re-templates it. The base chart only knows the
// `paid/*` catch-all route, so without this snapshot every `obol stack up`
// would wipe user-added cloud providers, Ollama models, and custom
// endpoints. The snapshot is merged back in by restoreLiteLLMConfig after
// helmfile sync completes.
func preserveLiteLLMConfigForHelm(cfg *config.Config, kubeconfigPath string) (string, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", "litellm-config", "-n", "llm", "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil || strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return raw, nil
}

func restoreLiteLLMConfig(cfg *config.Config, kubeconfigPath, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	if current, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", "litellm-config", "-n", "llm", "-o", "jsonpath={.data.config\\.yaml}"); err == nil && strings.TrimSpace(current) != "" {
		merged, err := mergeLiteLLMConfig(current, raw)
		if err != nil {
			return err
		}
		raw = merged
	}

	manifest := configMapFieldOwnershipManifest("litellm-config", "llm", "config.yaml", raw)

	return kubectl.ApplyServerSideForceConflicts(kubectlBinary, kubeconfigPath, manifest, "helm")
}

func mergeLiteLLMConfig(currentRaw, previousRaw string) (string, error) {
	var current model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(currentRaw), &current); err != nil {
		return "", fmt.Errorf("parse current LiteLLM config: %w", err)
	}

	var previous model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(previousRaw), &previous); err != nil {
		return "", fmt.Errorf("parse previous LiteLLM config: %w", err)
	}

	byName := make(map[string]int, len(current.ModelList))
	for i, entry := range current.ModelList {
		byName[entry.ModelName] = i
	}

	for _, entry := range previous.ModelList {
		if strings.TrimSpace(entry.ModelName) == "" {
			continue
		}
		if _, ok := byName[entry.ModelName]; ok {
			continue
		}
		byName[entry.ModelName] = len(current.ModelList)
		current.ModelList = append(current.ModelList, entry)
	}

	if len(current.GeneralSettings) == 0 && len(previous.GeneralSettings) > 0 {
		current.GeneralSettings = previous.GeneralSettings
	}
	if len(current.LiteLLMSettings) == 0 && len(previous.LiteLLMSettings) > 0 {
		current.LiteLLMSettings = previous.LiteLLMSettings
	}

	merged, err := yaml.Marshal(&current)
	if err != nil {
		return "", fmt.Errorf("serialize merged LiteLLM config: %w", err)
	}

	return string(merged), nil
}

func configMapFieldOwnershipManifest(name, namespace, key, value string) []byte {
	var b strings.Builder

	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  %s: |\n", name, namespace, key)
	for _, line := range strings.Split(value, "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}

	return []byte(b.String())
}

// reclaimLeakedDevK3dNetworks force-disconnects pull-through registry-mirror
// containers from any orphaned `k3d-obol-stack-*` Docker networks and then
// removes the network. Only runs when OBOL_DEVELOPMENT=true, because the
// mirror containers (k3d-obol-{docker,ghcr,quay}-io.localhost) are only
// created in development mode and they're the reason `k3d cluster delete`
// can't free the network on a dev box.
//
// Each `k3d cluster create` reserves a /16 from Docker's predefined
// 172.16.0.0/12 pool (~16 networks). Without reclaiming these on purge,
// roughly sixteen dev cycles exhaust the pool and every subsequent
// cluster create fails with "all predefined address pools have been
// fully subnetted".
//
// Live clusters are detected by `*-server-N` or `*-serverlb` attachments
// and skipped, so this is safe to call alongside other running stacks.
// Mirror containers auto-rejoin the next cluster's network on the next
// `obol stack up`, so disconnecting them here is non-destructive for the
// cache.
func reclaimLeakedDevK3dNetworks(u *ui.UI) {
	if os.Getenv("OBOL_DEVELOPMENT") != "true" {
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}

	out, err := exec.Command("docker", "network", "ls",
		"--filter", "name=k3d-obol-stack-",
		"--format", "{{.Name}}").Output()
	if err != nil {
		return
	}

	networks := strings.Fields(strings.TrimSpace(string(out)))
	if len(networks) == 0 {
		return
	}

	reclaimed := 0
	for _, network := range networks {
		inspect, err := exec.Command("docker", "network", "inspect", network,
			"--format", `{{range .Containers}}{{.Name}}{{"\n"}}{{end}}`).Output()
		if err != nil {
			continue
		}
		attached := strings.Fields(strings.TrimSpace(string(inspect)))

		if hasLiveK3dCluster(attached) {
			continue
		}

		for _, container := range attached {
			_ = exec.Command("docker", "network", "disconnect", "-f", network, container).Run()
		}
		if err := exec.Command("docker", "network", "rm", network).Run(); err == nil {
			reclaimed++
		}
	}

	if reclaimed > 0 {
		u.Infof("Reclaimed %d leaked dev registry network(s)", reclaimed)
	}
}

// hasLiveK3dCluster returns true if any container name on the network
// looks like a k3d cluster node — `*-serverlb` or `*-server-<N>`.
func hasLiveK3dCluster(containers []string) bool {
	for _, c := range containers {
		if strings.HasSuffix(c, "-serverlb") {
			return true
		}
		if i := strings.LastIndex(c, "-server-"); i >= 0 {
			if _, err := strconv.Atoi(c[i+len("-server-"):]); err == nil {
				return true
			}
		}
	}
	return false
}
