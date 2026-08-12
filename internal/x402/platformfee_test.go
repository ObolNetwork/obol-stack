package x402

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	return newFeeVerifierWithFacilitator(t, "http://facilitator.invalid", fee, routes)
}

// newFeeVerifierWithFacilitator is the paid-round-trip constructor: existing
// unit tests never hit the facilitator, so a dead URL is fine for them, but
// HandleProxy's per-request path must call /verify and /settle against a real
// mock.
func newFeeVerifierWithFacilitator(t *testing.T, facilitatorURL string, fee *AuthCaptureUnlockConfig, routes []RouteRule) (*Verifier, *PricingConfig) {
	t.Helper()
	cfg := &PricingConfig{
		Wallet:            "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Chain:             "base-sepolia",
		FacilitatorURL:    facilitatorURL,
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
	offered, err := BuildAuthCaptureRequirement(chain, asset, fee, "0x1111111111111111111111111111111111111111", DefaultMaxTimeoutSeconds, now)
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

// TestPlatformFee_NoLegacyNetworkAlias keeps the 402 free of an auth-capture
// entry under the legacy network name. The alias exists for pre-CAIP-2 buyers,
// which are all v1 exact buyers; an auth-capture alias could only be picked by
// a client that validateSignedAuthCapture would then reject on the network.
func TestPlatformFee_NoLegacyNetworkAlias(t *testing.T) {
	agent := RouteRule{Pattern: "/agent/*", Price: "0.01", AgentRuntime: "hermes"}
	v, cfg := newFeeVerifier(t, testFeeConfig(), []RouteRule{agent})

	mr, ok := v.resolvePaidRoute(cfg, &agent)
	if !ok {
		t.Fatal("agent route did not resolve")
	}
	advertised := legacyCompatRequirements(mr.requirements)

	var authCapture, exactAliases int
	for _, req := range advertised {
		switch {
		case req.Scheme == SchemeAuthCapture:
			authCapture++
			if req.Network != "eip155:84532" {
				t.Errorf("auth-capture advertised under legacy network %q — v2-only scheme must use CAIP-2 alone", req.Network)
			}
		case req.Network == "base-sepolia":
			exactAliases++
		}
	}
	if authCapture != 1 {
		t.Errorf("want exactly 1 auth-capture entry, got %d", authCapture)
	}
	if exactAliases != 1 {
		t.Errorf("want the exact legacy alias preserved for v1 buyers, got %d", exactAliases)
	}
	if advertised[0].Scheme != SchemeAuthCapture {
		t.Errorf("advertised accepts[0].scheme = %q, want %q", advertised[0].Scheme, SchemeAuthCapture)
	}
}

// TestPlatformFee_PaidRoundTrip is the only test that actually runs the two
// platformFeeHooks closures end-to-end through HandleProxy: resolveMatched
// substitutes the client-signed auth-capture requirement before /verify, and
// onSettled records fee revenue after /settle. Without this, a regression that
// stops wiring the hooks would leave every unit assertion green while fees
// silently stop being collected. The tamper half proves ResolveMatched
// short-circuits before the facilitator is ever called.
func TestPlatformFee_PaidRoundTrip(t *testing.T) {
	const payer = "0xAbCdEfabcdefABCDefAbcdefabCDefABcDefAbCd"
	var facilitatorCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"isValid":true,"payer":%q}`, payer)
	})
	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		facilitatorCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"payer":%q,"transaction":"0xabc","network":"eip155:84532"}`, payer)
	})
	facilitator := httptest.NewServer(mux)
	t.Cleanup(facilitator.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("CHAT_OK"))
	}))
	t.Cleanup(upstream.Close)

	const path = "/agent/chat"
	v, _ := newFeeVerifierWithFacilitator(t, facilitator.URL, testFeeConfig(), []RouteRule{{
		Pattern:        "/agent/*",
		Price:          "0.01",
		AgentRuntime:   "hermes",
		UpstreamURL:    upstream.URL,
		OfferNamespace: "test",
		OfferName:      "agent",
	}})

	// Challenge alone must not touch the facilitator — only a paid retry does.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("challenge status = %d, want 402; body: %s", w.Code, w.Body.String())
	}
	var challenge struct {
		X402Version int                             `json:"x402Version"`
		Accepts     []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	// Wire body may also carry the exact legacy-network alias (3 entries), but
	// the fee entry must still lead — buyers take the first scheme they speak.
	if len(challenge.Accepts) < 2 {
		t.Fatalf("want fee+exact accepts, got %d: %#v", len(challenge.Accepts), challenge.Accepts)
	}
	if challenge.Accepts[0].Scheme != SchemeAuthCapture {
		t.Fatalf("accepts[0].scheme = %q, want %q — the fee entry must come first or buyers take the free one",
			challenge.Accepts[0].Scheme, SchemeAuthCapture)
	}
	if got := facilitatorCalls.Load(); got != 0 {
		t.Fatalf("facilitator calls after challenge = %d, want 0", got)
	}

	amount, err := strconv.ParseInt(challenge.Accepts[0].Amount, 10, 64)
	if err != nil || amount <= 0 {
		t.Fatalf("accepts[0].Amount = %q, want a positive integer atomic amount", challenge.Accepts[0].Amount)
	}
	// 50 bps of the route price, integer division — same formula as recordFeeRevenue.
	wantFee := float64(amount * 50 / 10000)
	wantSettled := float64(amount)

	// Stub payment: accepted is the fee requirement the challenge just issued,
	// so validateSignedAuthCapture passes and resolveMatched returns it verbatim.
	wireJSON, _ := json.Marshal(map[string]any{
		"x402Version": 2,
		"payload":     map[string]any{"stub": true},
		"accepted":    challenge.Accepts[0],
	})
	paymentHeader := base64.StdEncoding.EncodeToString(wireJSON)
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-PAYMENT", paymentHeader)
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("paid status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CHAT_OK") {
		t.Fatalf("paid body = %q, want CHAT_OK (proxy must forward to the real upstream)", w.Body.String())
	}

	labels := prometheus.Labels{
		"network":       challenge.Accepts[0].Network,
		"asset":         challenge.Accepts[0].Asset,
		"fee_recipient": testFeeRecipient,
	}
	if got := testutil.ToFloat64(v.metrics.feeRevenueAtomic.With(labels)); got != wantFee {
		t.Errorf("fee revenue atomic = %v, want %v (amount %d × 50 bps / 10000)", got, wantFee, amount)
	}
	if got := testutil.ToFloat64(v.metrics.settledVolumeAtomic.With(labels)); got != wantSettled {
		t.Errorf("settled volume atomic = %v, want %v", got, wantSettled)
	}
	paidCalls := facilitatorCalls.Load()
	if paidCalls == 0 {
		t.Fatal("paid request did not call facilitator — resolveMatched/onSettled never ran")
	}

	// Redirect the fee to an attacker in the signed accepted requirement. The
	// resolveMatched hook must reject this before /verify — otherwise a
	// facilitator would settle a fee we never intended to collect.
	tampered := challenge.Accepts[0]
	ex := make(map[string]any, len(tampered.Extra))
	for k, val := range tampered.Extra {
		ex[k] = val
	}
	ex["feeRecipient"] = "0x9999999999999999999999999999999999999999"
	tampered.Extra = ex
	tamperedWire, _ := json.Marshal(map[string]any{
		"x402Version": 2,
		"payload":     map[string]any{"stub": true},
		"accepted":    tampered,
	})
	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-PAYMENT", base64.StdEncoding.EncodeToString(tamperedWire))
	w = httptest.NewRecorder()
	v.HandleProxy(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("tampered status = %d, want 402; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "payment_policy_mismatch") {
		t.Errorf("tampered body = %q, want payment_policy_mismatch", w.Body.String())
	}
	if got := facilitatorCalls.Load(); got != paidCalls {
		t.Errorf("facilitator calls after tamper = %d, want unchanged %d (rejected pre-forward)", got, paidCalls)
	}
}
