package network

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestWarnClientRestarts(t *testing.T) {
	orig := liveMemoryFn
	t.Cleanup(func() { liveMemoryFn = orig })

	d := Deployment{Namespace: "ethereum-x", Network: "hoodi", Mode: modeFull, ExecutionClient: "reth", ConsensusClient: "lighthouse"}

	type live struct{ req, lim string }
	cases := []struct {
		name    string
		el, cl  *live // nil = no StatefulSet
		stopped bool
		want    string // "" = no warning
	}{
		{"pre-sizing deployment", &live{"", ""}, &live{"3Gi", ""}, false, "restarts reth (limit 4Gi) and lighthouse (limit 16Gi)"},
		{"already sized", &live{"1Gi", "4Gi"}, &live{"3Gi", "16Gi"}, false, ""},
		{"only CL resized", &live{"1Gi", "4Gi"}, &live{"2Gi", "5Gi"}, false, "restarts lighthouse (limit 16Gi) once"},
		{"not deployed", nil, nil, false, ""},
		{"stopped", &live{"", ""}, &live{"", ""}, true, ""},
	}
	for _, c := range cases {
		liveMemoryFn = func(_ *config.Config, _, sts string) (string, string, bool) {
			l := c.el
			if sts == beaconService {
				l = c.cl
			}
			if l == nil {
				return "", "", false
			}
			return l.req, l.lim, true
		}

		dd := d
		dd.Stopped = c.stopped

		var out bytes.Buffer
		warnClientRestarts(&config.Config{}, ui.NewForTest(&out, &out), dd)

		got := out.String()
		if c.want == "" && got != "" {
			t.Errorf("%s: unexpected warning %q", c.name, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: warning %q, want it to contain %q", c.name, got, c.want)
		}
	}
}
