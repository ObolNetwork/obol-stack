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
	// disableRegistryCacheEnvVar is read by backend_k3d and by tests.
	// When set to "true" (or "1"), all pull-through cache containers are skipped.
	disableRegistryCacheEnvVar = "OBOL_DISABLE_REGISTRY_CACHE"
)

type registryMirror struct {
	upstreamHost string
	remoteURL    string
	name         string
	port         int
}

type registrySetup struct {
	configPath string
	useRefs    []string
}

// pullThroughMirrors are the three upstream pull-through caches that are
// started for ALL users by default (not just dev mode). They are tiny
// containers that cache image layers locally so that the second
// `obol stack up` on the same host pulls from disk instead of the internet.
var pullThroughMirrors = []registryMirror{
	{upstreamHost: "docker.io", remoteURL: "https://registry-1.docker.io", name: "obol-docker-io.localhost", port: 54100},
	{upstreamHost: "ghcr.io", remoteURL: "https://ghcr.io", name: "obol-ghcr-io.localhost", port: 54101},
	{upstreamHost: "quay.io", remoteURL: "https://quay.io", name: "obol-quay-io.localhost", port: 54102},
}

// localPushMirror is the local push target used by `just dev-frontend` to
// swap layered diffs into the cluster via `docker push localhost:54103/...`
// instead of `k3d image import`'s full-tarball round-trip. Only started when
// OBOL_DEVELOPMENT=true — regular users don't need it.
var localPushMirror = registryMirror{
	// No remoteURL — this is a pure local push target (no upstream proxy).
	upstreamHost: "localhost:54103",
	name:         "obol-local.localhost",
	port:         54103,
}

// devRegistryMirrors is kept for backward-compatibility with callers that
// iterate all mirrors (e.g. existing tests). It always returns pull-through
// mirrors; in dev mode it also includes the local push target.
//
// Deprecated: prefer pullThroughMirrors + localPushMirror directly.
var devRegistryMirrors = pullThroughMirrors

// allDevRegistryMirrors returns the full set (pull-through + local push).
// Used only in OBOL_DEVELOPMENT=true paths.
func allDevRegistryMirrors() []registryMirror {
	return append(append([]registryMirror{}, pullThroughMirrors...), localPushMirror)
}

// ensureRegistryCaches sets up the registry mirror containers and writes the
// k3d registries.yaml config file. It is called on every `obol stack up`.
//
//   - devMode=true → also starts the local push target (localhost:54103).
//   - devMode=false → starts only the three pull-through caches.
//
// Returns nil, nil when OBOL_DISABLE_REGISTRY_CACHE=true so the caller
// can treat that as "no registry setup requested".
func ensureRegistryCaches(cfg *config.Config, u *ui.UI, devMode bool) (*registrySetup, error) {
	if os.Getenv(disableRegistryCacheEnvVar) == "true" || os.Getenv(disableRegistryCacheEnvVar) == "1" {
		return nil, nil
	}

	mirrors := pullThroughMirrors
	if devMode {
		mirrors = allDevRegistryMirrors()
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	configPath := filepath.Join(cfg.ConfigDir, k3dRegistriesConfigFile)
	if err := os.WriteFile(configPath, []byte(renderRegistriesConfig(mirrors)), 0o600); err != nil {
		return nil, fmt.Errorf("write registries config: %w", err)
	}

	spinnerLabel := "Ensuring registry caches"
	if devMode {
		spinnerLabel = "Ensuring dev registry caches"
	}

	if err := u.RunWithSpinner(spinnerLabel, func() error {
		k3dBinary := filepath.Join(cfg.BinDir, "k3d")
		for _, mirror := range mirrors {
			if err := ensureDevRegistry(cfg, k3dBinary, mirror); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	setup := &registrySetup{configPath: configPath}
	for _, mirror := range mirrors {
		setup.useRefs = append(setup.useRefs, registryUseRef(mirror))
	}

	return setup, nil
}

// ensureDevRegistries is the legacy entry-point kept for backward
// compatibility. New code should call ensureRegistryCaches directly.
func ensureDevRegistries(cfg *config.Config, u *ui.UI) (*devRegistrySetup, error) {
	setup, err := ensureRegistryCaches(cfg, u, true)
	if err != nil {
		return nil, err
	}
	if setup == nil {
		return nil, nil
	}
	return &devRegistrySetup{configPath: setup.configPath, useRefs: setup.useRefs}, nil
}

// devRegistrySetup is a type alias kept so existing code that references it
// continues to compile without changes.
type devRegistrySetup = registrySetup

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
		// fall through to recreate it. The cache content lives on the host
		// bind-mount under registryCacheDir(mirror), so the new container
		// picks up exactly the layers the old one had — no re-pull.
		_ = runCommand(exec.Command("docker", "rm", "-f", containerName))
	}

	createArgs := []string{
		"registry", "create", mirror.name,
		"--port", strconv.Itoa(mirror.port),
		"--volume", fmt.Sprintf("%s:/var/lib/registry", registryCacheDir(mirror)),
		"--no-help",
	}
	if mirror.remoteURL != "" {
		createArgs = append(createArgs, "--proxy-remote-url", mirror.remoteURL)
	}
	if err := runCommand(exec.Command(k3dBinary, createArgs...)); err != nil {
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

// renderRegistriesConfig renders the k3d registries.yaml content for the
// given set of mirrors.
func renderRegistriesConfig(mirrors []registryMirror) string {
	var b strings.Builder

	b.WriteString("mirrors:\n")
	for _, mirror := range mirrors {
		fmt.Fprintf(&b, "  %q:\n", mirror.upstreamHost)
		b.WriteString("    endpoint:\n")
		fmt.Fprintf(&b, "      - %s\n", registryEndpoint(mirror))
	}

	return b.String()
}

// renderDevRegistriesConfig is kept for backward-compat with existing tests.
func renderDevRegistriesConfig() string {
	return renderRegistriesConfig(allDevRegistryMirrors())
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
	// Colons in the upstream host (e.g. "localhost:54103" for the local push
	// registry) would land in the on-disk path; replace with underscore so the
	// dir name is plain ASCII. Existing entries (docker.io, ghcr.io, quay.io)
	// have no colons so their cache paths are unchanged.
	safe := strings.ReplaceAll(mirror.upstreamHost, ":", "_")
	return filepath.Join(devRegistryCacheRoot(), safe)
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

func k3dCreateArgs(stackName, k3dConfigPath string, setup *registrySetup) []string {
	args := []string{
		"cluster", "create", stackName,
		"--config", k3dConfigPath,
		"--kubeconfig-update-default=false",
	}

	if setup == nil {
		return args
	}

	args = append(args, "--registry-config", setup.configPath)
	for _, ref := range setup.useRefs {
		args = append(args, "--registry-use", ref)
	}

	return args
}
