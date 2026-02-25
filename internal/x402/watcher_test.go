package x402

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validWatcherYAML = `wallet: "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
chain: "base-sepolia"
facilitatorURL: "https://facilitator.x402.rs"
routes:
  - pattern: "/rpc/*"
    price: "0.0001"
`

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWatchConfig_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go WatchConfig(ctx, path, v, 10*time.Millisecond)

	// Wait for initial load to happen.
	time.Sleep(30 * time.Millisecond)

	// Write updated config with a new route.
	updatedYAML := `wallet: "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
chain: "base-sepolia"
facilitatorURL: "https://facilitator.x402.rs"
routes:
  - pattern: "/rpc/*"
    price: "0.0001"
  - pattern: "/api/*"
    price: "0.005"
`
	writeConfig(t, path, updatedYAML)

	// Wait for the watcher to detect the change.
	time.Sleep(50 * time.Millisecond)

	cfg := v.config.Load()
	if cfg == nil {
		t.Fatal("config is nil after reload")
	}
	if len(cfg.Routes) != 2 {
		t.Errorf("expected 2 routes after reload, got %d", len(cfg.Routes))
	}
}

func TestWatchConfig_IgnoresUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go WatchConfig(ctx, path, v, 10*time.Millisecond)

	// Wait for multiple ticks without changing the file.
	time.Sleep(50 * time.Millisecond)

	// Config should still be loaded (initial load on first tick), but routes unchanged.
	cfg := v.config.Load()
	if cfg == nil {
		t.Fatal("config should not be nil")
	}
	if len(cfg.Routes) != 1 {
		t.Errorf("expected 1 route (unchanged), got %d", len(cfg.Routes))
	}
}

func TestWatchConfig_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go WatchConfig(ctx, path, v, 10*time.Millisecond)

	// Wait for initial load.
	time.Sleep(30 * time.Millisecond)

	// Write invalid YAML — watcher should log error but keep old config.
	writeConfig(t, path, "{{bad yaml: [")

	time.Sleep(50 * time.Millisecond)

	cfg := v.config.Load()
	if cfg == nil {
		t.Fatal("config should not be nil after bad reload")
	}
	// Old config should be preserved.
	if len(cfg.Routes) != 1 {
		t.Errorf("expected old config (1 route) preserved after bad YAML, got %d routes", len(cfg.Routes))
	}
}

func TestWatchConfig_CancelContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		WatchConfig(ctx, path, v, 10*time.Millisecond)
		close(done)
	}()

	// Let the watcher run briefly, then cancel.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// WatchConfig returned cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("WatchConfig did not return after context cancellation")
	}
}

func TestWatchConfig_MissingFile(t *testing.T) {
	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.x402.rs",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Point at a non-existent path — watcher should log but not crash.
	go WatchConfig(ctx, "/nonexistent/config.yaml", v, 10*time.Millisecond)

	// Let it tick a few times with missing file.
	time.Sleep(50 * time.Millisecond)

	// Original config should be preserved.
	cfg := v.config.Load()
	if cfg == nil {
		t.Fatal("config should not be nil — original should be preserved")
	}
	if len(cfg.Routes) != 1 {
		t.Errorf("expected original config (1 route), got %d", len(cfg.Routes))
	}
}
