package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestQuantHandler_ChainIDFailure verifies that a failing eth_chainId call is
// captured in the "errors" array but does not short-circuit the handler —
// downstream computations (recentBlocks, gasAnalysis, etc.) should still run.
func TestQuantHandler_ChainIDFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"chain id unavailable"}}`))
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x3"}`))
		case "eth_getBlockByNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x3","timestamp":"0x60000000","gasUsed":"0x5208","gasLimit":"0x1c9c380","baseFeePerGas":"0x3b9aca00","transactions":["0x1"]}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer srv.Close()

	handler := QuantHandler(srv.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}

	// chainId failure → surfaces via errors[] but does not break the handler.
	errs, _ := data["errors"].([]any)
	joined := ""
	for _, e := range errs {
		if s, ok := e.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "chainId") {
		t.Errorf("expected chainId in errors, got %q", joined)
	}
	// Downstream computation must still run.
	if data["latestBlockNumber"] == nil {
		t.Error("expected latestBlockNumber to be present despite chainId failure")
	}
	if data["recentBlocks"] == nil {
		t.Error("expected recentBlocks to be present despite chainId failure")
	}
	// chainId must not appear in the report map (we hit the error branch).
	if _, present := data["chainId"]; present {
		t.Error("chainId should not appear in report when the RPC call errored")
	}
}

// TestQuantHandler_PerBlockFetchError exercises the "continue" branch inside
// the per-block loop: some block fetches succeed, one returns an RPC error.
// The successful blocks must still be included; the failed block must show up
// in errors[].
func TestQuantHandler_PerBlockFetchError(t *testing.T) {
	var blockCallCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x5"}`))
		case "eth_getBlockByNumber":
			n := blockCallCount.Add(1)
			// Second block request fails; others succeed.
			if n == 2 {
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"block missing"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x5","timestamp":"0x1","gasUsed":"0x100","gasLimit":"0x1000","baseFeePerGas":"0x3b9aca00","transactions":["0x1"]}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer srv.Close()

	handler := QuantHandler(srv.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}

	// Errors[] must surface the per-block failure.
	errs, _ := data["errors"].([]any)
	joined := ""
	for _, e := range errs {
		if s, ok := e.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "block") {
		t.Errorf("expected per-block error message, got errors = %q", joined)
	}

	// recentBlocks is still populated — only the failed block is skipped.
	blocks, ok := data["recentBlocks"].([]any)
	if !ok {
		t.Fatalf("recentBlocks should be an array, got %T", data["recentBlocks"])
	}
	if len(blocks) == 0 {
		t.Error("expected at least one successful block in recentBlocks")
	}
}

// TestQuantHandler_MalformedBlockJSON exercises the json.Unmarshal error
// branch in the per-block loop: the RPC returns valid JSON-RPC framing but
// the inner "result" field doesn't match the expected block struct shape.
func TestQuantHandler_MalformedBlockJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "eth_getBlockByNumber":
			// result is a string instead of the expected object shape.
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"not-a-block-object"}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer srv.Close()

	handler := QuantHandler(srv.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}

	// Must produce a "decode block" error.
	errs, _ := data["errors"].([]any)
	joined := ""
	for _, e := range errs {
		if s, ok := e.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "decode block") {
		t.Errorf("expected decode block error, got %q", joined)
	}
}

// TestBlocksHandler_LatestBlockFetchFailure exercises the `errs = append(errs,
// ...)` branch at blocks.go:58 — blockNumber/gasPrice/chainId succeed but the
// follow-up eth_getBlockByNumber fails, so "block:" error must surface.
func TestBlocksHandler_LatestBlockFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "eth_blockNumber":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
		case "eth_gasPrice":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x3b9aca00"}`))
		case "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "eth_getBlockByNumber":
			// Fail only the latest-block lookup.
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	defer srv.Close()

	handler := BlocksHandler(srv.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}

	// The latest block fetch failed — there should be an errors entry mentioning "block".
	errs, _ := data["errors"].([]any)
	joined := ""
	for _, e := range errs {
		if s, ok := e.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "block") {
		t.Errorf("expected block error, got %q", joined)
	}
	// The rest of the data should still be present.
	if data["blockNumber"] == nil {
		t.Error("blockNumber should still be present")
	}
	// But latestBlock should NOT be populated (the call errored).
	if _, present := data["latestBlock"]; present {
		t.Error("latestBlock should not be set on fetch failure")
	}
}
