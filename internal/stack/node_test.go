package stack

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestK3sAgentJoinCommand(t *testing.T) {
	const (
		token  = "K10abc123::server:def456"
		server = "https://192.168.50.203:6443"
	)

	tests := []struct {
		name    string
		version string
		want    []string
		absent  []string
	}{
		{
			name:    "pinned version",
			version: "v1.35.5+k3s1",
			want: []string{
				"curl -sfL https://get.k3s.io | ",
				"INSTALL_K3S_VERSION=v1.35.5+k3s1 ",
				"K3S_URL=https://192.168.50.203:6443 ",
				"K3S_TOKEN='K10abc123::server:def456' ",
				"sh -s - agent",
			},
		},
		{
			name:    "unpinned version omits INSTALL_K3S_VERSION",
			version: "",
			want: []string{
				"K3S_URL=https://192.168.50.203:6443 ",
				"sh -s - agent",
			},
			absent: []string{"INSTALL_K3S_VERSION"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := K3sAgentJoinCommand(server, token, tt.version)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("join command missing %q\n  got: %s", w, got)
				}
			}

			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("join command should not contain %q\n  got: %s", a, got)
				}
			}
		})
	}
}

func TestParseK3sVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"standard two-line output", "k3s version v1.35.5+k3s1 (6a4781ad)\ngo version go1.25.9\n", "v1.35.5+k3s1"},
		{"single line no trailing newline", "k3s version v1.30.0+k3s1", "v1.30.0+k3s1"},
		{"empty", "", ""},
		{"unexpected format", "something else entirely", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseK3sVersion(tt.in); got != tt.want {
				t.Errorf("parseK3sVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestK3sServerURL(t *testing.T) {
	if got := K3sServerURL("https://example.local:6443"); got != "https://example.local:6443" {
		t.Errorf("override should be returned verbatim, got %q", got)
	}

	got := K3sServerURL("")
	if !strings.HasPrefix(got, "https://") || !strings.HasSuffix(got, ":6443") {
		t.Errorf("default server URL malformed: %q", got)
	}
}

func TestK3sNodeTokenPath(t *testing.T) {
	cfg := &config.Config{DataDir: "/tmp/obol-data"}

	got := K3sNodeTokenPath(cfg)
	if !strings.HasSuffix(got, "/k3s/server/node-token") {
		t.Errorf("token path = %q, want suffix /k3s/server/node-token", got)
	}

	if !strings.HasPrefix(got, "/tmp/obol-data") {
		t.Errorf("token path should be under the data-dir, got %q", got)
	}
}

func TestOutboundIP_NeverEmpty(t *testing.T) {
	if got := OutboundIP(); got == "" {
		t.Error("OutboundIP must never return empty (falls back to 127.0.0.1)")
	}
}
