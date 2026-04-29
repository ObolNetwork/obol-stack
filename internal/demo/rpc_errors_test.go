package demo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRPCCall_ErrorBranches(t *testing.T) {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		wantContains string
	}{
		{
			name: "malformed JSON body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{not json`))
			},
			wantContains: "decode response",
		},
		{
			name: "JSON-RPC error field populated",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
			},
			wantContains: "rpc error -32601: method not found",
		},
		{
			name: "HTTP 500 with JSON-RPC error body still surfaces rpc error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"server busy"}}`))
			},
			wantContains: "rpc error -32000: server busy",
		},
		{
			name: "HTTP 200 with non-JSON body returns decode error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`<html>502 Bad Gateway</html>`))
			},
			wantContains: "decode response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			client := srv.Client()
			got, err := rpcCall(client, srv.URL, "eth_blockNumber", "[]")
			if err == nil {
				t.Fatalf("expected error, got nil (result=%s)", string(got))
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantContains)
			}
			if got != nil {
				t.Errorf("expected nil result on error, got %s", string(got))
			}
		})
	}
}

func TestRPCCall_TransportFailure(t *testing.T) {
	// Port 1 is reserved and reliably unreachable without root bound listeners.
	_, err := rpcCall(http.DefaultClient, "http://127.0.0.1:1", "eth_blockNumber", "[]")
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "request failed")
	}
}

func TestRPCCall_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
	}))
	defer srv.Close()

	got, err := rpcCall(srv.Client(), srv.URL, "eth_blockNumber", "[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != `"0x10"` {
		t.Errorf("result = %s, want \"0x10\"", string(got))
	}
}
