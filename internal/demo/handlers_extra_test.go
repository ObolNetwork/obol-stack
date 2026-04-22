package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOracleHandler_BlockNumberFailureEarlyReturn verifies that when
// eth_blockNumber fails the handler returns an errors-only body and does
// NOT attempt to compute recentBlocks/gasAnalysis/txVolume from a zero
// blockNum — that would produce nonsense output for paying customers.
func TestOracleHandler_BlockNumberFailureEarlyReturn(t *testing.T) {
	var blockNumberCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2105"}`))
		case "eth_blockNumber":
			blockNumberCalls++
			// Return an RPC-level error to trigger the early-return branch.
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`))
		default:
			t.Errorf("unexpected RPC call %q after eth_blockNumber failure — early return didn't trigger", req.Method)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer srv.Close()

	handler := OracleHandler(srv.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if blockNumberCalls != 1 {
		t.Fatalf("eth_blockNumber called %d times, want 1", blockNumberCalls)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}

	// Must surface the upstream failure.
	errs, ok := data["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("expected errors entry, got %+v", data)
	}
	joined := ""
	for _, e := range errs {
		if s, ok := e.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "blockNumber") {
		t.Errorf("errors should mention blockNumber failure, got %q", joined)
	}

	// Must NOT have attempted the downstream computations.
	for _, forbidden := range []string{"recentBlocks", "gasAnalysis", "txVolume", "gasUtilization", "latestBlockNumber"} {
		if _, present := data[forbidden]; present {
			t.Errorf("early-return branch leaked %q into response: %+v", forbidden, data)
		}
	}
}

// TestExtractPayment_DefaultStatus verifies the status fallback when no
// X-Payment-Status header is present. This is the branch the existing
// TestHelloHandler skips (it always sets the header).
func TestExtractPayment_DefaultStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Intentionally no X-Payment-Status header.

	info := extractPayment(req)
	if info.Status != "paid" {
		t.Errorf("default Status = %q, want %q (firstNonEmpty fallback)", info.Status, "paid")
	}
	if info.Tx != "" {
		t.Errorf("Tx = %q, want empty", info.Tx)
	}
	if info.Payer != "" {
		t.Errorf("Payer = %q, want empty", info.Payer)
	}
}

func TestExtractPayment_PassesThroughHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Payment-Status", "settled")
	req.Header.Set("X-Payment-Tx", "0xdeadbeef")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	info := extractPayment(req)
	if info.Status != "settled" {
		t.Errorf("Status = %q, want %q", info.Status, "settled")
	}
	if info.Tx != "0xdeadbeef" {
		t.Errorf("Tx = %q, want 0xdeadbeef", info.Tx)
	}
	if info.Payer != "10.0.0.1" {
		t.Errorf("Payer = %q, want 10.0.0.1", info.Payer)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"first wins", []string{"a", "b"}, "a"},
		{"empty falls through", []string{"", "b"}, "b"},
		{"all empty", []string{"", "", ""}, ""},
		{"nil args", nil, ""},
		{"single", []string{"x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.in...); got != tt.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
