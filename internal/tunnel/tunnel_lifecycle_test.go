package tunnel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()

	return &config.Config{
		ConfigDir: filepath.Join(dir, "config"),
		DataDir:   filepath.Join(dir, "data"),
		BinDir:    filepath.Join(dir, "bin"),
	}
}

// ---------------------------------------------------------------------------
// State persistence
// ---------------------------------------------------------------------------

func TestTunnelState_RoundTrip(t *testing.T) {
	cfg := testConfig(t)

	st := &tunnelState{Mode: "quick"}

	if err := saveTunnelState(cfg, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Mode != "quick" {
		t.Errorf("mode = %q, want quick", got.Mode)
	}
	if got.ExposureMode != tunnelExposureQuick {
		t.Errorf("exposure_mode = %q, want quick", got.ExposureMode)
	}
	if got.ManagementMode != tunnelManagementQuick {
		t.Errorf("management_mode = %q, want quick", got.ManagementMode)
	}
	if got.Hostname != "" {
		t.Errorf("hostname = %q, want empty", got.Hostname)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set by save")
	}
}

func TestTunnelState_DNSMode(t *testing.T) {
	cfg := testConfig(t)

	st := &tunnelState{
		Mode:       "dns",
		Hostname:   "stack.example.com",
		AccountID:  "acct-123",
		ZoneID:     "zone-456",
		TunnelID:   "tun-789",
		TunnelName: "my-tunnel",
	}

	if err := saveTunnelState(cfg, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Mode != "dns" {
		t.Errorf("mode = %q, want dns", got.Mode)
	}
	if got.ExposureMode != tunnelExposurePersistent {
		t.Errorf("exposure_mode = %q, want persistent", got.ExposureMode)
	}
	if got.ManagementMode != tunnelManagementRemote {
		t.Errorf("management_mode = %q, want remote", got.ManagementMode)
	}
	if got.TunnelID != "tun-789" {
		t.Errorf("tunnel_id = %q, want tun-789", got.TunnelID)
	}
}

func TestTunnelState_LegacyQuickHostnameStaysQuick(t *testing.T) {
	cfg := testConfig(t)

	if err := os.MkdirAll(filepath.Dir(tunnelStatePath(cfg)), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{"mode":"quick","hostname":"annual-arc-abilities-lenses.trycloudflare.com"}`
	if err := os.WriteFile(tunnelStatePath(cfg), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Mode != legacyTunnelModeQuick {
		t.Fatalf("mode = %q, want %q", got.Mode, legacyTunnelModeQuick)
	}
	if got.ExposureMode != tunnelExposureQuick {
		t.Fatalf("exposure_mode = %q, want %q", got.ExposureMode, tunnelExposureQuick)
	}
	if got.ManagementMode != tunnelManagementQuick {
		t.Fatalf("management_mode = %q, want %q", got.ManagementMode, tunnelManagementQuick)
	}
	if got.IsPersistent() {
		t.Fatal("legacy quick tunnel with hostname should not be treated as persistent")
	}
}

func TestTunnelState_NotExist(t *testing.T) {
	cfg := testConfig(t)

	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load should not error on missing file: %v", err)
	}

	if got != nil {
		t.Fatalf("expected nil state for missing file, got %+v", got)
	}
}

func TestTunnelState_Overwrite(t *testing.T) {
	cfg := testConfig(t)

	st1 := &tunnelState{Mode: "quick"}
	if err := saveTunnelState(cfg, st1); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	st2 := &tunnelState{Mode: "dns", Hostname: "new.example.com"}
	if err := saveTunnelState(cfg, st2); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got, err := loadTunnelState(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Mode != "dns" || got.Hostname != "new.example.com" {
		t.Errorf("expected overwritten state, got %+v", got)
	}
}

func TestTunnelState_FilePermissions(t *testing.T) {
	cfg := testConfig(t)

	st := &tunnelState{Mode: "quick"}
	if err := saveTunnelState(cfg, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(tunnelStatePath(cfg))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

// ---------------------------------------------------------------------------
// Mode and URL derivation
// ---------------------------------------------------------------------------

func TestTunnelModeAndURL(t *testing.T) {
	tests := []struct {
		name     string
		st       *tunnelState
		wantMode string
		wantURL  string
	}{
		{
			name:     "nil state",
			st:       nil,
			wantMode: "quick",
			wantURL:  "",
		},
		{
			name:     "quick mode (no hostname)",
			st:       &tunnelState{Mode: "quick"},
			wantMode: "quick",
			wantURL:  "",
		},
		{
			name:     "dns mode with hostname",
			st:       &tunnelState{Mode: "dns", Hostname: "stack.example.com"},
			wantMode: "persistent",
			wantURL:  "https://stack.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, url := tunnelModeAndURL(tt.st)
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}

			if url != tt.wantURL {
				t.Errorf("url = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestDesiredPersistentTunnelName(t *testing.T) {
	tests := []struct {
		name       string
		stackID    string
		state      *tunnelState
		management string
		want       string
	}{
		{
			name:       "new local tunnel gets local suffix",
			stackID:    "sunny-otter",
			management: tunnelManagementLocal,
			want:       "obol-stack-sunny-otter-local",
		},
		{
			name:       "new remote tunnel gets remote suffix",
			stackID:    "sunny-otter",
			management: tunnelManagementRemote,
			want:       "obol-stack-sunny-otter-remote",
		},
		{
			name:       "same management reuses existing tunnel name",
			stackID:    "sunny-otter",
			management: tunnelManagementRemote,
			state: &tunnelState{
				ExposureMode:   tunnelExposurePersistent,
				ManagementMode: tunnelManagementRemote,
				TunnelName:     "obol-stack-sunny-otter",
			},
			want: "obol-stack-sunny-otter",
		},
		{
			name:       "management handoff does not reuse opposite mode tunnel name",
			stackID:    "sunny-otter",
			management: tunnelManagementLocal,
			state: &tunnelState{
				ExposureMode:   tunnelExposurePersistent,
				ManagementMode: tunnelManagementRemote,
				TunnelName:     "obol-stack-sunny-otter",
			},
			want: "obol-stack-sunny-otter-local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desiredPersistentTunnelName(tt.stackID, tt.state, tt.management)
			if got != tt.want {
				t.Fatalf("desiredPersistentTunnelName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Auto-stop decision logic
// ---------------------------------------------------------------------------

// ShouldAutoStopTunnel mirrors the decision logic in cmd/obol/sell.go's
// delete command: stop the quick tunnel only when no ServiceOffers remain
// and the tunnel mode is NOT dns (persistent tunnels shouldn't be auto-stopped).
func shouldAutoStopTunnel(remainingOffers string, st *tunnelState) bool {
	// If there are remaining offers, don't stop.
	if remainingOffers != "[]" && remainingOffers != "" {
		return false
	}
	// Persistent tunnels should not be auto-stopped.
	if st != nil && st.IsPersistent() {
		return false
	}
	// Quick tunnels with no remaining offers: stop.
	return true
}

func TestShouldAutoStopTunnel(t *testing.T) {
	tests := []struct {
		name      string
		remaining string
		state     *tunnelState
		want      bool
	}{
		{
			name:      "offers remain — don't stop",
			remaining: `[{"metadata":{"name":"my-offer"}}]`,
			state:     nil,
			want:      false,
		},
		{
			name:      "empty list, quick tunnel — stop",
			remaining: "[]",
			state:     &tunnelState{Mode: "quick"},
			want:      true,
		},
		{
			name:      "empty list, nil state — stop",
			remaining: "[]",
			state:     nil,
			want:      true,
		},
		{
			name:      "empty list, dns tunnel — don't stop",
			remaining: "[]",
			state:     &tunnelState{Mode: "dns", Hostname: "stack.example.com"},
			want:      false,
		},
		{
			name:      "empty string, quick tunnel — stop",
			remaining: "",
			state:     &tunnelState{Mode: "quick"},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldAutoStopTunnel(tt.remaining, tt.state)
			if got != tt.want {
				t.Errorf("shouldAutoStopTunnel(%q, %v) = %v, want %v", tt.remaining, tt.state, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Exported LoadTunnelState
// ---------------------------------------------------------------------------

func TestLoadTunnelState_Exported(t *testing.T) {
	cfg := testConfig(t)

	// No state file → (nil, nil).
	st, err := LoadTunnelState(cfg)
	if err != nil || st != nil {
		t.Fatalf("expected (nil, nil), got (%v, %v)", st, err)
	}

	// Write state, then load via exported function.
	if err := saveTunnelState(cfg, &tunnelState{Mode: "quick"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	st, err = LoadTunnelState(cfg)
	if err != nil {
		t.Fatalf("LoadTunnelState: %v", err)
	}

	if st.Hostname != "" {
		t.Errorf("hostname = %q, want empty", st.Hostname)
	}
}

// ---------------------------------------------------------------------------
// UpdatedAt timestamp
// ---------------------------------------------------------------------------

func TestTunnelState_UpdatedAtRefreshed(t *testing.T) {
	cfg := testConfig(t)

	before := time.Now().UTC().Add(-time.Second)

	st := &tunnelState{Mode: "quick"}
	if err := saveTunnelState(cfg, st); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, _ := loadTunnelState(cfg)
	if got.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt %v should be after %v", got.UpdatedAt, before)
	}
}

// ---------------------------------------------------------------------------
// ConfirmQuickTunnelLoss
// ---------------------------------------------------------------------------

func TestConfirmQuickTunnelLoss_PersistentSkipsWarning(t *testing.T) {
	cfg := testConfig(t)
	if err := saveTunnelState(cfg, &tunnelState{Mode: "dns", Hostname: "stack.example.com"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	if !ConfirmQuickTunnelLoss(cfg, ui.New(false), "https://old.trycloudflare.com", "test") {
		t.Error("persistent DNS tunnel should skip the warning and return true")
	}
}

func TestConfirmQuickTunnelLoss_EmptyURLSkips(t *testing.T) {
	if !ConfirmQuickTunnelLoss(testConfig(t), ui.New(false), "", "test") {
		t.Error("empty currentURL should skip the warning and return true")
	}
}

func TestConfirmQuickTunnelLoss_NonInteractivePassesThrough(t *testing.T) {
	// Tests run without a TTY, so Confirm short-circuits to its default (true).
	// The helper still prints the warning; here we just verify it does not block.
	if !ConfirmQuickTunnelLoss(testConfig(t), ui.New(false), "https://old.trycloudflare.com", "test") {
		t.Error("non-interactive Confirm should pass through with default-yes")
	}
}
