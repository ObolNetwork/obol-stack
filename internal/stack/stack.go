package stack

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/dns"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/update"
	petname "github.com/dustinkirkland/golang-petname"
)

const (
	kubeconfigFile = "kubeconfig.yaml"
	stackIDFile    = ".stack-id"
)

// Init initializes the stack configuration
func Init(cfg *config.Config, force bool, backendName string) error {
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

	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create stack config dir: %w", err)
	}

	// Check if stack ID already exists (preserve on --force)
	var stackID string
	if existingID, err := os.ReadFile(stackIDPath); err == nil {
		stackID = strings.TrimSpace(string(existingID))
		fmt.Printf("Preserving existing stack ID: %s (use purge to reset)\n", stackID)
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
		destroyOldBackendIfSwitching(cfg, backendName, stackID)
	}

	backend, err := NewBackend(backendName)
	if err != nil {
		return err
	}

	fmt.Println("Initializing cluster configuration")
	fmt.Printf("Cluster ID: %s\n", stackID)
	fmt.Printf("Backend: %s\n", backend.Name())

	// Check prerequisites
	if err := backend.Prerequisites(cfg); err != nil {
		return fmt.Errorf("prerequisites check failed: %w", err)
	}

	// Generate backend-specific config
	if err := backend.Init(cfg, stackID); err != nil {
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
	}); err != nil {
		return fmt.Errorf("failed to copy defaults: %w", err)
	}
	fmt.Printf("Defaults copied to: %s\n", defaultsDir)

	// Store stack ID
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
		return fmt.Errorf("failed to write stack ID: %w", err)
	}

	// Save backend choice
	if err := SaveBackend(cfg, backendName); err != nil {
		return fmt.Errorf("failed to save backend choice: %w", err)
	}

	fmt.Printf("Initialized stack configuration\n")
	fmt.Printf("Stack ID: %s\n", stackID)
	return nil
}

// destroyOldBackendIfSwitching checks if the backend is changing and tears down
// the old one to prevent orphaned clusters running side by side.
func destroyOldBackendIfSwitching(cfg *config.Config, newBackend, stackID string) {
	oldBackend, err := LoadBackend(cfg)
	if err != nil {
		return
	}
	if oldBackend.Name() == newBackend {
		return // same backend, nothing to clean up
	}

	fmt.Printf("Switching backend from %s to %s — destroying old cluster\n", oldBackend.Name(), newBackend)

	// Destroy the old backend's cluster (best-effort, don't block init)
	if stackID != "" {
		if err := oldBackend.Destroy(cfg, stackID); err != nil {
			fmt.Printf("Warning: failed to destroy old %s cluster: %v\n", oldBackend.Name(), err)
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
		// k3s runs directly on the host — Ollama is at localhost
		return "127.0.0.1"
	}
	// k3d runs inside Docker containers
	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}
	return "host.k3d.internal"
}

// ollamaHostIPForBackend resolves the Ollama host to an IP address.
// ClusterIP+Endpoints requires an IP (not a hostname).
func ollamaHostIPForBackend(backendName string) (string, error) {
	host := ollamaHostForBackend(backendName)

	// If already an IP, return as-is (k3s: 127.0.0.1)
	if net.ParseIP(host) != nil {
		return host, nil
	}

	// Resolve hostname to IP
	addrs, err := net.LookupHost(host)
	if err != nil {
		return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w\nEnsure Docker Desktop is running (macOS) or the Docker daemon is configured with host networking (Linux)", host, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("Ollama host %q resolved to no addresses", host)
	}
	return addrs[0], nil
}

// Up starts the cluster using the configured backend
func Up(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, kubeconfigFile)

	fmt.Printf("Starting stack (id: %s, backend: %s)\n", stackID, backend.Name())

	kubeconfigData, err := backend.Up(cfg, stackID)
	if err != nil {
		return err
	}

	// Write kubeconfig (backend may have already written it, but ensure consistency)
	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	// Sync defaults with backend-aware dataDir
	dataDir := backend.DataDir(cfg)
	if err := syncDefaults(cfg, kubeconfigPath, dataDir); err != nil {
		return err
	}

	// Ensure DNS resolver is running for wildcard *.obol.stack
	if err := dns.EnsureRunning(); err != nil {
		fmt.Printf("Warning: DNS resolver failed to start: %v\n", err)
	} else if err := dns.ConfigureSystemResolver(); err != nil {
		fmt.Printf("Warning: failed to configure system DNS resolver: %v\n", err)
	}

	fmt.Printf("\nStack ID: %s\n", stackID)
	fmt.Printf("\nStack started successfully.\nVisit http://obol.stack in your browser to get started.\nTry setting up an agent with `obol agent init` next.\n")
	update.HintIfStale(cfg)
	return nil
}

// Down stops the cluster and the DNS resolver container.
func Down(cfg *config.Config) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	// Stop the DNS resolver container so it doesn't hold port 5553
	// across restarts and block subsequent obol stack up runs.
	dns.Stop()

	return backend.Down(cfg, stackID)
}

// Purge deletes the cluster config and optionally data
func Purge(cfg *config.Config, force bool) error {
	stackID := getStackID(cfg)

	backend, err := LoadBackend(cfg)
	if err != nil {
		return fmt.Errorf("failed to load backend: %w", err)
	}

	// Destroy cluster if we have a stack ID
	if stackID != "" {
		if force {
			fmt.Printf("Force destroying cluster (id: %s)\n", stackID)
		} else {
			fmt.Printf("Destroying cluster (id: %s)\n", stackID)
		}
		if err := backend.Destroy(cfg, stackID); err != nil {
			fmt.Printf("Failed to destroy cluster (may already be deleted): %v\n", err)
		}
	}

	// Stop DNS resolver and remove system resolver config
	dns.Stop()
	dns.RemoveSystemResolver()

	// Remove stack config directory
	if err := os.RemoveAll(cfg.ConfigDir); err != nil {
		return fmt.Errorf("failed to remove stack config: %w", err)
	}
	fmt.Println("Removed cluster config directory")

	// Remove data directory only if force flag is set
	if force {
		fmt.Println("Removing data directory...")
		rmCmd := exec.Command("sudo", "rm", "-rf", cfg.DataDir)
		rmCmd.Stdout = os.Stdout
		rmCmd.Stderr = os.Stderr
		if err := rmCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove data directory: %w", err)
		}
		fmt.Println("Removed data directory")
		fmt.Println("Cluster fully purged (binaries preserved)")
	} else {
		fmt.Println("Cluster purged (config removed, data preserved)")
		fmt.Printf("To delete persistent data: sudo rm -rf %s\n", cfg.DataDir)
		fmt.Println("Or use 'obol stack purge --force' to remove everything")
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
func syncDefaults(cfg *config.Config, kubeconfigPath string, dataDir string) error {
	fmt.Println("Deploying default infrastructure with helmfile")

	defaultsHelmfilePath := filepath.Join(cfg.ConfigDir, "defaults")
	helmfilePath := filepath.Join(defaultsHelmfilePath, "helmfile.yaml")

	// Compatibility migration: older defaults pinned HTTPRoutes to `obol.stack` via
	// `spec.hostnames`. This breaks public access for:
	// - quick tunnels (random *.trycloudflare.com host)
	// - user-provided DNS hostnames (e.g. agent.example.com)
	// Removing hostnames makes routes match all hostnames while preserving existing
	// path-based routing.
	if err := migrateDefaultsHTTPRouteHostnames(helmfilePath); err != nil {
		fmt.Printf("Warning: failed to migrate defaults helmfile hostnames: %v\n", err)
	}

	helmfileCmd := exec.Command(
		filepath.Join(cfg.BinDir, "helmfile"),
		"--file", helmfilePath,
		"--kubeconfig", kubeconfigPath,
		"sync",
	)
	helmfileCmd.Env = append(os.Environ(),
		"KUBECONFIG="+kubeconfigPath,
		fmt.Sprintf("STACK_DATA_DIR=%s", dataDir),
	)
	helmfileCmd.Stdout = os.Stdout
	helmfileCmd.Stderr = os.Stderr

	if err := helmfileCmd.Run(); err != nil {
		fmt.Println("Failed to apply defaults helmfile, stopping cluster")
		if downErr := Down(cfg); downErr != nil {
			fmt.Printf("Failed to stop cluster during cleanup: %v\n", downErr)
		}
		return fmt.Errorf("failed to apply defaults helmfile: %w", err)
	}

	fmt.Println("Default infrastructure deployed")

	// Deploy default OpenClaw instance (non-fatal on failure)
	fmt.Println("Setting up default OpenClaw instance...")
	if err := openclaw.SetupDefault(cfg); err != nil {
		fmt.Printf("Warning: failed to set up default OpenClaw: %v\n", err)
		fmt.Println("You can manually set up OpenClaw later with: obol openclaw up")
	}

	return nil
}

// checkPortsAvailable verifies that all required ports can be bound.
// Returns an actionable error if any port is already in use.
func checkPortsAvailable(ports []int) error {
	var blocked []int
	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			// Permission denied (ports < 1024 on Linux require root) means the
			// port is available but we can't bind as non-root — not a conflict.
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
			"port(s) %s already in use\n\n"+
				"Obol Stack needs these ports for HTTP/HTTPS access.\n"+
				"Find what's using them with:\n"+
				"  sudo lsof -i :%d\n\n"+
				"Then stop the conflicting service and retry 'obol stack up'.",
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

	// Only removes the legacy default single-hostname block; if users customized their
	// helmfile with different hostnames, we leave it alone.
	needle := "              hostnames:\n                - obol.stack\n"
	s := string(data)
	if !strings.Contains(s, needle) {
		return nil
	}
	updated := strings.ReplaceAll(s, needle, "")
	if updated == s {
		return nil
	}
	return os.WriteFile(helmfilePath, []byte(updated), 0644)
}
