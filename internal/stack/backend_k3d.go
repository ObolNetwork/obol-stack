package stack

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	// Rewrite occupied host-port mappings so k3d cluster create won't fail
	// when another local stack is already bound to the default ingress ports.
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

	// Ensure the pull-through registry caches are running on every Up, in
	// BOTH the existing-cluster and fresh-create branches. `k3d cluster
	// start` does not auto-restart standalone registry containers attached
	// via `--registry-use` at create time — it only starts the cluster's
	// own nodes. Without this call, every retry after a `cluster stop` (or
	// after the failure-recovery Down() call in syncDefaults) falls back to
	// direct upstream pulls and re-fetches every image, costing minutes per
	// attempt.
	//
	// Pull-through caches (docker.io, ghcr.io, quay.io) are ON by default
	// for all users. The local push target (localhost:54103) is only started
	// in OBOL_DEVELOPMENT=true mode — it is used by `just dev-frontend` for
	// fast layered-diff reloads and is not needed by regular installs.
	//
	// Set OBOL_DISABLE_REGISTRY_CACHE=true to skip all cache containers
	// (e.g. hosts behind a corporate proxy with their own caching, or
	// environments with tight disk constraints).
	devMode := os.Getenv("OBOL_DEVELOPMENT") == "true"
	{
		setup, setupErr := ensureRegistryCaches(cfg, u, devMode)
		if setupErr != nil {
			u.Warnf("Registry cache unavailable, falling back to direct upstream pulls: %v", setupErr)
		} else {
			registrySetup = setup
		}
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

// stripConflictingPorts removes occupied default k3d ingress mappings. If all
// default host ports for a container port are occupied, it adds an ephemeral
// host-port mapping so multiple dev stacks can coexist on the same machine.
func stripConflictingPorts(k3dConfig string, u *ui.UI) string {
	return rewriteConflictingPorts(k3dConfig, u, hostPortAvailable, pickAvailableHostPort)
}

func rewriteConflictingPorts(
	k3dConfig string,
	u *ui.UI,
	available func(int) bool,
	pickPort func() (int, error),
) string {
	hasMapping := map[int]bool{}

	for _, c := range parseK3dPortMappings(k3dConfig) {
		if c.containerPort != 80 && c.containerPort != 443 {
			continue
		}

		block := portBlock(c.hostPort, c.containerPort)
		if !strings.Contains(k3dConfig, block) {
			continue
		}
		if available(c.hostPort) {
			hasMapping[c.containerPort] = true
			continue
		}

		k3dConfig = strings.Replace(k3dConfig, block, "", 1)
		if fallbackPort := fallbackForDefaultPort(c); fallbackPort > 0 {
			u.Warnf("Port %d is in use — removed %d:%d mapping (use port %d instead if available)",
				c.hostPort, c.hostPort, c.containerPort, fallbackPort)
		} else {
			u.Warnf("Port %d is in use — removed %d:%d mapping", c.hostPort, c.hostPort, c.containerPort)
		}
	}

	for _, containerPort := range []int{80, 443} {
		if hasMapping[containerPort] {
			continue
		}

		hostPort, err := pickPort()
		if err != nil {
			u.Warnf("No default host port is available for container port %d and no ephemeral port could be selected: %v", containerPort, err)
			continue
		}
		k3dConfig = insertK3dPortMapping(k3dConfig, portBlock(hostPort, containerPort))
		u.Warnf("All default host ports for container port %d are in use — using %d:%d instead", containerPort, hostPort, containerPort)
	}

	return k3dConfig
}

type k3dPortMapping struct {
	hostPort      int
	containerPort int
}

func parseK3dPortMappings(k3dConfig string) []k3dPortMapping {
	var mappings []k3dPortMapping
	for _, line := range strings.Split(k3dConfig, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- port:") {
			continue
		}

		portSpec := strings.TrimSpace(strings.TrimPrefix(line, "- port:"))
		parts := strings.Split(portSpec, ":")
		if len(parts) != 2 {
			continue
		}
		hostPort, hostErr := strconv.Atoi(parts[0])
		containerPort, containerErr := strconv.Atoi(parts[1])
		if hostErr != nil || containerErr != nil {
			continue
		}
		mappings = append(mappings, k3dPortMapping{hostPort: hostPort, containerPort: containerPort})
	}
	return mappings
}

func fallbackForDefaultPort(mapping k3dPortMapping) int {
	switch mapping {
	case k3dPortMapping{hostPort: 80, containerPort: 80}:
		return 8080
	case k3dPortMapping{hostPort: 443, containerPort: 443}:
		return 8443
	default:
		return 0
	}
}

func hostPortAvailable(port int) bool {
	return checkPortsAvailable([]int{port}) == nil
}

func pickAvailableHostPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.Port == 0 {
		return 0, fmt.Errorf("unexpected listener address %q", ln.Addr().String())
	}
	return addr.Port, nil
}

func insertK3dPortMapping(k3dConfig, block string) string {
	if strings.Contains(k3dConfig, "options:\n") {
		return strings.Replace(k3dConfig, "options:\n", block+"options:\n", 1)
	}
	if strings.Contains(k3dConfig, "ports:\n") {
		return k3dConfig + block
	}
	return k3dConfig + "\nports:\n" + block
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
		if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
			u.Warnf("Could not update k3d config at %s: %v", configPath, err)
		}
	}
}
