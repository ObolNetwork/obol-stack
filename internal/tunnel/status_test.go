package tunnel

import (
	"testing"
	"time"
)

func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{3 * time.Hour, "3h"},
		{25 * time.Hour, "1d1h"},
		{48 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		if got := humanizeDuration(tc.d); got != tc.want {
			t.Errorf("humanizeDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestHumanTunnelMode(t *testing.T) {
	cases := map[string]string{
		tunnelExposureQuick: "Temporary (quick tunnel)",
		"persistent-remote": "Permanent (Cloudflare-managed)",
		"persistent-local":  "Permanent (browser-managed)",
	}
	for mode, want := range cases {
		if got := humanTunnelMode(mode); got != want {
			t.Errorf("humanTunnelMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestDefaultPersistentConnectorStatus(t *testing.T) {
	cases := []struct {
		name string
		st   *tunnelState
		want string
	}{
		{
			name: "remote managed",
			st: &tunnelState{
				ExposureMode:   tunnelExposurePersistent,
				ManagementMode: tunnelManagementRemote,
				Hostname:       "stack.example.com",
			},
			want: "not_probed",
		},
		{
			name: "local managed",
			st: &tunnelState{
				ExposureMode:   tunnelExposurePersistent,
				ManagementMode: tunnelManagementLocal,
				Hostname:       "stack.example.com",
			},
			want: "managed-locally",
		},
		{
			name: "quick tunnel",
			st:   &tunnelState{ExposureMode: tunnelExposureQuick},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultPersistentConnectorStatus(tc.st); got != tc.want {
				t.Fatalf("defaultPersistentConnectorStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseCloudflaredMetrics(t *testing.T) {
	metrics := `# HELP cloudflared_tunnel_total_requests Amount of requests
cloudflared_tunnel_total_requests 42
# HELP cloudflared_tunnel_request_errors Amount of errors
cloudflared_tunnel_request_errors 3
build_info{version="2026.1.0",goversion="go1.25"} 1
`
	var probe connectorProbe
	parseCloudflaredMetrics(metrics, &probe)
	if probe.RequestsServed != 42 {
		t.Errorf("RequestsServed = %d, want 42", probe.RequestsServed)
	}
	if probe.RequestErrors != 3 {
		t.Errorf("RequestErrors = %d, want 3", probe.RequestErrors)
	}
	if probe.Version != "2026.1.0" {
		t.Errorf("Version = %q, want 2026.1.0", probe.Version)
	}
}
