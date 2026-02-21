package stack

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
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
		return fmt.Errorf("Docker is not running. k3d backend requires Docker.\nStart Docker and try again")
	}

	// Check k3d binary exists
	k3dPath := filepath.Join(cfg.BinDir, "k3d")
	if _, err := os.Stat(k3dPath); os.IsNotExist(err) {
		return fmt.Errorf("k3d not found at %s\nRun obolup.sh to install dependencies", k3dPath)
	}
	return nil
}

func (b *K3dBackend) Init(cfg *config.Config, stackID string) error {
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

	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)
	if err := os.WriteFile(k3dConfigPath, []byte(k3dConfig), 0644); err != nil {
		return fmt.Errorf("failed to write k3d config: %w", err)
	}

	fmt.Printf("K3d config saved to: %s\n", k3dConfigPath)
	return nil
}

func (b *K3dBackend) IsRunning(cfg *config.Config, stackID string) (bool, error) {
	stackName := fmt.Sprintf("obol-stack-%s", stackID)
	listCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "list", "--no-headers")
	output, err := listCmd.Output()
	if err != nil {
		return false, fmt.Errorf("k3d list command failed: %w", err)
	}
	return strings.Contains(string(output), stackName), nil
}

func (b *K3dBackend) Up(cfg *config.Config, stackID string) ([]byte, error) {
	stackName := fmt.Sprintf("obol-stack-%s", stackID)
	k3dConfigPath := filepath.Join(cfg.ConfigDir, k3dConfigFile)

	running, err := b.IsRunning(cfg, stackID)
	if err != nil {
		return nil, err
	}

	if running {
		fmt.Printf("Stack already exists, attempting to start: %s (id: %s)\n", stackName, stackID)
		startCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "start", stackName)
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to start existing cluster: %w", err)
		}
	} else {
		// Create data directory if it doesn't exist
		absDataDir, err := filepath.Abs(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for data directory: %w", err)
		}
		if err := os.MkdirAll(absDataDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create data directory: %w", err)
		}

		fmt.Println("Creating k3d cluster...")
		createCmd := exec.Command(
			filepath.Join(cfg.BinDir, "k3d"),
			"cluster", "create", stackName,
			"--config", k3dConfigPath,
			"--kubeconfig-update-default=false",
		)
		createCmd.Stdout = os.Stdout
		createCmd.Stderr = os.Stderr
		if err := createCmd.Run(); err != nil {
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
	// After k3d starts containers, k3s inside needs time to bind ports.
	if err := waitForAPIServer(kubeconfigData); err != nil {
		return nil, fmt.Errorf("cluster started but API server not ready: %w", err)
	}

	return kubeconfigData, nil
}

// waitForAPIServer polls the Kubernetes API server URL from the kubeconfig
// until it responds or a timeout is reached. This prevents race conditions
// where helmfile runs before k3s has bound its listener.
func waitForAPIServer(kubeconfigData []byte) error {
	// Extract the server URL from kubeconfig (e.g. https://0.0.0.0:52489)
	var serverURL string
	for _, line := range strings.Split(string(kubeconfigData), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "server:") {
			serverURL = strings.TrimSpace(strings.TrimPrefix(trimmed, "server:"))
			break
		}
	}
	if serverURL == "" {
		return fmt.Errorf("could not find server URL in kubeconfig")
	}

	// k3d kubeconfig uses 0.0.0.0 which doesn't work with Go's HTTP client
	// on macOS (can't connect to 0.0.0.0). Replace with 127.0.0.1.
	serverURL = strings.Replace(serverURL, "0.0.0.0", "127.0.0.1", 1)

	// k3s uses a self-signed cert, so skip TLS verification for the health check
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsInsecureSkipVerify(),
		},
	}

	fmt.Print("Waiting for Kubernetes API server...")
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/version")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				fmt.Println(" ready")
				return nil
			}
		}
		time.Sleep(2 * time.Second)
		fmt.Print(".")
	}

	return fmt.Errorf("timed out after 60s waiting for API server at %s", serverURL)
}

func (b *K3dBackend) Down(cfg *config.Config, stackID string) error {
	stackName := fmt.Sprintf("obol-stack-%s", stackID)

	fmt.Printf("Stopping stack gracefully: %s (id: %s)\n", stackName, stackID)

	stopCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "stop", stackName)
	stopCmd.Stdout = os.Stdout
	stopCmd.Stderr = os.Stderr
	if err := stopCmd.Run(); err != nil {
		fmt.Println("Graceful stop timed out or failed, forcing cluster deletion")
		deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
		deleteCmd.Stdout = os.Stdout
		deleteCmd.Stderr = os.Stderr
		if err := deleteCmd.Run(); err != nil {
			return fmt.Errorf("failed to stop cluster: %w", err)
		}
	}

	return nil
}

func (b *K3dBackend) Destroy(cfg *config.Config, stackID string) error {
	stackName := fmt.Sprintf("obol-stack-%s", stackID)

	fmt.Printf("Deleting cluster containers: %s\n", stackName)
	deleteCmd := exec.Command(filepath.Join(cfg.BinDir, "k3d"), "cluster", "delete", stackName)
	deleteCmd.Stdout = os.Stdout
	deleteCmd.Stderr = os.Stderr
	if err := deleteCmd.Run(); err != nil {
		fmt.Printf("Failed to delete cluster (may already be deleted): %v\n", err)
	}

	return nil
}

func (b *K3dBackend) DataDir(cfg *config.Config) string {
	return "/data"
}
