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

	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
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
	ollamaHost := ollamaHostForBackend(backendName)
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if err := embed.CopyDefaults(defaultsDir, map[string]string{
		"{{OLLAMA_HOST}}": ollamaHost,
	}); err != nil {
		return fmt.Errorf("failed to copy defaults: %w", err)
	}

	// Store stack ID
	if err := os.WriteFile(stackIDPath, []byte(stackID), 0644); err != nil {
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

// Up starts the cluster using the configured backend
func Up(cfg *config.Config, u *ui.UI) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, run 'obol stack init' first")
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
	if err := os.WriteFile(kubeconfigPath, kubeconfigData, 0600); err != nil {
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
	u.Print("Try setting up an agent with `obol agent init` next.")
	update.HintIfStale(cfg)
	return nil
}

// Down stops the cluster and the DNS resolver container.
func Down(cfg *config.Config, u *ui.UI) error {
	stackID := getStackID(cfg)
	if stackID == "" {
		return fmt.Errorf("stack ID not found, stack may not be initialized")
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

	// Remove data directory only if force flag is set
	if force {
		err := u.RunWithSpinner("Removing data directory", func() error {
			rmCmd := exec.Command("sudo", "rm", "-rf", cfg.DataDir)
			return rmCmd.Run()
		})
		if err != nil {
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
		fmt.Sprintf("STACK_DATA_DIR=%s", dataDir),
	)

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

	// Deploy default OpenClaw instance (non-fatal on failure)
	if err := u.RunWithSpinner("Setting up default OpenClaw instance", func() error {
		return openclaw.SetupDefault(cfg, u)
	}); err != nil {
		u.Warnf("Failed to set up default OpenClaw: %v", err)
		u.Dim("  You can manually set up OpenClaw later with: obol openclaw onboard")
	}

	return nil
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
