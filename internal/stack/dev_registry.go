package stack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	k3dRegistriesConfigFile = "registries.yaml"
	devRegistryCacheEnvVar  = "OBOL_REGISTRY_CACHE_DIR"
)

type registryMirror struct {
	upstreamHost string
	remoteURL    string
	name         string
	port         int
}

type devRegistrySetup struct {
	configPath string
	useRefs    []string
}

var devRegistryMirrors = []registryMirror{
	{upstreamHost: "docker.io", remoteURL: "https://registry-1.docker.io", name: "obol-docker-io.localhost", port: 54100},
	{upstreamHost: "ghcr.io", remoteURL: "https://ghcr.io", name: "obol-ghcr-io.localhost", port: 54101},
	{upstreamHost: "quay.io", remoteURL: "https://quay.io", name: "obol-quay-io.localhost", port: 54102},
}

func ensureDevRegistries(cfg *config.Config, u *ui.UI) (*devRegistrySetup, error) {
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	configPath := filepath.Join(cfg.ConfigDir, k3dRegistriesConfigFile)
	if err := os.WriteFile(configPath, []byte(renderDevRegistriesConfig()), 0o600); err != nil {
		return nil, fmt.Errorf("write registries config: %w", err)
	}

	if err := u.RunWithSpinner("Ensuring dev registry caches", func() error {
		k3dBinary := filepath.Join(cfg.BinDir, "k3d")

		for _, mirror := range devRegistryMirrors {
			if err := ensureDevRegistry(cfg, k3dBinary, mirror); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	setup := &devRegistrySetup{configPath: configPath}
	for _, mirror := range devRegistryMirrors {
		setup.useRefs = append(setup.useRefs, registryUseRef(mirror))
	}

	return setup, nil
}

func ensureDevRegistry(cfg *config.Config, k3dBinary string, mirror registryMirror) error {
	if err := os.MkdirAll(registryCacheDir(mirror), 0o755); err != nil {
		return fmt.Errorf("create cache dir for %s: %w", mirror.upstreamHost, err)
	}

	containerName := registryContainerName(mirror)
	running, err := dockerContainerRunning(containerName)
	if err == nil {
		if running {
			return nil
		}

		// Container exists but is stopped. Try to start it.
		if startErr := runCommand(exec.Command("docker", "start", containerName)); startErr == nil {
			return nil
		}

		// Start failed — most commonly because the k3d-obol-stack-* Docker
		// network the registry was attached to has been removed (cluster
		// purge or reclaimLeakedDevK3dNetworks). The container's stored
		// network reference is now a dangling ID and `docker start` aborts
		// with "network ... not found". Force-remove the container and
		// fall through to recreate it. The cache content lives on a host
		// volume mount, so the recreated container picks it back up
		// without re-downloading anything.
		_ = runCommand(exec.Command("docker", "rm", "-f", containerName))
	}

	createCmd := exec.Command(
		k3dBinary,
		"registry", "create", mirror.name,
		"--port", strconv.Itoa(mirror.port),
		"--proxy-remote-url", mirror.remoteURL,
		"--volume", fmt.Sprintf("%s:/var/lib/registry", registryCacheDir(mirror)),
		"--no-help",
	)
	if err := runCommand(createCmd); err != nil {
		return fmt.Errorf("create registry %s: %w", mirror.name, err)
	}

	return nil
}

func dockerContainerRunning(containerName string) (bool, error) {
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(string(out)) == "true", nil
}

func runCommand(cmd *exec.Cmd) error {
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}

		return fmt.Errorf("%w: %s", err, msg)
	}

	return nil
}

func renderDevRegistriesConfig() string {
	var b strings.Builder

	b.WriteString("mirrors:\n")
	for _, mirror := range devRegistryMirrors {
		fmt.Fprintf(&b, "  %q:\n", mirror.upstreamHost)
		b.WriteString("    endpoint:\n")
		fmt.Fprintf(&b, "      - %s\n", registryEndpoint(mirror))
	}

	return b.String()
}

func registryUseRef(mirror registryMirror) string {
	return registryContainerName(mirror) + ":" + strconv.Itoa(mirror.port)
}

func registryEndpoint(mirror registryMirror) string {
	return "http://" + registryContainerName(mirror) + ":5000"
}

func registryContainerName(mirror registryMirror) string {
	return "k3d-" + mirror.name
}

func registryCacheDir(mirror registryMirror) string {
	return filepath.Join(devRegistryCacheRoot(), mirror.upstreamHost)
}

func devRegistryCacheRoot() string {
	if dir := os.Getenv(devRegistryCacheEnvVar); dir != "" {
		return dir
	}

	xdgStateHome := os.Getenv("XDG_STATE_HOME")
	if xdgStateHome == "" {
		home, _ := os.UserHomeDir()
		xdgStateHome = filepath.Join(home, ".local", "state")
	}

	return filepath.Join(xdgStateHome, "obol", "registry-cache")
}

func k3dCreateArgs(stackName, k3dConfigPath string, registrySetup *devRegistrySetup) []string {
	args := []string{
		"cluster", "create", stackName,
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
	}

	if registrySetup == nil {
		return args
	}

	args = append(args, "--registry-config", registrySetup.configPath)
	for _, ref := range registrySetup.useRefs {
		args = append(args, "--registry-use", ref)
	}

	return args
}
