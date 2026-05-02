package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelloHandler(t *testing.T) {
	handler := HelloHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Payment-Status", "paid")
	req.Header.Set("X-Payment-Tx", "0xabc123")
	req.Header.Set("User-Agent", "test-agent")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Demo != "hello" {
		t.Errorf("expected demo=hello, got %q", resp.Demo)
	}
	if resp.Payment.Status != "paid" {
		t.Errorf("expected payment.status=paid, got %q", resp.Payment.Status)
	}
	if resp.Payment.Tx != "0xabc123" {
		t.Errorf("expected payment.tx=0xabc123, got %q", resp.Payment.Tx)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}
	if msg, _ := data["message"].(string); msg == "" {
		t.Error("expected non-empty message in data")
	}
	echo, _ := data["echo"].(map[string]any)
	if echo == nil {
		t.Fatal("expected echo in data")
	}
	if method, _ := echo["method"].(string); method != "GET" {
		t.Errorf("expected echo.method=GET, got %q", method)
	}
}

func TestBlocksHandler_NoServer(t *testing.T) {
	// Blocks handler with unreachable eRPC should still return a response with errors.
	handler := BlocksHandler("http://127.0.0.1:1") // port 1 = unreachable

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
	if resp.Demo != "blocks" {
		t.Errorf("expected demo=blocks, got %q", resp.Demo)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}
	// Should have errors since eRPC is unreachable.
	if _, hasErrors := data["errors"]; !hasErrors {
		t.Error("expected errors in response when eRPC is unreachable")
	}
}

func TestBlocksHandler_MockRPC(t *testing.T) {
	// Mock eRPC server.
	mockRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		var result string
		switch req.Method {
		case "eth_blockNumber":
			result = `"0x10"`
		case "eth_gasPrice":
			result = `"0x3b9aca00"`
		case "eth_chainId":
			result = `"0x2105"`
		case "eth_getBlockByNumber":
			result = `{"number":"0x10","timestamp":"0x60000000","gasUsed":"0x5208","gasLimit":"0x1c9c380","baseFeePerGas":"0x3b9aca00","transactions":[]}`
		default:
			result = `null`
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  json.RawMessage(result),
		})
	}))
	defer mockRPC.Close()

	handler := BlocksHandler(mockRPC.URL)
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
	if resp.Demo != "blocks" {
		t.Errorf("expected demo=blocks, got %q", resp.Demo)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}
	if data["blockNumber"] == nil {
		t.Error("expected blockNumber in response")
	}
	if data["chainId"] == nil {
		t.Error("expected chainId in response")
	}
	if data["gasPrice"] == nil {
		t.Error("expected gasPrice in response")
	}
}

func TestQuantHandler_MockRPC(t *testing.T) {
	callCount := 0
	mockRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		var result string
		switch req.Method {
		case "eth_blockNumber":
			result = `"0x10"`
		case "eth_chainId":
			result = `"0x2105"`
		case "eth_getBlockByNumber":
			result = `{"number":"0x10","timestamp":"0x60000000","gasUsed":"0x5208","gasLimit":"0x1c9c380","baseFeePerGas":"0x3b9aca00","transactions":["0x1","0x2"]}`
		default:
			result = `null`
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  json.RawMessage(result),
		})
	}))
	defer mockRPC.Close()

	handler := QuantHandler(mockRPC.URL)
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
	if resp.Demo != "quant" {
		t.Errorf("expected demo=quant, got %q", resp.Demo)
	}

	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}
	if data["gasAnalysis"] == nil {
		t.Error("expected gasAnalysis in response")
	}
	if data["txVolume"] == nil {
		t.Error("expected txVolume in response")
	}
	if data["gasUtilization"] == nil {
		t.Error("expected gasUtilization in response")
	}
}

func TestResponseEnvelope(t *testing.T) {
	handler := HelloHandler()
	req := httptest.NewRequest(http.MethodGet, "/test?foo=bar", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if resp.Payment.Status == "" {
		t.Error("expected non-empty payment status")
	}
}
