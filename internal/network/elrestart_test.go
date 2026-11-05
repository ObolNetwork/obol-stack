package network

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestWarnELRestart(t *testing.T) {
	orig := liveELMemoryFn
	t.Cleanup(func() { liveELMemoryFn = orig })

	cases := []struct {
		name    string
		memory  string
		found   bool
		stopped bool
		want    bool
	}{
		{"no requests yet", "", true, false, true},
		{"already sized", "12Gi", true, false, false},
		{"not deployed", "", false, false, false},
		{"stopped", "", true, true, false},
	}
	for _, c := range cases {
		liveELMemoryFn = func(*config.Config, string) (string, bool) { return c.memory, c.found }

		var out bytes.Buffer
		u := ui.NewForTest(&out, &out)
		warnELRestart(&config.Config{}, u, Deployment{Namespace: "ethereum-x", ExecutionClient: "reth", Stopped: c.stopped})

		if got := strings.Contains(out.String(), "also restarts reth"); got != c.want {
			t.Errorf("%s: warned = %v, want %v (output %q)", c.name, got, c.want, out.String())
		}
	}
}
