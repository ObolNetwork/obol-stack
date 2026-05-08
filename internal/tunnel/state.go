package tunnel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const (
	tunnelExposureQuick      = "quick"
	tunnelExposurePersistent = "persistent"

	tunnelManagementQuick  = "quick"
	tunnelManagementLocal  = "local"
	tunnelManagementRemote = "remote"

	legacyTunnelModeQuick = "quick"
	legacyTunnelModeDNS   = "dns"

	managementConfigMapName = "cloudflared-management"
	managementConfigModeKey = "management_mode"

	persistentReplicaCount = 2
	quickReplicaCount      = 1
)

type tunnelState struct {
	// Mode is kept for backward compatibility with older on-disk state files.
	Mode string `json:"mode,omitempty"`

	ExposureMode   string    `json:"exposure_mode,omitempty"`
	ManagementMode string    `json:"management_mode,omitempty"`
	Hostname       string    `json:"hostname,omitempty"`
	AccountID      string    `json:"account_id,omitempty"`
	ZoneID         string    `json:"zone_id,omitempty"`
	TunnelID       string    `json:"tunnel_id,omitempty"`
	TunnelName     string    `json:"tunnel_name,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func tunnelStateDir(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "tunnel")
}

func tunnelStatePath(cfg *config.Config) string {
	return filepath.Join(tunnelStateDir(cfg), "cloudflared.json")
}

func remoteTunnelTokenPath(cfg *config.Config) string {
	return filepath.Join(tunnelStateDir(cfg), "cloudflared-token")
}

func defaultPersistentTunnelName(stackID, management string) string {
	base := "obol-stack-" + strings.TrimSpace(stackID)
	switch management {
	case tunnelManagementLocal:
		return base + "-local"
	case tunnelManagementRemote:
		return base + "-remote"
	default:
		return base
	}
}

func desiredPersistentTunnelName(stackID string, st *tunnelState, management string) string {
	normalized := normalizeTunnelState(st)
	if normalized != nil && normalized.ManagementMode == management && strings.TrimSpace(normalized.TunnelName) != "" {
		return normalized.TunnelName
	}

	return defaultPersistentTunnelName(stackID, management)
}

func normalizeTunnelState(st *tunnelState) *tunnelState {
	if st == nil {
		return nil
	}

	clone := *st

	if clone.ExposureMode == "" {
		switch clone.Mode {
		case legacyTunnelModeDNS:
			clone.ExposureMode = tunnelExposurePersistent
		case legacyTunnelModeQuick:
			clone.ExposureMode = tunnelExposureQuick
		default:
			if clone.Hostname != "" {
				clone.ExposureMode = tunnelExposurePersistent
			} else {
				clone.ExposureMode = tunnelExposureQuick
			}
		}
	}

	if clone.ManagementMode == "" {
		switch clone.ExposureMode {
		case tunnelExposurePersistent:
			if clone.AccountID != "" || clone.ZoneID != "" {
				clone.ManagementMode = tunnelManagementRemote
			} else {
				clone.ManagementMode = tunnelManagementLocal
			}
		default:
			clone.ManagementMode = tunnelManagementQuick
		}
	}

	clone.Mode = legacyTunnelMode(clone.ExposureMode)

	return &clone
}

func legacyTunnelMode(exposureMode string) string {
	if exposureMode == tunnelExposurePersistent {
		return legacyTunnelModeDNS
	}

	return legacyTunnelModeQuick
}

func loadTunnelState(cfg *config.Config) (*tunnelState, error) {
	data, err := os.ReadFile(tunnelStatePath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // no state file means tunnel was never provisioned; not an error
		}

		return nil, err
	}

	var st tunnelState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}

	return normalizeTunnelState(&st), nil
}

func saveTunnelState(cfg *config.Config, st *tunnelState) error {
	if st == nil {
		return errors.New("tunnel state is nil")
	}

	normalized := normalizeTunnelState(st)
	if err := os.MkdirAll(filepath.Dir(tunnelStatePath(cfg)), 0o700); err != nil {
		return err
	}

	normalized.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}

	// Contains non-secret metadata only, but keep it user-private by default.
	return os.WriteFile(tunnelStatePath(cfg), data, 0o600)
}

func saveRemoteTunnelToken(cfg *config.Config, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("tunnel token is empty")
	}

	if err := os.MkdirAll(tunnelStateDir(cfg), 0o700); err != nil {
		return err
	}

	return os.WriteFile(remoteTunnelTokenPath(cfg), []byte(token), 0o600)
}

func loadRemoteTunnelToken(cfg *config.Config) (string, error) {
	data, err := os.ReadFile(remoteTunnelTokenPath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}

		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func deleteRemoteTunnelToken(cfg *config.Config) error {
	if err := os.Remove(remoteTunnelTokenPath(cfg)); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// TunnelState is an exported alias so other packages (agent, openclaw)
// can read tunnel state without reaching into unexported types.
type TunnelState = tunnelState

// LoadTunnelState reads the persisted tunnel state from disk.
// Returns (nil, nil) if no state file exists.
func LoadTunnelState(cfg *config.Config) (*TunnelState, error) {
	return loadTunnelState(cfg)
}

func (st *tunnelState) IsPersistent() bool {
	normalized := normalizeTunnelState(st)
	return normalized != nil && normalized.ExposureMode == tunnelExposurePersistent
}

func (st *tunnelState) Management() string {
	normalized := normalizeTunnelState(st)
	if normalized == nil {
		return tunnelManagementQuick
	}

	return normalized.ManagementMode
}

func (st *tunnelState) DisplayMode() string {
	normalized := normalizeTunnelState(st)
	if normalized == nil || normalized.ExposureMode != tunnelExposurePersistent {
		return tunnelExposureQuick
	}

	switch normalized.ManagementMode {
	case tunnelManagementRemote:
		return "persistent-remote"
	case tunnelManagementLocal:
		return "persistent-local"
	default:
		return tunnelExposurePersistent
	}
}

func desiredRuntimeReplicas(st *tunnelState) int {
	if st != nil && st.IsPersistent() {
		return persistentReplicaCount
	}

	return quickReplicaCount
}

func tunnelModeAndURL(st *tunnelState) (mode, url string) {
	normalized := normalizeTunnelState(st)
	if normalized != nil && normalized.IsPersistent() && normalized.Hostname != "" {
		return tunnelExposurePersistent, "https://" + normalized.Hostname
	}

	return tunnelExposureQuick, ""
}
