package defaults

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

const (
	backendK3d = "k3d"
	backendK3s = "k3s"

	stackIDFile      = ".stack-id"
	stackBackendFile = ".stack-backend"
	stampFile        = ".obol-defaults-stamp"
)

// RefreshInfrastructureIfChanged refreshes the generated defaults tree when
// the embedded infrastructure assets, backend, or stack ID changed.
func RefreshInfrastructureIfChanged(cfg *config.Config, backendName, stackID string) (bool, error) {
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	stamp, err := infrastructureStamp(backendName, stackID)
	if err != nil {
		return false, err
	}

	currentStamp, _ := os.ReadFile(filepath.Join(defaultsDir, stampFile))
	_, helmfileErr := os.Stat(filepath.Join(defaultsDir, "helmfile.yaml"))
	if string(currentStamp) == stamp && helmfileErr == nil {
		return false, nil
	}

	if err := CopyInfrastructure(cfg, backendName, stackID); err != nil {
		return false, err
	}

	return true, nil
}

// CopyInfrastructure renders the embedded infrastructure defaults for the
// current stack and records the stamp that produced the copied tree.
func CopyInfrastructure(cfg *config.Config, backendName, stackID string) error {
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	replacements, err := InfrastructureReplacements(backendName, stackID)
	if err != nil {
		return err
	}

	if err := embed.CopyDefaults(defaultsDir, replacements); err != nil {
		return err
	}

	stamp, err := infrastructureStamp(backendName, stackID)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(defaultsDir, stampFile), []byte(stamp), 0o600)
}

// InfrastructureReplacements returns the placeholder values used when copying
// embedded infrastructure defaults.
func InfrastructureReplacements(backendName, stackID string) (map[string]string, error) {
	ollamaHost := OllamaHostForBackend(backendName)

	ollamaHostIP, err := OllamaHostIPForBackend(backendName)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"{{OLLAMA_HOST}}":    ollamaHost,
		"{{OLLAMA_HOST_IP}}": ollamaHostIP,
		"{{CLUSTER_ID}}":     stackID,
	}, nil
}

// DetectedBackendName reads the persisted backend choice, defaulting to k3d for
// legacy stacks that predate .stack-backend.
func DetectedBackendName(cfg *config.Config) string {
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, stackBackendFile))
	if err != nil {
		return backendK3d
	}

	backendName := strings.TrimSpace(string(data))
	if backendName == "" {
		return backendK3d
	}

	return backendName
}

// StackID reads the persisted stack ID.
func StackID(cfg *config.Config) string {
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, stackIDFile))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

func infrastructureStamp(backendName, stackID string) (string, error) {
	digest, err := embed.InfrastructureDigest()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("digest=%s\nbackend=%s\nstackID=%s\n", digest, backendName, stackID), nil
}

// OllamaHostForBackend returns the hostname/IP that reaches the host Ollama
// instance from inside the cluster.
func OllamaHostForBackend(backendName string) string {
	if backendName == backendK3s {
		return "127.0.0.1"
	}

	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}

	return "host.k3d.internal"
}

// OllamaHostIPForBackend resolves the Ollama host to an IP address.
// ClusterIP+Endpoints requires an IP, not a hostname.
func OllamaHostIPForBackend(backendName string) (string, error) {
	host := OllamaHostForBackend(backendName)

	if net.ParseIP(host) != nil {
		return host, nil
	}

	addrs, err := net.LookupHost(host)
	if err == nil && len(addrs) > 0 {
		return addrs[0], nil
	}

	if runtime.GOOS == "darwin" && backendName == backendK3d {
		return DockerDesktopGatewayIP(), nil
	}

	if runtime.GOOS == "linux" && backendName == backendK3d {
		ip, bridgeErr := DockerBridgeGatewayIP()
		if bridgeErr == nil {
			return ip, nil
		}

		return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w; docker0 fallback also failed: %w", host, err, bridgeErr)
	}

	return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w\n\tEnsure Docker Desktop is running", host, err)
}

// DockerDesktopGatewayIP returns the Docker Desktop VM gateway IP.
func DockerDesktopGatewayIP() string {
	return "192.168.65.254"
}

// DockerBridgeGatewayIP returns the IPv4 address of an active Docker bridge
// interface.
func DockerBridgeGatewayIP() (string, error) {
	if ip, err := BridgeInterfaceIP("docker0"); err == nil {
		return ip, nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("cannot list network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if !strings.HasPrefix(iface.Name, "br-") {
			continue
		}
		if ip, err := BridgeInterfaceIP(iface.Name); err == nil {
			return ip, nil
		}
	}

	return "", errors.New("no active Docker bridge interface found (docker0 or br-*)")
}

// BridgeInterfaceIP returns the IPv4 address of a named network interface.
func BridgeInterfaceIP(name string) (string, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", fmt.Errorf("interface %s not found: %w", name, err)
	}

	if iface.Flags&net.FlagUp == 0 {
		return "", fmt.Errorf("interface %s is down", name)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("cannot get addresses for %s: %w", name, err)
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			return ipNet.IP.String(), nil
		}
	}

	return "", fmt.Errorf("no IPv4 address found on interface %s", name)
}
