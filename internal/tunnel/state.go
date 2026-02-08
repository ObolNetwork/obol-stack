package tunnel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

type tunnelState struct {
	Mode       string    `json:"mode"` // "quick", "local", "remote" (or legacy: "dns")
	Hostname   string    `json:"hostname"`
	AccountID  string    `json:"account_id,omitempty"`
	ZoneID     string    `json:"zone_id,omitempty"`
	TunnelID   string    `json:"tunnel_id,omitempty"`
	TunnelName string    `json:"tunnel_name,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func tunnelStatePath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "tunnel", "cloudflared.json")
}

func loadTunnelState(cfg *config.Config) (*tunnelState, error) {
	data, err := os.ReadFile(tunnelStatePath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var st tunnelState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func saveTunnelState(cfg *config.Config, st *tunnelState) error {
	if err := os.MkdirAll(filepath.Dir(tunnelStatePath(cfg)), 0755); err != nil {
		return err
	}
	st.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	// Contains non-secret metadata only, but keep it user-private by default.
	return os.WriteFile(tunnelStatePath(cfg), data, 0600)
}

func tunnelModeAndURL(st *tunnelState) (mode, url string) {
	if st == nil {
		return "quick", ""
	}

	if st.Hostname != "" {
		url = "https://" + st.Hostname
	}

	if st.Mode != "" {
		return st.Mode, url
	}

	// Back-compat for older state files that only populated Hostname.
	if st.Hostname != "" {
		return "dns", url
	}
	return "quick", url
}
