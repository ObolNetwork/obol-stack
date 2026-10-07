package x402

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

func TestRecordedPricing_RoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if rec, err := LoadRecordedPricing(cfg); err != nil || rec != nil {
		t.Fatalf("missing record = (%v, %v), want (nil, nil)", rec, err)
	}
	in := &RecordedPricing{
		Wallet:         "0x1111111111111111111111111111111111111111",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://facilitator.example",
		VerifyOnly:     true,
	}
	if err := SaveRecordedPricing(cfg, in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(RecordedPricingPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	out, err := LoadRecordedPricing(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if *out != *in || out.Version != recordedPricingVersion {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

// fakeKube serves the pricing ConfigMap + wallet Secret and applies merge
// patches to them, so reconcile idempotence can be asserted end to end.
type fakeKube struct {
	pricingYAML string
	wallet      string
	patches     []string
}

func (f *fakeKube) Output(args ...string) (string, error) {
	switch {
	case args[1] == "configmap":
		return f.pricingYAML, nil
	case args[1] == "secret":
		return base64.StdEncoding.EncodeToString([]byte(f.wallet)), nil
	}
	return "", nil
}

func (f *fakeKube) Run(args ...string) error {
	f.patches = append(f.patches, args[1])
	patch := args[6]
	switch args[1] {
	case "secret":
		var p struct {
			StringData map[string]string `yaml:"stringData"`
		}
		_ = yaml.Unmarshal([]byte(patch), &p)
		f.wallet = p.StringData["WALLET_ADDRESS"]
	case "configmap":
		var p struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		_ = yaml.Unmarshal([]byte(patch), &p)
		if p.Metadata.Labels["obol.org/managed-by"] != "obol" {
			return &testErr{"pricing patch missing managed-by label"}
		}
		f.pricingYAML = p.Data["pricing.yaml"]
	}
	return nil
}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func TestReconcilePricing_AppliesThenIdempotent(t *testing.T) {
	// Fresh chart default: empty wallet plus a helm-rendered
	// authCaptureUnlock block that must survive the reconcile.
	kube := &fakeKube{pricingYAML: `wallet: ""
chain: "base"
facilitatorURL: "https://x402.gcp.obol.tech"
verifyOnly: true
authCaptureUnlock:
  enabled: true
  offerPrefix: "agent"
routes: []
`}
	rec := &RecordedPricing{
		Wallet:         "0x2222222222222222222222222222222222222222",
		Chain:          "base-sepolia",
		FacilitatorURL: "https://x402.gcp.obol.tech",
		VerifyOnly:     true,
	}

	changed, err := reconcilePricing(kube, rec)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first reconcile against a fresh cluster must patch")
	}
	if strings.Join(kube.patches, ",") != "secret,configmap" {
		t.Fatalf("patches = %v, want secret then configmap", kube.patches)
	}
	if kube.wallet != rec.Wallet {
		t.Errorf("secret wallet = %q", kube.wallet)
	}
	var live PricingConfig
	if err := yaml.Unmarshal([]byte(kube.pricingYAML), &live); err != nil {
		t.Fatal(err)
	}
	if live.Wallet != rec.Wallet || live.Chain != "base-sepolia" {
		t.Errorf("live pricing = %+v", live)
	}
	if live.AuthCaptureUnlock == nil || !live.AuthCaptureUnlock.Enabled {
		t.Error("reconcile must preserve the helm-rendered authCaptureUnlock block")
	}

	kube.patches = nil
	changed, err = reconcilePricing(kube, rec)
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(kube.patches) != 0 {
		t.Fatalf("second reconcile must be a no-op, got changed=%v patches=%v", changed, kube.patches)
	}
}
