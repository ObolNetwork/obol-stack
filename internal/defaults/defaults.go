package defaults

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

const (
	backendK3d = "k3d"
	backendK3s = "k3s"

	stackIDFile      = ".stack-id"
	stackBackendFile = ".stack-backend"
	stampFile        = ".obol-defaults-stamp"
	// devImageTagFile records the tag the dev-mode manifest rewrite stamped
	// the locally-built images with. internal/stack reads it at build time so
	// the image it builds/imports matches what the rendered manifests pin —
	// even if HEAD moved between `stack init` and `stack up`.
	devImageTagFile = ".dev-image-tag"
)

// DevImageTag returns the tag used for locally-built dev images under
// OBOL_DEVELOPMENT. It is `dev-<short-git-sha>` of the working tree, so each
// branch/worktree builds a distinct tag and parallel dev stacks sharing one
// Docker daemon never clobber each other's images (the `:latest` collision that
// let a sibling worktree's build poison an unrelated stack). Committing changes
// the SHA and triggers a fresh build; uncommitted changes reuse the committed
// tag unless OBOL_FORCE_REBUILD_LOCAL_DEV_IMAGES is set. Falls back to `latest`
// when the source is not a git checkout (e.g. a tarball build), preserving the
// previous behaviour there.
func DevImageTag() string {
	cmd := exec.Command("git", "rev-parse", "--short=12", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "latest"
	}
	sha := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^[0-9a-f]{7,40}$`).MatchString(sha) {
		return "latest"
	}
	return "dev-" + sha
}

// ReadDevImageTag returns the dev image tag persisted at CopyInfrastructure
// time, or "latest" if none was recorded (non-dev install, or pre-dates this
// mechanism). internal/stack uses it to tag the images it builds.
func ReadDevImageTag(cfg *config.Config) string {
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, devImageTagFile))
	if err != nil {
		return "latest"
	}
	tag := strings.TrimSpace(string(data))
	if tag == "" {
		return "latest"
	}
	return tag
}

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

	// Under OBOL_DEVELOPMENT we build images from the working tree and
	// import them into k3d. The embedded templates pin published digests for
	// production safety, which means the cluster ignores our locally-built
	// images and silently uses stale ghcr.io binaries. Rewrite the digest
	// pins to a per-commit `dev-<sha>` tag after copy so the dev cycle Just
	// Works without operators having to kubectl-set-image every loop, and so
	// parallel worktree stacks don't collide on a shared `:latest`. Persist
	// the tag so internal/stack builds/imports the exact tag we pinned here.
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		devTag := DevImageTag()
		if err := rewriteDevDigestPins(defaultsDir, devTag); err != nil {
			return fmt.Errorf("rewrite dev digest pins: %w", err)
		}
		if err := os.WriteFile(filepath.Join(cfg.ConfigDir, devImageTagFile), []byte(devTag), 0o600); err != nil {
			return fmt.Errorf("persist dev image tag: %w", err)
		}
	}

	stamp, err := infrastructureStamp(backendName, stackID)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(defaultsDir, stampFile), []byte(stamp), 0o600)
}

// devLocallyBuiltImageBases lists the image refs whose digests we want
// to swap for :latest under OBOL_DEVELOPMENT. Must stay in lockstep
// with internal/stack.baseLocalImages — duplication is intentional to
// avoid an import cycle (defaults → stack would form a loop).
var devLocallyBuiltImageBases = []string{
	"ghcr.io/obolnetwork/x402-verifier",
	"ghcr.io/obolnetwork/serviceoffer-controller",
	"ghcr.io/obolnetwork/x402-buyer",
	"ghcr.io/obolnetwork/demo-server",
	"ghcr.io/obolnetwork/obol-stack-public-storefront",
}

// rewriteDevDigestPins walks the copied defaults tree and replaces
// every `<base>@sha256:<hex>` or `<base>:<short-sha>` reference whose
// base is in devLocallyBuiltImageBases with `<base>:latest`. Only
// operates on .yaml and .yml files so we don't risk corrupting binaries
// or charts.
//
// Both pin styles are matched because release pipelines may publish
// either digest pins (immutable) or short-SHA tag pins (e.g. `:b13254e`)
// — in either case the local dev build is tagged `:latest`, so the
// rewrite needs to catch both forms or `obol stack up` would pull from
// the registry instead of using the freshly-built local image.
func rewriteDevDigestPins(defaultsDir, devTag string) error {
	patterns := make([]*regexp.Regexp, 0, len(devLocallyBuiltImageBases))
	replaceWith := make([]string, 0, len(devLocallyBuiltImageBases))
	for _, base := range devLocallyBuiltImageBases {
		// Match all three pin styles we ship across the infrastructure
		// templates and rewrite to `:latest` so the local-dev build wins.
		// Patterns covered (single regex, left-to-right alternation):
		//   <base>:<7-40 hex>@sha256:<64 hex>   tag + digest combo
		//   <base>@sha256:<64 hex>              digest-only pin
		//   <base>:<7-40 hex>                   short-SHA tag pin (e.g. b13254e)
		// The combo form MUST come first so the engine doesn't stop at the
		// shorter `:<hex>` match and leave a stray `@sha256:<digest>` suffix,
		// which Docker still resolves to the immutable registry image and
		// silently bypasses the local build (root cause of the no-debug-logs
		// regression in flow-11 step 43 chase, May 2026).
		patterns = append(patterns, regexp.MustCompile(regexp.QuoteMeta(base)+"(:[a-f0-9]{7,40}@sha256:[a-f0-9]{64}|@sha256:[a-f0-9]{64}|:[a-f0-9]{7,40})"))
		replaceWith = append(replaceWith, base+":"+devTag)
	}

	return filepath.WalkDir(defaultsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := data
		for i, p := range patterns {
			updated = p.ReplaceAll(updated, []byte(replaceWith[i]))
		}
		if string(updated) == string(data) {
			return nil
		}
		return os.WriteFile(path, updated, 0o600)
	})
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
//
// Resolution order on darwin+k3d:
//  1. Resolve host.docker.internal from inside a transient Docker container
//     (works for Docker Desktop, Colima, Rancher Desktop — each exposes a
//     different host gateway IP, but all expose host.docker.internal inside
//     containers).
//  2. Fall back to the Docker Desktop magic gateway IP (192.168.65.254) so
//     Docker Desktop users without a working `docker` CLI still work.
func OllamaHostIPForBackend(backendName string) (string, error) {
	host := OllamaHostForBackend(backendName)

	if net.ParseIP(host) != nil {
		return host, nil
	}

	addrs, err := net.LookupHost(host)
	if err == nil && len(addrs) > 0 {
		return addrs[0], nil
	}

	if backendName == backendK3d {
		if ip, dockerErr := ResolveHostGatewayViaDocker(); dockerErr == nil {
			return ip, nil
		}

		if runtime.GOOS == "darwin" {
			return DockerDesktopGatewayIP(), nil
		}

		if runtime.GOOS == "linux" {
			ip, bridgeErr := DockerBridgeGatewayIP()
			if bridgeErr == nil {
				return ip, nil
			}

			return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w; docker0 fallback also failed: %w", host, err, bridgeErr)
		}
	}

	return "", fmt.Errorf("cannot resolve Ollama host %q to IP: %w\n\tEnsure Docker Desktop, Colima, or Rancher Desktop is running", host, err)
}

// DockerDesktopGatewayIP returns the Docker Desktop VM gateway IP.
//
// This is a hardcoded magic value valid only for Docker Desktop on macOS.
// Colima and Rancher Desktop use different bridge IPs (e.g. 192.168.5.2 for
// Colima's default profile), so prefer ResolveHostGatewayViaDocker which
// works across all macOS Docker runtimes.
func DockerDesktopGatewayIP() string {
	return "192.168.65.254"
}

// ResolveHostGatewayViaDocker asks the local Docker daemon to resolve the
// host gateway by running a tiny container that prints the IP of
// host.docker.internal. This works on Docker Desktop, Colima, and Rancher
// Desktop because all three expose host.docker.internal inside containers
// and map it to whatever bridge gateway their VM is using.
//
// Falls back to --add-host=host.docker.internal:host-gateway when the bare
// hostname is not pre-populated by the runtime.
func ResolveHostGatewayViaDocker() (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker CLI not found: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, args := range [][]string{
		{"run", "--rm", "alpine:3", "getent", "hosts", "host.docker.internal"},
		{"run", "--rm", "--add-host=host.docker.internal:host-gateway", "alpine:3", "getent", "hosts", "host.docker.internal"},
	} {
		out, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			continue
		}

		fields := strings.Fields(strings.TrimSpace(string(out)))
		if len(fields) < 1 {
			continue
		}

		if ip := net.ParseIP(fields[0]); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}

	return "", errors.New("docker host gateway resolution returned no IPv4 address")
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
