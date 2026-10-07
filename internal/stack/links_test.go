package stack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestDashboardURL(t *testing.T) {
	tests := []struct {
		name, base, kind, ns, n, want string
	}{
		{"root", "http://obol.stack", LinkRoot, "", "", "http://obol.stack/"},
		{"root trims trailing slash", "http://obol.stack:8080/", LinkRoot, "", "", "http://obol.stack:8080/"},
		{"offer is one encoded segment", "http://obol.stack", LinkOffer, "llm", "qwen-gated", "http://obol.stack/marketplace/llm%2Fqwen-gated"},
		{"offer on alt port", "http://obol.stack:18080", LinkOffer, "default", "my-api", "http://obol.stack:18080/marketplace/default%2Fmy-api"},
		{"offer without ns falls back to listings", "http://obol.stack", LinkOffer, "", "x", "http://obol.stack/marketplace/listings"},
		{"storefront", "http://obol.stack", LinkStorefront, "", "", "http://obol.stack/storefront"},
		{"purchases", "http://obol.stack", LinkPurchases, "llm", "x", "http://obol.stack/marketplace/purchases"},
		{"listings", "http://obol.stack", LinkListings, "", "", "http://obol.stack/marketplace/listings"},
		{"agent falls back to root", "http://obol.stack", LinkAgent, "agent-foo", "foo", "http://obol.stack/"},
		{"unknown kind falls back to root", "http://obol.stack", "networks", "", "", "http://obol.stack/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DashboardURL(tt.base, tt.kind, tt.ns, tt.n); got != tt.want {
				t.Errorf("DashboardURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOnFirstRun(t *testing.T) {
	cfg := &config.Config{ConfigDir: filepath.Join(t.TempDir(), "cfg")}

	// A first run where the browser could not open must not burn the marker.
	if OnFirstRun(cfg, MarkerUIOpened, func() bool { return false }) {
		t.Fatal("OnFirstRun returned true although open failed")
	}
	if MarkerExists(cfg, MarkerUIOpened) {
		t.Fatal("marker written although open failed")
	}

	calls := 0
	open := func() bool { calls++; return true }
	if !OnFirstRun(cfg, MarkerUIOpened, open) {
		t.Fatal("first successful run should open")
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, MarkerUIOpened)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if OnFirstRun(cfg, MarkerUIOpened, open) {
		t.Fatal("second run should not open again")
	}
	if calls != 1 {
		t.Fatalf("open called %d times, want 1", calls)
	}

	// Markers are independent.
	if !OnFirstRun(cfg, MarkerFirstOfferOpened, open) {
		t.Fatal("first-offer marker should be independent of the UI marker")
	}
}
