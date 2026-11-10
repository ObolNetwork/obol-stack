package stack

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestDefaultNodeMemory(t *testing.T) {
	cases := []struct{ total, want int64 }{
		{31 * gib, 31*gib - 31*gib/4},
		{16 * gib, 12 * gib},
		{64 * gib, 56 * gib},
		{8 * gib, 0},
	}
	for _, c := range cases {
		if got := defaultNodeMemory(c.total); got != c.want {
			t.Errorf("defaultNodeMemory(%s) = %s, want %s", humanGiB(c.total), humanGiB(got), humanGiB(c.want))
		}
	}
}

func TestParseNodeMemory(t *testing.T) {
	cases := []struct {
		in      string
		bytes   int64
		auto    bool
		wantErr bool
	}{
		{"", 0, true, false},
		{"auto", 0, true, false},
		{"off", 0, false, false},
		{"0", 0, false, false},
		{"22g", 22 * gib, false, false},
		{"22Gi", 22 * gib, false, false},
		{"22GiB", 22 * gib, false, false},
		{"22528m", 22 * gib, false, false},
		{"1g", 0, false, true},
		{"lots", 0, false, true},
	}
	for _, c := range cases {
		b, auto, err := parseNodeMemory(c.in)
		if (err != nil) != c.wantErr || b != c.bytes || auto != c.auto {
			t.Errorf("parseNodeMemory(%q) = %d, %v, %v; want %d, %v, error %v", c.in, b, auto, err, c.bytes, c.auto, c.wantErr)
		}
	}
}

func TestConfiguredNodeMemory(t *testing.T) {
	rendered := strings.ReplaceAll(embed.K3dConfig, "{{SERVERS_MEMORY}}", k3dSize(22*gib))
	if b, ok := configuredNodeMemory(rendered); !ok || b != 22*gib {
		t.Errorf("rendered config: got %d, %v; want 22 GiB", b, ok)
	}

	off := strings.ReplaceAll(embed.K3dConfig, "{{SERVERS_MEMORY}}", "")
	if b, ok := configuredNodeMemory(off); !ok || b != 0 {
		t.Errorf("cap off: got %d, %v; want 0, true", b, ok)
	}

	if _, ok := configuredNodeMemory("options:\n  runtime:\n    labels: []\n"); ok {
		t.Error("legacy config without serversMemory reported as present")
	}
}

func TestEnforceNodeMemory(t *testing.T) {
	origTotal, origDocker := dockerMemTotalFn, dockerOutputFn
	t.Cleanup(func() { dockerMemTotalFn, dockerOutputFn = origTotal, origDocker })

	dockerMemTotalFn = func() (int64, error) { return 31 * gib, nil }

	cases := []struct {
		name       string
		config     string
		inspect    string
		stats      string
		wantUpdate string // "" = no docker update
	}{
		{"new cluster, swap on", `serversMemory: "22528m"`, "23622320128 47244640256", "", "--memory 23622320128 --memory-swap 23622320128"},
		{"already capped, no swap", `serversMemory: "22528m"`, "23622320128 23622320128", "", ""},
		{"hand-set cap kept", `serversMemory: "20480m"`, "23622320128 23622320128", "", ""},
		{"legacy cluster gets default", "options: {}", "0 0", "15.2GiB / 31GiB", "--memory 24964497408 --memory-swap 24964497408"},
		{"legacy cluster using nearly the cap waits", "options: {}", "0 0", "22.1GiB / 31GiB", ""},
		{"legacy cluster, stats unreadable", "options: {}", "0 0", "", "--memory 24964497408 --memory-swap 24964497408"},
		{"cap off", `serversMemory: ""`, "0 0", "", ""},
	}
	for _, c := range cases {
		var update string
		dockerOutputFn = func(args ...string) (string, error) {
			switch args[0] {
			case "ps":
				return "k3d-obol-stack-x-server-0", nil
			case "inspect":
				return c.inspect, nil
			case "stats":
				if c.stats == "" {
					return "", errors.New("no stats")
				}
				return c.stats, nil
			case "update":
				update = strings.Join(args[1:len(args)-1], " ")
				return "", nil
			}
			return "", errors.New("unexpected")
		}

		var out bytes.Buffer
		enforceNodeMemory(ui.NewForTest(&out, &out), "obol-stack-x", c.config)

		if update != c.wantUpdate {
			t.Errorf("%s: docker update %q, want %q", c.name, update, c.wantUpdate)
		}
	}
}
