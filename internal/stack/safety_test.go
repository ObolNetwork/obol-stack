package stack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// newTestCfg returns a Config rooted at t.TempDir() so safety_test does not
// touch real cluster paths. ConfigDir has no kubeconfig.yaml so the cluster
// half of DiscoverLiveServices short-circuits via kubectl.EnsureCluster.
func newTestCfg(t *testing.T) *config.Config {
	t.Helper()
	tmp := t.TempDir()
	return &config.Config{
		ConfigDir: filepath.Join(tmp, "config"),
		DataDir:   filepath.Join(tmp, "data"),
		BinDir:    filepath.Join(tmp, "bin"),
		StateDir:  filepath.Join(tmp, "state"),
	}
}

func writeGatewayPID(t *testing.T, cfg *config.Config, name string, pid int) {
	t.Helper()
	dir := filepath.Join(cfg.StateDir, "sell-inference", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gateway.pid"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
}

func TestConfirmRunningServicesLoss_EmptyPassesThrough(t *testing.T) {
	cfg := newTestCfg(t)
	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmRunningServicesLoss(cfg, u, "obol stack down", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !proceed {
		t.Fatal("empty snapshot must pass through (returned false)")
	}
	if buf.Len() != 0 {
		t.Errorf("expected silent pass-through, got output: %q", buf.String())
	}
}

func TestConfirmRunningServicesLoss_NonInteractiveLiveServiceFailsClosed(t *testing.T) {
	cfg := newTestCfg(t)
	writeGatewayPID(t, cfg, "aeon", os.Getpid()) // our own PID is guaranteed alive

	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf) // isTTY defaults false

	proceed, err := ConfirmRunningServicesLoss(cfg, u, "obol stack down", false)
	if err == nil {
		t.Fatal("expected non-nil error when live gateways exist in non-interactive shell without --yes")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error must mention --yes (operator escape hatch): %v", err)
	}
	if proceed {
		t.Error("must not proceed when the safety gate trips")
	}
}

func TestConfirmRunningServicesLoss_SkipConfirmPassesThrough(t *testing.T) {
	cfg := newTestCfg(t)
	writeGatewayPID(t, cfg, "aeon", os.Getpid())

	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmRunningServicesLoss(cfg, u, "obol stack down", true)
	if err != nil {
		t.Fatalf("unexpected err with --yes: %v", err)
	}
	if !proceed {
		t.Fatal("--yes must let the action proceed")
	}
	if !strings.Contains(buf.String(), "--yes") {
		t.Errorf("operator should still see a warning, got: %q", buf.String())
	}
}

func TestConfirmRunningServicesLoss_DeadGatewayDoesNotTrigger(t *testing.T) {
	cfg := newTestCfg(t)
	// PID 0 is treated as invalid; reserve a fresh PID by forking would be
	// flaky in unit tests, so use a definitely-not-existing PID.
	writeGatewayPID(t, cfg, "stale", 2147483645)

	var buf bytes.Buffer
	u := ui.NewForTest(&buf, &buf)

	proceed, err := ConfirmRunningServicesLoss(cfg, u, "obol stack down", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !proceed {
		t.Fatal("dead PID file must be ignored — should pass through")
	}
}

func TestErrSafetyAborted_IsExported(t *testing.T) {
	if !errors.Is(ErrSafetyAborted(), errSafetyAborted) {
		t.Fatal("ErrSafetyAborted() must wrap the package sentinel")
	}
}

func TestRawOffer_GateReadyRequiresBothConditions(t *testing.T) {
	cases := []struct {
		name     string
		conds    [][2]string // (type, status)
		wantGate bool
	}{
		{"both true", [][2]string{{"PaymentGateReady", "True"}, {"RoutePublished", "True"}}, true},
		{"payment only", [][2]string{{"PaymentGateReady", "True"}}, false},
		{"route only", [][2]string{{"RoutePublished", "True"}}, false},
		{"both false", [][2]string{{"PaymentGateReady", "False"}, {"RoutePublished", "False"}}, false},
		{"payment true route false", [][2]string{{"PaymentGateReady", "True"}, {"RoutePublished", "False"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var o rawOffer
			for _, c := range tc.conds {
				o.Status.Conditions = append(o.Status.Conditions, struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				}{Type: c[0], Status: c[1]})
			}
			if got := o.gateReady(); got != tc.wantGate {
				t.Errorf("gateReady() = %v, want %v", got, tc.wantGate)
			}
		})
	}
}

func TestRawOffer_PriceSummary(t *testing.T) {
	mk := func(perRequest, perMTok, perHour, asset string) rawOffer {
		var o rawOffer
		o.Spec.Payment.Price.PerRequest = perRequest
		o.Spec.Payment.Price.PerMTok = perMTok
		o.Spec.Payment.Price.PerHour = perHour
		o.Spec.Payment.Asset.Symbol = asset
		return o
	}
	cases := []struct {
		name string
		in   rawOffer
		want string
	}{
		{"per request USDC default", mk("0.001", "", "", ""), "0.001 USDC/request"},
		{"per MTok OBOL", mk("", "23", "", "OBOL"), "23 OBOL/MTok"},
		{"per hour", mk("", "", "5", "USDC"), "5 USDC/hour"},
		{"empty", mk("", "", "", ""), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.priceSummary(); got != tc.want {
				t.Errorf("priceSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}
