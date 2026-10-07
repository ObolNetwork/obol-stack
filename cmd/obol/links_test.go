package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func writeK3dPorts(t *testing.T, dir, hostPort string) {
	t.Helper()
	body := "ports:\n  - port: " + hostPort + ":80\n    nodeFilters:\n      - loadbalancer\n"
	if err := os.WriteFile(filepath.Join(dir, "k3d.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrintOfferLinks_UsesStackIngressAndEncodedSlug(t *testing.T) {
	cfg := newTestConfig(t)
	writeK3dPorts(t, cfg.ConfigDir, "18080")

	var out bytes.Buffer
	u := ui.NewForTest(&out, &out) // non-TTY: never opens a browser
	printOfferLinks(cfg, u, "llm", "qwen", true)

	got := out.String()
	for _, want := range []string{
		"http://obol.stack:18080/marketplace/llm%2Fqwen",
		"http://obol.stack:18080/marketplace/listings",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// No browser opened → the one-time marker must survive for a later,
	// interactive run.
	if stack.MarkerExists(cfg, stack.MarkerFirstOfferOpened) {
		t.Error("first-offer marker written although no browser opened")
	}
}

func TestPrintStackUpLinks_NonInteractiveKeepsMarker(t *testing.T) {
	cfg := newTestConfig(t)
	writeK3dPorts(t, cfg.ConfigDir, "80")

	var out bytes.Buffer
	printStackUpLinks(cfg, ui.NewForTest(&out, &out))
	if stack.MarkerExists(cfg, stack.MarkerUIOpened) {
		t.Error("UI marker written although no browser opened")
	}
}
