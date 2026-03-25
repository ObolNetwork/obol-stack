package tunnel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
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

	st := &tunnelState{
		Mode:     "quick",
		Hostname: "abc-def.trycloudflare.com",
	}

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

	if got.Hostname != "abc-def.trycloudflare.com" {
		t.Errorf("hostname = %q, want abc-def.trycloudflare.com", got.Hostname)
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

	if got.TunnelID != "tun-789" {
		t.Errorf("tunnel_id = %q, want tun-789", got.TunnelID)
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

	st1 := &tunnelState{Mode: "quick", Hostname: "old.trycloudflare.com"}
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

	st := &tunnelState{Mode: "quick", Hostname: "test.trycloudflare.com"}
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
			wantMode: "dns",
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
	// DNS (persistent) tunnels should not be auto-stopped.
	if st != nil && st.Mode == "dns" {
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
	if err := saveTunnelState(cfg, &tunnelState{
		Mode:     "quick",
		Hostname: "exported-test.trycloudflare.com",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	st, err = LoadTunnelState(cfg)
	if err != nil {
		t.Fatalf("LoadTunnelState: %v", err)
	}

	if st.Hostname != "exported-test.trycloudflare.com" {
		t.Errorf("hostname = %q, want exported-test.trycloudflare.com", st.Hostname)
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
