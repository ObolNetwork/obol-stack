package escrow

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSubmitter records submissions and returns a canned tx hash.
type fakeSubmitter struct {
	calls   atomic.Int64
	voucher Permit2Voucher
	details []TransferDetail
	err     error
}

func (f *fakeSubmitter) Submit(_ context.Context, v Permit2Voucher, details []TransferDetail) (string, error) {
	f.calls.Add(1)
	f.voucher = v
	f.details = details
	if f.err != nil {
		return "", f.err
	}
	return "0xfeedface", nil
}

// newTestServer wires a Server over a temp-dir store and returns an
// HTTPGateway client pointed at it — proving the server speaks exactly the
// wire shape the gateway client expects.
func newTestServer(t *testing.T, opts ServerOptions) (*httptest.Server, *HTTPGateway, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newTestServerWithStore(t, store, opts)
}

func newTestServerWithStore(t *testing.T, store *Store, opts ServerOptions) (*httptest.Server, *HTTPGateway, *Store) {
	t.Helper()
	srv := httptest.NewServer(NewServer(store, opts).Handler())
	t.Cleanup(srv.Close)
	g := &HTTPGateway{Base: srv.URL, Token: opts.Token, Client: srv.Client()}
	return srv, g, store
}

// signedTestVoucher returns a voucher bound to testSpender, expiring in 1h.
func signedTestVoucher(t *testing.T) Permit2Voucher {
	t.Helper()
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	if err := SignVoucher(&v, big.NewInt(84532), key); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestServer_ReserveAwaitingThenReserved(t *testing.T) {
	sub := &fakeSubmitter{}
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Networks: []string{"base", "base-sepolia"}, Submitter: sub})
	ctx := context.Background()

	// 1. Reserve without a voucher: AwaitingVoucher + the spender to bind.
	r, err := g.Reserve(ctx, ReserveRequest{ID: "b1", Network: "base-sepolia", Amount: "3500"})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if r.State != StateAwaitingVoucher {
		t.Fatalf("state = %q, want AwaitingVoucher", r.State)
	}
	if r.Spender != testSpender.Hex() {
		t.Fatalf("spender hint = %q, want %s", r.Spender, testSpender.Hex())
	}

	// 2. Re-reserve with a voucher binding that spender: Reserved.
	v := signedTestVoucher(t)
	r2, err := g.Reserve(ctx, ReserveRequest{ID: "b1", Network: "base-sepolia", Voucher: &v})
	if err != nil {
		t.Fatalf("re-Reserve with voucher: %v", err)
	}
	if r2.State != StateReserved || r2.Spender != testSpender.Hex() {
		t.Fatalf("receipt = %+v, want Reserved", r2)
	}

	// 3. A later voucher-less re-reserve must NOT downgrade the held voucher.
	r3, err := g.Reserve(ctx, ReserveRequest{ID: "b1", Network: "base-sepolia"})
	if err != nil {
		t.Fatalf("voucher-less re-Reserve: %v", err)
	}
	if r3.State != StateReserved {
		t.Fatalf("voucher-less re-reserve downgraded state to %q", r3.State)
	}
}

func TestServer_ReserveRejectsBadVouchers(t *testing.T) {
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Networks: []string{"base-sepolia"}, Submitter: &fakeSubmitter{}})
	ctx := context.Background()

	// Wrong spender binding.
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	v.Spender = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"
	if err := SignVoucher(&v, big.NewInt(84532), key); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "bad1", Voucher: &v}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("wrong-spender reserve = %v, want 400", err)
	}

	// Tampered amount.
	v2 := signedTestVoucher(t)
	v2.Recipients[0].Amount = "999999"
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "bad2", Voucher: &v2}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("tampered reserve = %v, want 400", err)
	}

	// Network not served by this facilitator.
	v3, key3 := goldenVoucher(t)
	v3.Deadline = time.Now().Add(time.Hour).Unix()
	v3.Network = "base"
	if err := SignVoucher(&v3, big.NewInt(8453), key3); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "bad3", Voucher: &v3}); err == nil || !strings.Contains(err.Error(), "not served") {
		t.Errorf("off-network reserve = %v, want network rejection", err)
	}

	// Reserve leg / voucher network drift.
	v4 := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "bad4", Network: "base", Voucher: &v4}); err == nil || !strings.Contains(err.Error(), "different chains") {
		t.Errorf("network-drift reserve = %v, want mismatch error", err)
	}
}

func TestServer_CaptureBatchHappyPathAndIdempotency(t *testing.T) {
	sub := &fakeSubmitter{}
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Networks: []string{"base-sepolia"}, Submitter: sub})
	ctx := context.Background()

	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b2", Voucher: &v}); err != nil {
		t.Fatal(err)
	}

	// Capture a subset: seat 0 only; seat 1 must ride along index-wise at 0.
	r, err := g.CaptureBatch(ctx, "b2", []BatchRecipient{{Address: v.Recipients[0].Address, Amount: "1000"}})
	if err != nil {
		t.Fatalf("CaptureBatch: %v", err)
	}
	if r.State != StateCaptured || r.TxHash != "0xfeedface" {
		t.Fatalf("receipt = %+v", r)
	}
	if got := sub.calls.Load(); got != 1 {
		t.Fatalf("submitter calls = %d, want 1", got)
	}
	if len(sub.details) != 2 || sub.details[0].Amount.Cmp(big.NewInt(1000)) != 0 || sub.details[1].Amount.Sign() != 0 {
		t.Fatalf("submitted details = %+v, want index-wise [1000, 0]", sub.details)
	}

	// Idempotent: a second capture returns the stored receipt, no re-settle.
	r2, err := g.Capture(ctx, "b2")
	if err != nil {
		t.Fatalf("re-Capture: %v", err)
	}
	if r2.TxHash != "0xfeedface" || sub.calls.Load() != 1 {
		t.Fatalf("re-capture = %+v after %d submits, want stored receipt and 1 submit", r2, sub.calls.Load())
	}

	// Re-reserve after capture returns the captured receipt (settled is settled).
	r3, err := g.Reserve(ctx, ReserveRequest{ID: "b2", Voucher: &v})
	if err != nil {
		t.Fatal(err)
	}
	if r3.State != StateCaptured {
		t.Fatalf("re-reserve after capture = %+v", r3)
	}
}

func TestServer_CaptureFullVoucherWhenNoBody(t *testing.T) {
	sub := &fakeSubmitter{}
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Submitter: sub})
	ctx := context.Background()

	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b3", Voucher: &v}); err != nil {
		t.Fatal(err)
	}
	// Plain Capture (no body) settles every voucher seat.
	if _, err := g.Capture(ctx, "b3"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(sub.details) != 2 || sub.details[0].Amount.Cmp(big.NewInt(1000)) != 0 || sub.details[1].Amount.Cmp(big.NewInt(2500)) != 0 {
		t.Fatalf("details = %+v, want full [1000, 2500]", sub.details)
	}
}

func TestServer_CaptureGuards(t *testing.T) {
	sub := &fakeSubmitter{}
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Submitter: sub})
	ctx := context.Background()

	// Unknown escrow.
	if _, err := g.Capture(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("capture unknown = %v, want 404", err)
	}

	// AwaitingVoucher (no voucher attached) cannot capture.
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Capture(ctx, "b4"); err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("capture awaiting = %v, want 409", err)
	}

	// Subset rule: amount mismatch is rejected before any submission.
	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b5", Voucher: &v}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CaptureBatch(ctx, "b5", []BatchRecipient{{Address: v.Recipients[0].Address, Amount: "999"}}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("amount-mismatch capture = %v, want 400", err)
	}
	if _, err := g.CaptureBatch(ctx, "b5", []BatchRecipient{{Address: testSpender.Hex(), Amount: "1000"}}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("unknown-recipient capture = %v, want 400", err)
	}
	if sub.calls.Load() != 0 {
		t.Fatalf("submitter was called %d times for rejected captures", sub.calls.Load())
	}

	// Voided escrow cannot capture.
	if _, err := g.Void(ctx, "b5"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Capture(ctx, "b5"); err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("capture voided = %v, want 409", err)
	}

	// Settlement failure surfaces as 502 and leaves the escrow Reserved.
	v6 := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b6", Voucher: &v6}); err != nil {
		t.Fatal(err)
	}
	sub.err = fmt.Errorf("rpc down")
	if _, err := g.Capture(ctx, "b6"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("failed settle = %v, want 502", err)
	}
	sub.err = nil
	if _, err := g.Capture(ctx, "b6"); err != nil {
		t.Errorf("retry after settle failure: %v", err)
	}
}

func TestServer_CaptureWithoutSubmitter503(t *testing.T) {
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Submitter: nil})
	ctx := context.Background()

	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b7", Voucher: &v}); err != nil {
		t.Fatal(err)
	}
	_, err := g.Capture(ctx, "b7")
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "OBOL_ESCROW_KEY") {
		t.Fatalf("capture without submitter = %v, want 503 naming OBOL_ESCROW_KEY", err)
	}
	// Reserve and void still work without a submitter.
	if _, err := g.Void(ctx, "b7"); err != nil {
		t.Fatalf("void without submitter: %v", err)
	}
}

func TestServer_ReserveVoucherWithoutSpenderConfigured(t *testing.T) {
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret"})
	ctx := context.Background()

	// Voucher-less reserve still answers (empty spender hint).
	r, err := g.Reserve(ctx, ReserveRequest{ID: "b8"})
	if err != nil || r.State != StateAwaitingVoucher || r.Spender != "" {
		t.Fatalf("reserve = %+v, %v", r, err)
	}
	// Voucher verification is refused: nothing to bind the spender to.
	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b8", Voucher: &v}); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("voucher reserve without spender = %v, want 503", err)
	}
}

func TestServer_VoidIdempotent(t *testing.T) {
	_, g, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender})
	ctx := context.Background()

	// Voiding the unknown is a no-op success (refunds re-runnable).
	if r, err := g.Void(ctx, "ghost"); err != nil || r.State != StateVoided {
		t.Fatalf("void unknown = %+v, %v", r, err)
	}
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b9"}); err != nil {
		t.Fatal(err)
	}
	if r, err := g.Void(ctx, "b9"); err != nil || r.State != StateVoided {
		t.Fatalf("void = %+v, %v", r, err)
	}
	if r, err := g.Void(ctx, "b9"); err != nil || r.State != StateVoided {
		t.Fatalf("re-void = %+v, %v", r, err)
	}
}

func TestServer_BearerAuth(t *testing.T) {
	srv, _, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender})
	ctx := context.Background()

	wrong := &HTTPGateway{Base: srv.URL, Token: "wrong", Client: srv.Client()}
	if _, err := wrong.Reserve(ctx, ReserveRequest{ID: "a1"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token = %v, want 401", err)
	}
	missing := &HTTPGateway{Base: srv.URL, Client: srv.Client()}
	if _, err := missing.Void(ctx, "a1"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("missing token = %v, want 401", err)
	}

	// Empty server token disables auth — symmetric with the client omitting
	// the Authorization header when its Token is empty.
	srv2, g2, _ := newTestServer(t, ServerOptions{Token: "", Spender: testSpender})
	_ = srv2
	if _, err := g2.Reserve(ctx, ReserveRequest{ID: "a2"}); err != nil {
		t.Errorf("unauthenticated reserve on tokenless server: %v", err)
	}
}

func TestServer_PersistenceAcrossRestart(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := &fakeSubmitter{}
	_, g, _ := newTestServerWithStore(t, store, ServerOptions{Token: "secret", Spender: testSpender, Submitter: sub})
	ctx := context.Background()

	v := signedTestVoucher(t)
	if _, err := g.Reserve(ctx, ReserveRequest{ID: "p1", Voucher: &v}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Capture(ctx, "p1"); err != nil {
		t.Fatal(err)
	}

	// "Restart": a new server over the same state dir, with a submitter that
	// would fail if called — the stored receipt must be returned instead.
	sub2 := &fakeSubmitter{err: fmt.Errorf("must not settle twice")}
	_, g2, _ := newTestServerWithStore(t, store, ServerOptions{Token: "secret", Spender: testSpender, Submitter: sub2})
	r, err := g2.Capture(ctx, "p1")
	if err != nil {
		t.Fatalf("capture after restart: %v", err)
	}
	if r.State != StateCaptured || r.TxHash != "0xfeedface" {
		t.Fatalf("receipt after restart = %+v", r)
	}
	if sub2.calls.Load() != 0 {
		t.Fatal("restart capture re-submitted an already-captured escrow")
	}
}

func TestServer_InfoAndHealthz(t *testing.T) {
	srv, _, _ := newTestServer(t, ServerOptions{Token: "secret", Spender: testSpender, Networks: []string{"base", "base-sepolia"}})

	// /escrow/info is unauthenticated discovery: signers need the spender
	// address before they hold any credential.
	resp, err := srv.Client().Get(srv.URL + "/escrow/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("info status = %d", resp.StatusCode)
	}
	var info struct {
		Address  string   `json:"address"`
		Networks []string `json:"networks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Address != testSpender.Hex() {
		t.Errorf("info address = %q", info.Address)
	}
	if len(info.Networks) != 2 || info.Networks[0] != "base" {
		t.Errorf("info networks = %v", info.Networks)
	}

	hz, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	hz.Body.Close()
	if hz.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d", hz.StatusCode)
	}
}

func TestServer_RejectsUnsafeIDs(t *testing.T) {
	srv, _, _ := newTestServer(t, ServerOptions{Token: "", Spender: testSpender})

	resp, err := srv.Client().Post(srv.URL+"/escrow/void/bad%20id", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unsafe id status = %d, want 400", resp.StatusCode)
	}
}
