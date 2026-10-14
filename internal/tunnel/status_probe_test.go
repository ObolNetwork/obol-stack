package tunnel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// healthyRunningResult is a running tunnel whose replicas and connector are
// all healthy, so only the public probe fields decide the summary.
func healthyRunningResult(mode string) tunnelStatusResult {
	r := tunnelStatusResult{
		Mode:              mode,
		ExposureMode:      mode,
		URL:               "https://stack.example.com",
		DesiredReplicas:   1,
		ReadyReplicas:     1,
		AvailableReplicas: 1,
	}
	if mode == tunnelExposurePersistent {
		r.ManagementMode = tunnelManagementRemote
		r.ConnectorStatus = "connected"
	}
	return r
}

func TestSummarizeTunnelStatus_PublicProbe(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*tunnelStatusResult)
		want   string
	}{
		{"probe skipped (--no-probe)", func(r *tunnelStatusResult) {}, "active"},
		{"probe reachable", func(r *tunnelStatusResult) {
			r.PublicProbed, r.PublicReachable, r.PublicHTTPStatus = true, true, 200
		}, "active"},
		{"probe HTTP error", func(r *tunnelStatusResult) {
			r.PublicProbed, r.PublicHTTPStatus = true, 530
		}, "degraded"},
		{"probe transport error", func(r *tunnelStatusResult) {
			r.PublicProbed, r.PublicProbeError = true, "dial tcp: no such host"
		}, "degraded"},
	}
	for _, mode := range []string{tunnelExposurePersistent, tunnelExposureQuick} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				r := healthyRunningResult(mode)
				tc.mutate(&r)
				if got := summarizeTunnelStatus(r); got != tc.want {
					t.Fatalf("summarizeTunnelStatus = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

func TestSummarizeTunnelStatus_ReplicaStateWinsOverProbe(t *testing.T) {
	r := healthyRunningResult(tunnelExposurePersistent)
	r.ReadyReplicas = 0
	r.PublicProbed = true
	if got := summarizeTunnelStatus(r); got != "starting" {
		t.Fatalf("summarizeTunnelStatus = %q, want starting", got)
	}
}

func TestProbePublicURL(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	code, err := probePublicURL(bad.URL)
	if err != nil || code != http.StatusBadGateway {
		t.Fatalf("probePublicURL = %d, %v; want 502, nil", code, err)
	}

	bad.Close()
	if _, err := probePublicURL(bad.URL); err == nil {
		t.Fatal("probePublicURL on a closed server returned nil error")
	}
}

func TestStatusOutput_FailedPublicProbe(t *testing.T) {
	r := healthyRunningResult(tunnelExposurePersistent)
	r.PublicProbed = true
	r.PublicProbeError = "dial tcp: no such host"
	r.Status = summarizeTunnelStatus(r)

	var stdout, stderr bytes.Buffer
	u := ui.NewForTest(&stdout, &stderr)
	printStatusReport(u, r, time.Now())
	printStatusHints(u, r)
	out := stdout.String() + stderr.String()

	for _, want := range []string{
		"degraded",
		"unreachable (dial tcp: no such host)",
		"Troubleshooting:",
		"obol tunnel logs",
		"Public Hostname routes to http://traefik.traefik.svc.cluster.local:80",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status"] != "degraded" || decoded["public_probed"] != true {
		t.Fatalf("JSON status/public_probed = %v/%v, want degraded/true", decoded["status"], decoded["public_probed"])
	}
}

func TestStatusOutput_ActiveSkipsTroubleshooting(t *testing.T) {
	r := healthyRunningResult(tunnelExposurePersistent)
	r.Status = summarizeTunnelStatus(r)

	var stdout, stderr bytes.Buffer
	u := ui.NewForTest(&stdout, &stderr)
	printStatusHints(u, r)
	if out := stdout.String() + stderr.String(); strings.Contains(out, "Troubleshooting:") {
		t.Fatalf("active tunnel printed troubleshooting:\n%s", out)
	}
}
