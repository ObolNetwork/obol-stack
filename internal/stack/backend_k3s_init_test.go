package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// TestK3sBackend_Init_SubstitutesNodeSANs verifies that Init renders the
// embedded k3s-config.yaml with every {{...}} placeholder resolved and the
// node's LAN IP + hostname injected into the tls-san block, so a worker node
// can join the server by either address.
func TestK3sBackend_Init_SubstitutesNodeSANs(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: dir,
		DataDir:   filepath.Join(dir, "data"),
	}

	b := &K3sBackend{}
	if err := b.Init(cfg, ui.New(false), "teststack"); err != nil {
		t.Fatalf("Init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, k3sConfigFile))
	if err != nil {
		t.Fatalf("read rendered k3s config: %v", err)
	}

	rendered := string(data)
	if strings.Contains(rendered, "{{") {
		t.Errorf("rendered k3s config still has an unsubstituted placeholder:\n%s", rendered)
	}

	if !strings.Contains(rendered, "tls-san") {
		t.Fatal("rendered k3s config has no tls-san block")
	}

	for _, want := range []string{OutboundIP(), nodeHostname()} {
		if !strings.Contains(rendered, want) {
			t.Errorf("tls-san missing %q\n%s", want, rendered)
		}
	}
}
