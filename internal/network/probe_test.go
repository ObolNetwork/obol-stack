package network

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeUpstream_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "eth_chainId") {
			t.Errorf("expected eth_chainId in body, got %s", body)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x14a34"}`)) // 84_532 = base sepolia
	}))
	defer srv.Close()

	res := ProbeUpstream(context.Background(), RPCUpstreamInfo{
		ID: "u1", Endpoint: srv.URL, ChainID: 84532,
	}, 2*time.Second)

	if !res.Reachable {
		t.Fatalf("expected reachable, got err=%q", res.Err)
	}
	if res.ObservedChain != 84532 {
		t.Fatalf("expected observed 84532, got %d", res.ObservedChain)
	}
	if res.Mismatch() {
		t.Fatalf("expected no mismatch")
	}
}

func TestProbeUpstream_ChainMismatch_StalePin(t *testing.T) {
	// Simulates the report's exact failure mode: a custom upstream the operator
	// added pointing at a local Anvil fork that has since been recreated for a
	// different chain (or is now the host's other anvil from a parallel test).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x539"}`)) // 1337
	}))
	defer srv.Close()

	res := ProbeUpstream(context.Background(), RPCUpstreamInfo{
		ID: "custom-84532-0", Endpoint: srv.URL, ChainID: 84532,
	}, 2*time.Second)

	if !res.Reachable {
		t.Fatalf("expected reachable, got err=%q", res.Err)
	}
	if !res.Mismatch() {
		t.Fatalf("expected mismatch (declared 84532 vs observed %d)", res.ObservedChain)
	}
}

func TestProbeUpstream_DeadEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	srv.Close() // close immediately so the URL is unreachable

	res := ProbeUpstream(context.Background(), RPCUpstreamInfo{
		ID: "dead", Endpoint: srv.URL, ChainID: 84532,
	}, 250*time.Millisecond)

	if res.Reachable {
		t.Fatalf("expected unreachable")
	}
	if res.Err == "" {
		t.Fatalf("expected error message")
	}
}

func TestProbeUpstream_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	}))
	defer srv.Close()

	res := ProbeUpstream(context.Background(), RPCUpstreamInfo{
		ID: "broken", Endpoint: srv.URL, ChainID: 84532,
	}, 2*time.Second)

	if res.Reachable {
		t.Fatalf("expected unreachable when JSON-RPC reports error")
	}
	if !strings.Contains(res.Err, "method not found") {
		t.Fatalf("expected error to surface RPC error message, got %q", res.Err)
	}
}

func TestProbeUpstream_EmptyEndpoint(t *testing.T) {
	res := ProbeUpstream(context.Background(), RPCUpstreamInfo{ID: "x", Endpoint: "", ChainID: 1}, time.Second)
	if res.Reachable {
		t.Fatalf("empty endpoint must not be marked reachable")
	}
	if res.Err == "" {
		t.Fatalf("expected error explaining empty endpoint")
	}
}

func TestParseHexUint(t *testing.T) {
	cases := map[string]int{
		"0x1":     1,
		"0x14a34": 84532,
		"0X10":    16,
		"539":     1337,
	}
	for input, want := range cases {
		got, err := parseHexUint(input)
		if err != nil {
			t.Errorf("parseHexUint(%q) errored: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseHexUint(%q) = %d, want %d", input, got, want)
		}
	}
	if _, err := parseHexUint(""); err == nil {
		t.Errorf("expected error for empty string")
	}
}
