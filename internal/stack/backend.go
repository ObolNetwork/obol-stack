package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	// BackendK3d is the k3d backend (Docker-based, default)
	BackendK3d = "k3d"
	// BackendK3s is the standalone k3s backend (bare-metal)
	BackendK3s = "k3s"

	stackBackendFile = ".stack-backend"
)

// Backend abstracts the Kubernetes cluster runtime (k3d, k3s)
type Backend interface {
	// Name returns the backend identifier (e.g., "k3d", "k3s")
	Name() string

	// Init generates backend-specific cluster configuration files
	Init(cfg *config.Config, u *ui.UI, stackID string) error

	// Up creates or starts the cluster and returns kubeconfig contents
	Up(cfg *config.Config, u *ui.UI, stackID string) (kubeconfigData []byte, err error)

	// IsRunning returns true if the cluster is currently running
	IsRunning(cfg *config.Config, stackID string) (bool, error)

	// Down stops the cluster without destroying configuration or data
	Down(cfg *config.Config, u *ui.UI, stackID string) error

	// Destroy removes the cluster entirely (containers/processes)
	Destroy(cfg *config.Config, u *ui.UI, stackID string) error

	// DataDir returns the storage path for the local-path-provisioner.
	// For k3d this is "/data" (Docker volume mount point).
	// For k3s this is the absolute host path to cfg.DataDir.
	DataDir(cfg *config.Config) string

	// Prerequisites checks that required software/permissions are available
	Prerequisites(cfg *config.Config) error
}

// NewBackend creates a Backend by name
func NewBackend(name string) (Backend, error) {
	switch name {
	case BackendK3d:
		return &K3dBackend{}, nil
	case BackendK3s:
		return &K3sBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend: %s (supported: k3d, k3s)", name)
	}
}

// LoadBackend reads the persisted backend choice from .stack-backend file.
// Falls back to k3d if no file exists (backward compatibility).
func LoadBackend(cfg *config.Config) (Backend, error) {
	path := filepath.Join(cfg.ConfigDir, stackBackendFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return &K3dBackend{}, nil
	}
	return NewBackend(strings.TrimSpace(string(data)))
}

// SaveBackend persists the backend choice
func SaveBackend(cfg *config.Config, name string) error {
	path := filepath.Join(cfg.ConfigDir, stackBackendFile)
	return os.WriteFile(path, []byte(name), 0644)
}

// DetectExistingBackend reads the persisted backend choice without
// falling back to a default. Returns empty string if no backend file exists,
// which lets Init() apply its own default.
func DetectExistingBackend(cfg *config.Config) string {
	path := filepath.Join(cfg.ConfigDir, stackBackendFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	// Validate it's a known backend; return empty if corrupted
	if _, err := NewBackend(name); err != nil {
		return ""
	}
	return name
}
