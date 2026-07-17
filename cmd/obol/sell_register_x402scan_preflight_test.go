package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// preflightOpenAPI never fails the command — it only warns via u.Warn/Warnf,
// which write to UI's stderr writer. These tests call it directly against a
// plain httptest.Server (bypassing resolveX402scanOrigin's https/hostname
// gating, which is exercised separately in sell_register_x402scan_test.go).

func TestPreflightOpenAPI_NoOperations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paths":{}}`))
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	u := ui.NewForTest(&bytes.Buffer{}, &stderr)
	preflightOpenAPI(context.Background(), u, srv.URL)

	if got := stderr.String(); !strings.Contains(got, "advertises no operations") {
		t.Fatalf("expected no-operations warning, got: %q", got)
	}
}

func TestPreflightOpenAPI_MultiOfferSharedOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paths":{
			"/services/foo/v1/chat/completions": {},
			"/services/bar/v1": {}
		}}`))
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	u := ui.NewForTest(&bytes.Buffer{}, &stderr)
	preflightOpenAPI(context.Background(), u, srv.URL)

	if got := stderr.String(); !strings.Contains(got, "2 offers in one /openapi.json") {
		t.Fatalf("expected multi-offer warning, got: %q", got)
	}
}

func TestPreflightOpenAPI_AllTestnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paths":{
			"/services/foo/v1/chat/completions": {
				"post": {"x-payment-info": {"accepts": [{"network": "eip155:84532"}]}}
			}
		}}`))
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	u := ui.NewForTest(&bytes.Buffer{}, &stderr)
	allTestnet := preflightOpenAPI(context.Background(), u, srv.URL)

	if !allTestnet {
		t.Fatal("expected allTestnet=true for an origin whose only accepted network is base-sepolia")
	}
	if got := stderr.String(); !strings.Contains(got, "eip155:84532 (testnet)") {
		t.Fatalf("expected testnet warning, got: %q", got)
	}
}

func TestPreflightOpenAPI_MixedMainnetAndTestnetIsNotAllTestnet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paths":{
			"/services/foo/v1/chat/completions": {
				"post": {"x-payment-info": {"accepts": [
					{"network": "eip155:84532"},
					{"network": "eip155:8453"}
				]}}
			}
		}}`))
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	u := ui.NewForTest(&bytes.Buffer{}, &stderr)
	allTestnet := preflightOpenAPI(context.Background(), u, srv.URL)

	if allTestnet {
		t.Fatal("expected allTestnet=false when a mainnet network is also accepted")
	}
	if got := stderr.String(); strings.Contains(got, "testnet") {
		t.Fatalf("did not expect a testnet warning when base mainnet is accepted, got: %q", got)
	}
}

func TestPreflightOpenAPI_UnreachableOrNon200(t *testing.T) {
	// Non-200: server up but returns an error status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	var stderr bytes.Buffer
	u := ui.NewForTest(&bytes.Buffer{}, &stderr)
	preflightOpenAPI(context.Background(), u, srv.URL)

	if got := stderr.String(); !strings.Contains(got, "returned HTTP 500") {
		t.Fatalf("expected HTTP-500 warning, got: %q", got)
	}

	// Unreachable: close the server first so the connection is refused.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	origin := srv2.URL
	srv2.Close()

	stderr.Reset()
	preflightOpenAPI(context.Background(), u, origin)

	if got := stderr.String(); !strings.Contains(got, "could not fetch") {
		t.Fatalf("expected unreachable-origin warning, got: %q", got)
	}
}
