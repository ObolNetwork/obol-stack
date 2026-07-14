package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

type Runtime string

const (
	OpenClaw Runtime = "openclaw"
	Hermes   Runtime = "hermes"

	DefaultDomain     = "obol.stack"
	DefaultInstanceID = "obol-agent"
)

type Descriptor struct {
	Runtime       Runtime
	DisplayName   string
	ServiceName   string
	ConfigMapName string
	DataPVCName   string
	HomeDir       string
	DefaultPort   int
}

type DeploymentRef struct {
	Runtime Runtime
	ID      string
}

func Describe(runtime Runtime) Descriptor {
	switch runtime {
	case Hermes:
		return Descriptor{
			Runtime:       Hermes,
			DisplayName:   "Hermes",
			ServiceName:   "hermes",
			ConfigMapName: "hermes-config",
			DataPVCName:   "hermes-data",
			HomeDir:       ".hermes",
			DefaultPort:   8642,
		}
	default:
		return Descriptor{
			Runtime:       OpenClaw,
			DisplayName:   "OpenClaw",
			ServiceName:   "openclaw",
			ConfigMapName: "openclaw-config",
			DataPVCName:   "openclaw-data",
			HomeDir:       ".openclaw",
			DefaultPort:   18789,
		}
	}
}

func DeploymentPath(cfg *config.Config, runtime Runtime, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", string(runtime), id)
}

func Namespace(runtime Runtime, id string) string {
	return fmt.Sprintf("%s-%s", runtime, id)
}

func Hostname(runtime Runtime, id string) string {
	return fmt.Sprintf("%s-%s.%s", runtime, id, DefaultDomain)
}

func DashboardHostname(runtime Runtime, id string) string {
	if runtime == Hermes {
		if id == DefaultInstanceID {
			return fmt.Sprintf("%s.%s", DefaultInstanceID, DefaultDomain)
		}

		return fmt.Sprintf("%s-ui.%s", Namespace(Hermes, id), DefaultDomain)
	}

	return Hostname(runtime, id)
}

func Hostnames(runtime Runtime, id string) []string {
	if strings.TrimSpace(id) == "" {
		return nil
	}

	hostnames := []string{Hostname(runtime, id)}
	if runtime == Hermes {
		hostnames = append(hostnames, DashboardHostname(runtime, id))
	}

	return hostnames
}

func CollectHostnames(cfg *config.Config, include ...DeploymentRef) []string {
	var hostnames []string
	seen := make(map[string]bool)

	add := func(runtime Runtime, id string) {
		for _, hostname := range Hostnames(runtime, id) {
			if hostname == "" || seen[hostname] {
				continue
			}

			hostnames = append(hostnames, hostname)
			seen[hostname] = true
		}
	}

	for _, ref := range include {
		add(ref.Runtime, ref.ID)
	}

	for _, runtime := range []Runtime{Hermes, OpenClaw} {
		ids, err := ListInstanceIDs(cfg, runtime)
		if err != nil {
			continue
		}

		for _, id := range ids {
			add(runtime, id)
		}
	}

	return hostnames
}

func DataRoot(cfg *config.Config, runtime Runtime, id string) string {
	desc := Describe(runtime)
	return filepath.Join(cfg.DataDir, Namespace(runtime, id), desc.DataPVCName)
}

func HomePath(cfg *config.Config, runtime Runtime, id string) string {
	desc := Describe(runtime)
	return filepath.Join(DataRoot(cfg, runtime, id), desc.HomeDir)
}

func WorkspacePath(cfg *config.Config, runtime Runtime, id string) string {
	return filepath.Join(HomePath(cfg, runtime, id), "workspace")
}

func KeystoreVolumePath(cfg *config.Config, runtime Runtime, id string) string {
	return filepath.Join(cfg.DataDir, Namespace(runtime, id), "remote-signer-keystores")
}

func ListInstanceIDs(cfg *config.Config, runtime Runtime) ([]string, error) {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", string(runtime))

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to read %s instances: %w", Describe(runtime).DisplayName, err)
	}

	var ids []string

	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}

	return ids, nil
}
