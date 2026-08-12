package x402

import (
	"encoding/json"
	"testing"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

const (
	feePathFeeRecipient      = "0x0f1c130C52047C30A84d548dDEed9Ddb2DE983dB"
	feePathCaptureAuthorizer = "0xb035F7221C990694fd71370a5274c40E630B27Bf"
)

func testFeeConfig() *AuthCaptureUnlockConfig {
	return &AuthCaptureUnlockConfig{
		Enabled:           true,
		FeeRecipient:      testFeeRecipient,
		CaptureAuthorizer: testCaptureAuthorizer,
		MinFeeBps:         50,
		MaxFeeBps:         50,
	}
}

func newFeeVerifier(t *testing.T, fee *AuthCaptureUnlockConfig, routes []RouteRule) (*Verifier, *PricingConfig) {
	t.Helper()
	cfg := &PricingConfig{
		Wallet:            "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:             "base-sepolia",
		FacilitatorURL:    "http://facilitator.invalid",
		Routes:            routes,
		AuthCaptureUnlock: fee,
	}
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	v.MarkRoutesLoaded()
	return v, cfg
}

// TestPlatformFee_AdvertisedOnAgentRoutesOnly pins the scope of the per-request
// platform fee: an agent offer advertises the fee-bearing auth-capture
// requirement AHEAD of its exact twin (so a buyer that speaks both pays the
// fee), while an http offer is untouched and keeps advertising exact alone.
func TestPlatformFee_AdvertisedOnAgentRoutesOnly(t *testing.T) {
	agent := RouteRule{Pattern: "/agent/*", Price: "0.01", AgentRuntime: "hermes"}
	api := RouteRule{Pattern: "/api/*", Price: "0.01"}
	v, cfg := newFeeVerifier(t, testFeeConfig(), []RouteRule{agent, api})

	mrAgent, ok := v.resolvePaidRoute(cfg, &agent)
	if !ok {
		t.Fatal("agent route did not resolve")
	}
	if len(mrAgent.requirements) != 2 {
		t.Fatalf("agent route: want 2 requirements (fee + exact), got %d", len(mrAgent.requirements))
	}
	feeReq := mrAgent.requirements[0]
	if feeReq.Scheme != SchemeAuthCapture {
		t.Errorf("agent accepts[0].scheme = %q, want %q — the fee entry must come first or buyers take the free one", feeReq.Scheme, SchemeAuthCapture)
	}
	if got := feeReq.Extra["feeRecipient"]; got != testFeeRecipient {
		t.Errorf("feeRecipient = %v, want %s", got, testFeeRecipient)
	}
	if got := feeReq.Extra["maxFeeBps"]; got != uint16(50) {
		t.Errorf("maxFeeBps = %v, want 50", got)
	}
	if feeReq.Amount != mrAgent.requirements[1].Amount {
		t.Errorf("fee twin priced at %q but exact twin at %q — they must charge the buyer the same",
			feeReq.Amount, mrAgent.requirements[1].Amount)
	}
	if mrAgent.requirements[1].Scheme != "exact" {
		t.Errorf("agent accepts[1].scheme = %q, want exact (the compatibility path)", mrAgent.requirements[1].Scheme)
	}

	mrAPI, ok := v.resolvePaidRoute(cfg, &api)
	if !ok {
		t.Fatal("http route did not resolve")
	}
	if len(mrAPI.requirements) != 1 || mrAPI.requirements[0].Scheme != "exact" {
		t.Errorf("http offer must stay exact-only, got %d requirement(s) with scheme %q",
			len(mrAPI.requirements), mrAPI.requirements[0].Scheme)
	}
}

// TestPlatformFee_DisabledIsInert is the operator kill switch: with the fee
// off, an agent route is byte-for-byte the exact-only route it was before the
// fee existed.
func TestPlatformFee_DisabledIsInert(t *testing.T) {
	off := testFeeConfig()
	off.Enabled = false
	agent := RouteRule{Pattern: "/agent/*", Price: "0.01", AgentRuntime: "hermes"}
	v, cfg := newFeeVerifier(t, off, []RouteRule{agent})

	mr, ok := v.resolvePaidRoute(cfg, &agent)
	if !ok {
		t.Fatal("agent route did not resolve")
	}
	if len(mr.requirements) != 1 || mr.requirements[0].Scheme != "exact" {
		t.Fatalf("disabled fee must advertise exact alone, got %d requirement(s) with scheme %q",
			len(mr.requirements), mr.requirements[0].Scheme)
	}
	if _, ok := v.platformFeeHooks(cfg, mr); ok != nil {
		t.Error("disabled fee must install no settle hook")
	}
}

// TestPlatformFee_NetworkFilter keeps the fee off chains whose facilitator has
// no auth-capture handler: advertising it there would make it the entry a
// capable buyer picks first, and their payment would then fail at verify.
func TestPlatformFee_NetworkFilter(t *testing.T) {
	fee := testFeeConfig()
	fee.Network = "base" // stack prices in base-sepolia
	agent := RouteRule{Pattern: "/agent/*", Price: "0.01", AgentRuntime: "hermes"}
	v, cfg := newFeeVerifier(t, fee, []RouteRule{agent})

	mr, ok := v.resolvePaidRoute(cfg, &agent)
	if !ok {
		t.Fatal("agent route did not resolve")
	}
	if len(mr.requirements) != 1 || mr.requirements[0].Scheme != "exact" {
		t.Fatalf("fee scoped to another network must not be advertised, got %d requirement(s) with scheme %q",
			len(mr.requirements), mr.requirements[0].Scheme)
	}
}

// TestPlatformFee_PrimaryChainResolved guards a regression the fee twin makes
// easy: the primary chain/asset used to be detected by len(reqs)==1, which the
// extra requirement silently broke, leaving the 402 display unpriced.
func TestPlatformFee_PrimaryChainResolved(t *testing.T) {
	agent := RouteRule{Pattern: "/agent/*", Price: "0.01", AgentRuntime: "hermes"}
	v, cfg := newFeeVerifier(t, testFeeConfig(), []RouteRule{agent})

	mr, ok := v.resolvePaidRoute(cfg, &agent)
	if !ok {
		t.Fatal("agent route did not resolve")
	}
	if mr.chain.CAIP2Network == "" {
		t.Error("primary chain unresolved — the fee requirement displaced the len(reqs)==1 detection")
	}
	if mr.asset.Address == "" {
		t.Error("primary asset unresolved")
	}
}

// wireEcho round-trips a requirement through JSON the way a buyer's signed
// `accepted` field reaches us: typed numbers (uint16, int64) come back as
// float64, which is exactly where a naive field comparison would break.
func wireEcho(t *testing.T, req x402types.PaymentRequirements) x402types.PaymentRequirements {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal requirement: %v", err)
	}
	var out x402types.PaymentRequirements
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal requirement: %v", err)
	}
	return out
}

// TestValidateSignedAuthCapture is the money path. Auth-capture is settled
// against the struct the CLIENT supplies, so every field that decides who gets
// paid and how much must be pinned against what we offered. Each case below is
// a way a buyer could otherwise redirect the fee, underpay, or hold an
// authorization open.
func TestValidateSignedAuthCapture(t *testing.T) {
	now := time.Now()
	fee := testFeeConfig()
	fee.applyDeadlineDefaults()
	fee.Price = "0.01"
	chain := ChainInfo{CAIP2Network: "eip155:84532"}
	asset := AssetInfo{
		Address:        "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Decimals:       6,
		EIP712Name:     "USDC",
		EIP712Version:  "2",
		TransferMethod: "eip3009",
	}
	offered, err := BuildAuthCaptureRequirement(chain, asset, fee, "0x1111111111111111111111111111111111111111", now)
	if err != nil {
		t.Fatalf("BuildAuthCaptureRequirement: %v", err)
	}

	// The buyer signs the challenge, then time passes before the paid request
	// lands — so validation runs against a LATER clock than the one that issued
	// the deadlines. This is the normal case and must pass.
	later := now.Add(30 * time.Second).Unix()

	t.Run("faithful echo after clock drift", func(t *testing.T) {
		if err := validateSignedAuthCapture(wireEcho(t, offered), offered, fee.CaptureDeadlineSecs, later); err != nil {
			t.Fatalf("a faithful echo must validate, got: %v", err)
		}
	})

	t.Run("address casing is not tampering", func(t *testing.T) {
		signed := wireEcho(t, offered)
		signed.PayTo = "0X1111111111111111111111111111111111111111"
		if err := validateSignedAuthCapture(signed, offered, fee.CaptureDeadlineSecs, later); err != nil {
			t.Errorf("payTo casing must not be rejected, got: %v", err)
		}
	})

	tampered := map[string]func(*x402types.PaymentRequirements){
		"fee redirected to the buyer": func(r *x402types.PaymentRequirements) {
			r.Extra["feeRecipient"] = "0x2222222222222222222222222222222222222222"
		},
		"fee zeroed out": func(r *x402types.PaymentRequirements) {
			r.Extra["maxFeeBps"] = float64(0)
		},
		"fee floor lowered": func(r *x402types.PaymentRequirements) {
			r.Extra["minFeeBps"] = float64(0)
		},
		"capture authorizer swapped": func(r *x402types.PaymentRequirements) {
			r.Extra["captureAuthorizer"] = "0x3333333333333333333333333333333333333333"
		},
		"autoCapture disabled": func(r *x402types.PaymentRequirements) {
			r.Extra["autoCapture"] = false
		},
		"seller leg redirected": func(r *x402types.PaymentRequirements) {
			r.PayTo = "0x4444444444444444444444444444444444444444"
		},
		"underpayment": func(r *x402types.PaymentRequirements) {
			r.Amount = "1"
		},
		"asset swapped": func(r *x402types.PaymentRequirements) {
			r.Asset = "0x5555555555555555555555555555555555555555"
		},
		"network swapped": func(r *x402types.PaymentRequirements) {
			r.Network = "eip155:8453"
		},
		"scheme downgraded to exact": func(r *x402types.PaymentRequirements) {
			r.Scheme = "exact"
		},
		"authorization held open far past the window": func(r *x402types.PaymentRequirements) {
			r.Extra["captureDeadline"] = float64(later + 86400)
			r.Extra["refundDeadline"] = float64(later + 172800)
		},
		"capture deadline already expired": func(r *x402types.PaymentRequirements) {
			r.Extra["captureDeadline"] = float64(later - 1)
		},
		"refund before capture": func(r *x402types.PaymentRequirements) {
			r.Extra["refundDeadline"] = float64(later + 1)
		},
	}
	for name, tamper := range tampered {
		t.Run(name, func(t *testing.T) {
			signed := wireEcho(t, offered)
			tamper(&signed)
			if err := validateSignedAuthCapture(signed, offered, fee.CaptureDeadlineSecs, later); err == nil {
				t.Fatalf("%s must be rejected, but validation passed", name)
			}
		})
	}

	t.Run("missing extra", func(t *testing.T) {
		signed := wireEcho(t, offered)
		signed.Extra = nil
		if err := validateSignedAuthCapture(signed, offered, fee.CaptureDeadlineSecs, later); err == nil {
			t.Fatal("a payment with no extra must be rejected")
		}
	})
}
