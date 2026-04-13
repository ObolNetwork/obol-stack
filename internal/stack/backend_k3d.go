package stack

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// tlsInsecureSkipVerify returns a TLS config that skips certificate verification.
// Used only for health-checking the local k3s API server which uses a self-signed cert.
func tlsInsecureSkipVerify() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local k3s health check only
}

const (
	k3dConfigFile = "k3d.yaml"
)

// K3dBackend manages clusters via k3d (k3s inside Docker containers)
type K3dBackend struct{}

func (b *K3dBackend) Name() string { return BackendK3d }

func (b *K3dBackend) Prerequisites(cfg *config.Config) error {
	// Check Docker is running
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil

	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return errors.New("docker is not running; k3d backend requires Docker — start Docker and try again")
	}

	// Check k3d binary exists
	k3dPath := filepath.Join(cfg.BinDir, "k3d")
	if _, err := os.Stat(k3dPath); os.IsNotExist(err) {
		return fmt.Errorf("k3d not found at %s\nRun obolup.sh to install dependencies", k3dPath)
	}

	return nil
}

func (b *K3dBackend) Init(cfg *config.Config, u *ui.UI, stackID string) error {
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for data directory: %w", err)
	}

	absConfigDir, err := filepath.Abs(cfg.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for config directory: %w", err)
	}

	// Template k3d config with actual values
	k3dConfig := embed.K3dConfig
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{STACK_ID}}", stackID)
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{DATA_DIR}}", absDataDir)
	k3dConfig = strings.ReplaceAll(k3dConfig, "{{CONFIG_DIR}}", absConfigDir)

	// Strip port mappings for occupied host ports so k3d cluster create won't
	// fail.  The fallback mappings (8080→80, 8443→443) are always preserved.
	k3dConfig = stripConflictingPorts(k3dConfig, u)

	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)
	if err := os.WriteFile(k3dConfigPath, []byte(k3dConfig), 0o600); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	return nil
}

func (b *K3dBackend) IsRunning(cfg *config.Config, stackID string) (bool, error) {
	stackName := "obol-stack-" + stackID
	listCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")

	output, err := listCmd.Output()
	if err != nil {
		return false, fmt.Errorf("k3d list command failed: %w", err)
	}

	return strings.Contains(string(output), stackName), nil
}

func (b *K3dBackend) Up(cfg *config.Config, u *ui.UI, stackID string) ([]byte, error) {
	stackName := "obol-stack-" + stackID
	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)
	var registrySetup *devRegistrySetup

	running, err := b.IsRunning(cfg, stackID)
	if err != nil {
		return nil, err
	}

	if running {
		u.Warn("Cluster already exists, starting it")

		startCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "start", stackName)
		if err := u.Exec(ui.ExecConfig{
			Name: "Starting existing k3d cluster",
			Cmd:  startCmd,
		}); err != nil {
			return nil, fmt.Errorf("failed to start existing cluster: %w", err)
		}
	} else {
		// Create data directory if it doesn't exist
		absDataDir, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for data directory: %w", err)
		}

		if err := os.MkdirAll(absDataDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %w", err)
		}

		// Re-check port availability — port state may have changed since
		// 'obol stack init' wrote the k3d config.
		ensureK3dPortsAvailable(k3dConfigPath, u)

		if os.Getenv("OBOL_DEVELOPMENT") == "true" {
			setup, setupErr := ensureDevRegistries(cfg, u)
			if setupErr != nil {
				u.Warnf("Dev registry cache unavailable, falling back to direct upstream pulls: %v", setupErr)
			} else {
				registrySetup = setup
			}
		}

		createCmd := exec.Command(
			filepath.Join(cfg.BinDir, "k3d"),
			k3dCreateArgs(stackName, k3dConfigPath, registrySetup)...,
		)
		if err := u.Exec(ui.ExecConfig{
			Name: "Creating k3d cluster",
			Cmd:  createCmd,
		}); err != nil {
			return nil, fmt.Errorf("failed to create cluster: %w", err)
		}
	}

	// Export kubeconfig
	kubeconfigCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "kubeconfig", "get", stackName)

	kubeconfigData, err := kubeconfigCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// k3d generates kubeconfig with server: https://0.0.0.0:<port>.
	// On macOS, Go's HTTP client and helm can't connect to 0.0.0.0.
	// Replace with 127.0.0.1 which works on all platforms.
	kubeconfigData = []byte(strings.ReplaceAll(string(kubeconfigData), "https://0.0.0.0:", "https://127.0.0.1:"))

	// Wait for the Kubernetes API server to be reachable.
	if err := waitForAPIServer(u, kubeconfigData); err != nil {
		return nil, fmt.Errorf("cluster started but API server not ready: %w", err)
	}

	return kubeconfigData, nil
}

// waitForAPIServer polls the Kubernetes API server URL from the kubeconfig
// until it responds or a timeout is reached.
func waitForAPIServer(u *ui.UI, kubeconfigData []byte) error {
	// Extract the server URL from kubeconfig
	var serverURL string

	for line := range strings.SplitSeq(string(kubeconfigData), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "server:"); ok {
			serverURL = strings.TrimSpace(after)
			break
		}
	}

	if serverURL == "" {
		return errors.New("could not find server URL in kubeconfig")
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsInsecureSkipVerify(),
		},
	}

	return u.RunWithSpinner("Waiting for Kubernetes API server", func() error {
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(serverURL + "/version")
			if err == nil {
				resp.Body.Close()

				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
					return nil
				}
			}

			time.Sleep(2 * time.Second)
		}

		return fmt.Errorf("timed out after 120s waiting for API server at %s", serverURL)
	})
}

func (b *K3dBackend) Down(cfg *config.Config, u *ui.UI, stackID string) error {
	stackName := "obol-stack-" + stackID

	u.Infof("Stopping stack: %s", stackName)

	stopCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "stop", stackName)
	if err := u.Exec(ui.ExecConfig{
		Name: "Stopping k3d cluster",
		Cmd:  stopCmd,
	}); err != nil {
		u.Warn("Graceful stop failed, forcing cluster deletion")

		deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
		if err := u.Exec(ui.ExecConfig{
			Name: "Deleting k3d cluster",
			Cmd:  deleteCmd,
		}); err != nil {
			return fmt.Errorf("failed to stop cluster: %w", err)
		}
	}

	return nil
}

func (b *K3dBackend) Destroy(cfg *config.Config, u *ui.UI, stackID string) error {
	stackName := "obol-stack-" + stackID

	deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
	if err := u.Exec(ui.ExecConfig{
		Name: "Deleting cluster containers",
		Cmd:  deleteCmd,
	}); err != nil {
		u.Warnf("Failed to delete cluster (may already be deleted): %v", err)
	}

	return nil
}

func (b *K3dBackend) DataDir(cfg *config.Config) string {
	return "/data"
}

// portBlock returns the YAML block for a given host:container port mapping as
// it appears in the embedded k3d-config.yaml template.
func portBlock(host, container int) string {
	return fmt.Sprintf("  - port: %d:%d\n    nodeFilters:\n      - loadbalancer\n", host, container)
}

// stripConflictingPorts removes the identity port mappings (80:80, 443:443)
// from a k3d config string when those host ports are already in use. The
// fallback mappings (8080→80, 8443→443) are always preserved so Traefik
// remains reachable on an alternative port.
func stripConflictingPorts(k3dConfig string, u *ui.UI) string {
	type mapping struct {
		hostPort      int
		containerPort int
	}

	// Only strip the identity mappings; the high-port fallbacks are kept.
	candidates := []mapping{
		{80, 80},
		{443, 443},
	}

	for _, c := range candidates {
		if checkPortsAvailable([]int{c.hostPort}) != nil {
			block := portBlock(c.hostPort, c.containerPort)
			if strings.Contains(k3dConfig, block) {
				k3dConfig = strings.Replace(k3dConfig, block, "", 1)
				u.Warnf("Port %d is in use — removed %d:%d mapping (use port %d instead)",
					c.hostPort, c.hostPort, c.containerPort, c.hostPort+8000)
			}
		}
	}

	return k3dConfig
}

// ensureK3dPortsAvailable re-reads the k3d config file, strips any port
// mappings that have become conflicting since the config was written, and
// persists the result. This handles the case where port state changed between
// 'obol stack init' and 'obol stack up'.
func ensureK3dPortsAvailable(configPath string, u *ui.UI) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	original := string(data)
	updated := stripConflictingPorts(original, u)

	if updated != original {
		_ = os.WriteFile(configPath, []byte(updated), 0o600)
	}
}
