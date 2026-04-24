package agentruntime

import (
	"errors"
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

func SkillsPath(cfg *config.Config, runtime Runtime, id string) string {
	return filepath.Join(HomePath(cfg, runtime, id), "skills")
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

func ResolveInstance(cfg *config.Config, runtime Runtime, args []string) (id string, remaining []string, err error) {
	instances, err := ListInstanceIDs(cfg, runtime)
	if err != nil {
		return "", nil, err
	}

	desc := Describe(runtime)

	switch len(instances) {
	case 0:
		return "", nil, fmt.Errorf("no %s instances found — run 'obol %s onboard' to create one", desc.DisplayName, runtime)
	case 1:
		return instances[0], args, nil
	default:
		if len(args) > 0 {
			for _, inst := range instances {
				if args[0] == inst {
					return inst, args[1:], nil
				}
			}
		}

		return "", nil, fmt.Errorf("multiple %s instances found, specify one: %s", desc.DisplayName, strings.Join(instances, ", "))
	}
}

func MustDefaultDeploymentPath(cfg *config.Config) string {
	return DeploymentPath(cfg, Hermes, DefaultInstanceID)
}

func ResolveSingleDefaultNamespace(cfg *config.Config, runtime Runtime) (string, error) {
	ids, err := ListInstanceIDs(cfg, runtime)
	if err != nil {
		return "", err
	}

	switch len(ids) {
	case 0:
		return "", errors.New("no instances found")
	case 1:
		return Namespace(runtime, ids[0]), nil
	default:
		return "", fmt.Errorf("multiple %s instances found (%s), specify an instance", Describe(runtime).DisplayName, strings.Join(ids, ", "))
	}
}
