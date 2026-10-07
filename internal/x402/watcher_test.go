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
facilitatorURL: "https://x402.gcp.obol.tech"
routes:
  - pattern: "/rpc/*"
    price: "0.0001"
`

func writeConfig(t *testing.T, path, content string) {
	t.Helper()

	// Replace the file atomically (write a temp file, rename over), the way
	// kubelet swaps a ConfigMap volume. An in-place os.WriteFile truncates
	// first, so a poll landing mid-write would load an empty file — which
	// parses as a valid zero-route config.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// The watcher detects changes by mtime alone, and filesystem timestamps
	// can be coarser than the gap between two writes in a test. Give every
	// rewrite a strictly later mtime so the change is always observable.
	if prev, err := os.Stat(path); err == nil {
		next := prev.ModTime().Add(time.Second)
		if err := os.Chtimes(tmp, next, next); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func newWatcherTestVerifier(t *testing.T) *Verifier {
	t.Helper()

	v, err := NewVerifier(&PricingConfig{
		Wallet:         "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://x402.gcp.obol.tech",
		Routes:         []RouteRule{{Pattern: "/rpc/*", Price: "0.0001"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	return v
}

// startWatcher runs the WatchConfig loop for v and returns a channel of
// per-poll outcomes, so tests synchronise on what the watcher actually did
// rather than on wall-clock sleeps. The returned func cancels the watcher and
// waits for it to exit.
func startWatcher(t *testing.T, path string, v *Verifier) (<-chan watchEvent, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan watchEvent)
	done := make(chan struct{})

	go func() {
		defer close(done)
		watchConfig(ctx, path, time.Millisecond, verifierReloader(v), func(ev watchEvent) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
	}()

	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)

	return events, stop
}

// awaitEvent blocks until the watcher reports want, failing on deadline.
func awaitEvent(t *testing.T, events <-chan watchEvent, want watchEvent) {
	t.Helper()

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev == want {
				return
			}
		case <-deadline:
			t.Fatalf("watcher never reported event %d", want)
		}
	}
}

func TestWatchConfig_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v := newWatcherTestVerifier(t)
	events, _ := startWatcher(t, path, v)

	// Initial load on the first poll.
	awaitEvent(t, events, watchApplied)

	// Write updated config with a new route.
	updatedYAML := `wallet: "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
chain: "base-sepolia"
facilitatorURL: "https://x402.gcp.obol.tech"
routes:
  - pattern: "/rpc/*"
    price: "0.0001"
  - pattern: "/api/*"
    price: "0.005"
`
	writeConfig(t, path, updatedYAML)

	awaitEvent(t, events, watchApplied)

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

	v := newWatcherTestVerifier(t)
	events, stop := startWatcher(t, path, v)

	awaitEvent(t, events, watchApplied)
	loaded := v.config.Load()

	// Every subsequent poll of the untouched file must be a no-op.
	for i := 0; i < 3; i++ {
		select {
		case ev := <-events:
			if ev != watchUnchanged {
				t.Fatalf("poll %d: got event %d, want unchanged", i, ev)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("watcher stopped polling after %d polls", i)
		}
	}
	stop()

	cfg := v.config.Load()
	if cfg != loaded {
		t.Error("config was swapped although the file did not change")
	}

	if len(cfg.Routes) != 1 {
		t.Errorf("expected 1 route (unchanged), got %d", len(cfg.Routes))
	}
}

func TestWatchConfig_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeConfig(t, path, validWatcherYAML)

	v := newWatcherTestVerifier(t)
	events, stop := startWatcher(t, path, v)

	awaitEvent(t, events, watchApplied)
	loaded := v.config.Load()

	// Write invalid YAML — watcher should log error but keep old config.
	writeConfig(t, path, "{{bad yaml: [")

	awaitEvent(t, events, watchLoadError)
	stop()

	cfg := v.config.Load()
	if cfg == nil {
		t.Fatal("config should not be nil after bad reload")
	}
	if cfg != loaded {
		t.Error("config was swapped by an invalid reload")
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

	v := newWatcherTestVerifier(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		WatchConfig(ctx, path, v, time.Millisecond)
		close(done)
	}()

	// Cancel while the watcher is running (or about to start); it must
	// return promptly either way.
	cancel()

	select {
	case <-done:
		// WatchConfig returned cleanly.
	case <-time.After(10 * time.Second):
		t.Fatal("WatchConfig did not return after context cancellation")
	}
}

func TestWatchConfig_MissingFile(t *testing.T) {
	v := newWatcherTestVerifier(t)
	loaded := v.config.Load()

	// Point at a non-existent path — watcher should log but not crash.
	events, stop := startWatcher(t, filepath.Join(t.TempDir(), "missing.yaml"), v)

	for i := 0; i < 3; i++ {
		awaitEvent(t, events, watchStatError)
	}
	stop()

	// Original config should be preserved.
	cfg := v.config.Load()
	if cfg != loaded {
		t.Fatal("config should be the original — missing file must not swap it")
	}

	if len(cfg.Routes) != 1 {
		t.Errorf("expected original config (1 route), got %d", len(cfg.Routes))
	}
}
