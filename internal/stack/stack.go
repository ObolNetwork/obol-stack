package stack

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agent"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/dns"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/update"
	petname "github.com/dustinkirkland/golang-petname"
)

const (
	kubeconfigFile = "kubeconfig.yaml"
	stackIDFile    = ".stack-id"
)

// Init initializes the stack configuration
func Init(cfg *config.Config, u *ui.UI, force bool, backendName string) error {
	// Check if any stack config already exists
	stackIDPath := filepath.Join(cfg.ConfigDir, stackIDFile)
	backendFilePath := filepath.Join(cfg.ConfigDir, stackBackendFile)

	hasExistingConfig := false
	if _, err := os.Stat(stackIDPath); err == nil {
		hasExistingConfig = true
	}

	if _, err := os.Stat(backendFilePath); err == nil {
		hasExistingConfig = true
	}
	// Also check legacy k3d.yaml for backward compatibility
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, k3dConfigFile)); err == nil {
		hasExistingConfig = true
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

	// Copy embedded defaults (helmfile + charts for infrastructure)
	// Resolve {{OLLAMA_HOST}} based on backend:
	// - k3d (Docker): host.docker.internal (macOS) or host.k3d.internal (Linux)
	// - k3s (bare-metal): 127.0.0.1 (k3s runs directly on the host)
	// Resolve {{OLLAMA_HOST_IP}} to a numeric IP for the Endpoints object:
	// - Endpoints require an IP, not a hostname (ClusterIP+Endpoints pattern)
	ollamaHost := ollamaHostForBackend(backendName)

	ollamaHostIP, err := ollamaHostIPForBackend(backendName)
	if err != nil {
		return fmt.Errorf("failed to resolve Ollama host IP: %w", err)
	}

	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if err := embed.CopyDefaults(defaultsDir, map[string]string{
		"{{OLLAMA_HOST}}":    ollamaHost,
		"{{OLLAMA_HOST_IP}}": ollamaHostIP,
		"{{CLUSTER_ID}}":     stackID,
	}); err != nil {
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

// ollamaHostForBackend returns the hostname/IP that reaches the host Ollama
// instance from inside the cluster.
func ollamaHostForBackend(backendName string) string {
	if backendName == BackendK3s {
		return "127.0.0.1"
	}

	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}

	return "host.k3d.internal"
}

// ollamaHostIPForBackend resolves the Ollama host to an IP address.
// ClusterIP+Endpoints requires an IP (not a hostname).
//
// Resolution strategy:
//  1. If already an IP (k3s: 127.0.0.1), return as-is
//  2. Try host-side DNS resolution
//  3. macOS: use Docker Desktop VM gateway (192.168.65.254)
//  4. Linux: fall back to docker0 bridge interface IP
func ollamaHostIPForBackend(backendName string) (string, error) {
	host := ollamaHostForBackend(backendName)

	// If already an IP, return as-is (k3s: 127.0.0.1)
	if net.ParseIP(host) != nil {
		return host, nil
	}

	// Try host-side DNS resolution first.
	addrs, err := net.LookupHost(host)
	if err == nil && len(addrs) > 0 {
		return addrs[0], nil
	}

	// macOS Docker Desktop: host.docker.internal is only resolvable inside
	// containers (Docker injects it via DNS), not on the host. Use the
	// well-known VM gateway IP that Docker Desktop exposes to containers.
	if runtime.GOOS == "darwin" && backendName == BackendK3d {
		return dockerDesktopGatewayIP(), nil
	}

	// Linux fallback: docker0 bridge interface IP (reachable from all containers).
	if runtime.GOOS == "linux" && backendName == BackendK3d {
		ip, bridgeErr := dockerBridgeGatewayIP()
		if bridgeErr == nil {
			return ip, nil
		}

		return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w; docker0 fallback also failed: %w", host, err, bridgeErr)
	}

	return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w\n\tEnsure Docker Desktop is running", host, err)
}

// dockerDesktopGatewayIP returns the Docker Desktop VM gateway IP.
// On macOS, Docker Desktop runs a LinuxKit VM. The host is reachable from
// containers at this well-known gateway address (192.168.65.254 maps to
// host.docker.internal inside the VM). This has been stable across Docker
// Desktop versions since the transition from HyperKit to Apple Virtualization.
func dockerDesktopGatewayIP() string {
	return "192.168.65.254"
}

// dockerBridgeGatewayIP returns the IPv4 address of the docker0 network interface.
// On Linux, docker0 is the default Docker bridge (typically 172.17.0.1). This IP
// is reachable from any Docker container regardless of the container's network,
// because the host has this address on its network stack and Docker enables
// IP forwarding between bridge networks and the host.
func dockerBridgeGatewayIP() (string, error) {
	iface, err := net.InterfaceByName("docker0")
	if err != nil {
		return "", fmt.Errorf("docker0 interface not found: %w", err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("cannot get docker0 addresses: %w", err)
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			return ipNet.IP.String(), nil
		}
	}

	return "", errors.New("no IPv4 address found on docker0 interface")
}

// Up starts the cluster using the configured backend
func Up(cfg *config.Config, u *ui.UI) error {
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

	// Sync defaults with backend-aware dataDir
	dataDir := backend.DataDir(cfg)
	if err := syncDefaults(cfg, u, kubeconfigPath, dataDir); err != nil {
		return err
	}

	// Ensure DNS resolver is running for wildcard *.obol.stack
	if err := dns.EnsureRunning(); err != nil {
		u.Warnf("DNS resolver failed to start: %v", err)
	} else if err := dns.ConfigureSystemResolver(); err != nil {
		u.Warnf("Failed to configure system DNS resolver: %v", err)
	} else {
		u.Success("DNS resolver configured")
	}

	u.Blank()
	u.Bold("Stack started successfully.")
	u.Print("Visit http://obol.stack in your browser to get started.")
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

	// Compatibility migration
	if err := migrateDefaultsHTTPRouteHostnames(helmfilePath); err != nil {
		u.Warnf("Failed to migrate defaults helmfile hostnames: %v", err)
	}

	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"sync",
	)
	helmfileCmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		"STACK_DATA_DIR="+dataDir,
	)

	// In development mode, build and import local Docker images that aren't
	// on a public registry yet (e.g. x402-verifier and the LiteLLM custom image).
	// This must happen before helmfile sync so pods do not try to pull tags that
	// only exist in the local k3d image store.
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		buildAndImportLocalImages(cfg)
	}

	if err := u.Exec(ui.ExecConfig{
		Name: "Deploying default infrastructure",
		Cmd:  helmfileCmd,
	}); err != nil {
		u.Warn("Helmfile sync failed, stopping cluster")

		if downErr := Down(cfg, u); downErr != nil {
			u.Warnf("Failed to stop cluster during cleanup: %v", downErr)
		}

		return fmt.Errorf("failed to apply defaults helmfile: %w", err)
	}

	u.Success("Default infrastructure deployed")

	// Auto-configure LiteLLM with Ollama models and any cloud providers
	// whose API keys are found in the environment. This ensures the
	// inference path works out of the box — no separate `obol model setup`
	// step required. Non-fatal: the user can always run `obol model setup` later.
	autoConfigureLLM(cfg, u)

	// Deploy default OpenClaw instance (non-fatal on failure).
	// Not wrapped in RunWithSpinner because SetupDefault/Onboard produce their
	// own UI output (Info, Detail, Print) and run sub-spinners via u.Exec.
	// An outer spinner would fight with that output and block any sudo password
	// prompt (e.g. EnsureHostsEntries writing /etc/hosts).
	u.Blank()
	u.Info("Setting up default OpenClaw instance")

	if err := openclaw.SetupDefault(cfg, u); err != nil {
		u.Warnf("Failed to set up default OpenClaw: %v", err)
		u.Dim("  You can manually set up OpenClaw later with: obol openclaw onboard")
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
	if err == nil && len(ollamaModels) > 0 && !model.HasConfiguredModels(cfg) {
		u.Blank()
		u.Infof("Ollama detected with %d model(s)", len(ollamaModels))

		var names []string

		for _, m := range ollamaModels {
			name := m.Name
			if before, ok := strings.CutSuffix(name, ":latest"); ok {
				name = before
			}

			names = append(names, name)
		}

		if err := model.PatchLiteLLMProvider(cfg, u, "ollama", "", names); err != nil {
			u.Warnf("Auto-configure Ollama failed: %v", err)
		} else {
			configured = append(configured, "ollama")
		}
	}

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
	dockerfile string // relative to project root, e.g. "Dockerfile.x402-verifier"
}

// localImages lists images that should be built locally and imported into k3d.
var localImages = []localImage{
	{tag: "ghcr.io/obolnetwork/x402-verifier:latest", dockerfile: "Dockerfile.x402-verifier"},
	{tag: "ghcr.io/obolnetwork/x402-buyer:latest", dockerfile: "Dockerfile.x402-buyer"},
}

// buildAndImportLocalImages builds Docker images from source and imports them
// into the k3d cluster. This ensures images are available even when the GHCR
// publish workflow hasn't run. Non-fatal: logs warnings on failure.
func buildAndImportLocalImages(cfg *config.Config) {
	stackID := getStackID(cfg)
	if stackID == "" {
		return
	}

	// Find the project root (where go.mod lives).
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		fmt.Println("Warning: could not find project root, skipping local image build")
		return
	}

	clusterName := "obol-stack-" + stackID
	k3dBinary := filepath.Join(cfg.BinDir, "k3d")

	for _, img := range localImages {
		contextDir := projectRoot

		dockerfilePath := filepath.Join(projectRoot, img.dockerfile)
		if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
			continue // Dockerfile not present (production install without source)
		}

		fmt.Printf("Building %s from %s...\n", img.tag, img.dockerfile)
		buildCmd := exec.Command("docker", "build",
			"-f", dockerfilePath,
			"-t", img.tag,
			contextDir,
		)
		buildCmd.Stdout = os.Stdout

		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Printf("Warning: failed to build %s: %v\n", img.tag, err)
			continue
		}

		fmt.Printf("Importing %s into cluster %s...\n", img.tag, clusterName)
		importCmd := exec.Command(k3dBinary, "image", "import", img.tag, "-c", clusterName)
		importCmd.Stdout = os.Stdout

		importCmd.Stderr = os.Stderr
		if err := importCmd.Run(); err != nil {
			fmt.Printf("Warning: failed to import %s into k3d: %v\n", img.tag, err)
		}
	}
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

// checkPortsAvailable verifies that all required ports can be bound.
func checkPortsAvailable(ports []int) error {
	var blocked []int

	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			if strings.Contains(err.Error(), "permission denied") {
				continue
			}

			blocked = append(blocked, port)

			continue
		}

		ln.Close()
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
