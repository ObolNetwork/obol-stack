package escrow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLedgerGateway_Lifecycle(t *testing.T) {
	g := NewLedgerGateway()
	ctx := context.Background()

	r, err := g.Reserve(ctx, ReserveRequest{ID: "b1", Amount: "500.00"})
	if err != nil || r.State != StateReserved {
		t.Fatalf("Reserve = %+v, %v", r, err)
	}
	if !strings.HasPrefix(r.TxHash, "dev-ledger:") {
		t.Fatalf("ledger receipt %q must be labeled dev-ledger", r.TxHash)
	}

	// Reserve is idempotent.
	again, err := g.Reserve(ctx, ReserveRequest{ID: "b1"})
	if err != nil || again.State != StateReserved {
		t.Fatalf("re-Reserve = %+v, %v", again, err)
	}

	c, err := g.Capture(ctx, "b1")
	if err != nil || c.State != StateCaptured {
		t.Fatalf("Capture = %+v, %v", c, err)
	}

	// Captured escrow cannot be voided (the reward was legitimately paid).
	if _, err := g.Void(ctx, "b1"); err == nil {
		t.Fatal("Void after Capture should error")
	}
}

func TestLedgerGateway_VoidPaths(t *testing.T) {
	g := NewLedgerGateway()
	ctx := context.Background()

	// Voiding the unknown is a no-op success (refunds must be re-runnable).
	if r, err := g.Void(ctx, "ghost"); err != nil || r.State != StateVoided {
		t.Fatalf("Void(unknown) = %+v, %v", r, err)
	}

	if _, err := g.Reserve(ctx, ReserveRequest{ID: "b2"}); err != nil {
		t.Fatal(err)
	}
	if r, err := g.Void(ctx, "b2"); err != nil || r.State != StateVoided {
		t.Fatalf("Void(reserved) = %+v, %v", r, err)
	}
	// Capturing a voided escrow fails.
	if _, err := g.Capture(ctx, "b2"); err == nil {
		t.Fatal("Capture after Void should error")
	}
}

func TestHTTPGateway_RoutesAndAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody ReserveRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		_ = json.NewEncoder(w).Encode(Receipt{State: StateReserved, TxHash: "0xabc"})
	}))
	defer server.Close()

	g := &HTTPGateway{Base: server.URL + "/", Token: "secret", Client: server.Client()}
	r, err := g.Reserve(context.Background(), ReserveRequest{ID: "b3", Network: "base", Amount: "10"})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if gotPath != "/escrow/reserve/b3" {
		t.Errorf("path = %q, want /escrow/reserve/b3", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth = %q, want bearer token", gotAuth)
	}
	if gotBody.Network != "base" || gotBody.Amount != "10" {
		t.Errorf("body = %+v", gotBody)
	}
	if r.State != StateReserved || r.TxHash != "0xabc" {
		t.Errorf("receipt = %+v", r)
	}
}

func TestHTTPGateway_SurfacesFacilitatorErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such escrow", http.StatusNotFound)
	}))
	defer server.Close()

	g := &HTTPGateway{Base: server.URL, Client: server.Client()}
	if _, err := g.Capture(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("Capture error = %v, want 404 surfaced", err)
	}
}
