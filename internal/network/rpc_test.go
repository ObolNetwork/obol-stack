package network

import (
	"strings"
	"testing"
)

// Same config must produce the same patch (a no-op that doesn't roll eRPC);
// a different config must produce a different one.
func TestERPCConfigHashPatch(t *testing.T) {
	a := erpcConfigHashPatch([]byte("projects: [a]\n"))
	if a != erpcConfigHashPatch([]byte("projects: [a]\n")) {
		t.Fatal("hash patch not stable for identical config")
	}

	if a == erpcConfigHashPatch([]byte("projects: [b]\n")) {
		t.Fatal("hash patch unchanged for different config")
	}

	if !strings.Contains(a, `"obol.org/config-hash"`) {
		t.Fatalf("patch missing annotation key: %s", a)
	}
}
