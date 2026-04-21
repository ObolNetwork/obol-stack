package erc8004

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestRemoteSigner_GetAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/keys" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(keysResponse{
			Keys: []string{"0x1234567890abcdef1234567890abcdef12345678"},
		})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr, err := signer.GetAddress(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	if addr != want {
		t.Errorf("got %s, want %s", addr.Hex(), want.Hex())
	}
}

func TestRemoteSigner_GetAddress_NoKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(keysResponse{Keys: []string{}})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	_, err := signer.GetAddress(context.Background())
	if err == nil {
		t.Fatal("expected error for no keys")
	}
}

func TestRemoteSigner_SignTransaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var tx SignTxRequest
		if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if tx.ChainID != "84532" {
			t.Errorf("chain_id = %q, want 84532", tx.ChainID)
		}

		json.NewEncoder(w).Encode(signResponse{
			SignedTransaction: "0x02f8deadbeef",
		})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	signed, err := signer.SignTransaction(context.Background(), addr, SignTxRequest{
		ChainID:              "84532",
		To:                   "0x8004A818BFB912233c491871b3d84c89A494BD9e",
		Nonce:                "0",
		GasLimit:             "100000",
		MaxFeePerGas:         "1000000000",
		MaxPriorityFeePerGas: "1000000",
		Value:                "0",
		Data:                 "0x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signed != "0x02f8deadbeef" {
		t.Errorf("got %q, want 0x02f8deadbeef", signed)
	}
}

func TestRemoteSigner_SignTypedData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var data EIP712TypedData
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if data.PrimaryType != "Registration" {
			t.Errorf("primaryType = %q, want Registration", data.PrimaryType)
		}

		json.NewEncoder(w).Encode(signResponse{
			Signature: "0xdeadbeef",
		})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	sig, err := signer.SignTypedData(context.Background(), addr, EIP712TypedData{
		Types: map[string][]EIP712Field{
			"Registration": {{Name: "agent", Type: "address"}},
		},
		PrimaryType: "Registration",
		Domain:      map[string]interface{}{"name": "test"},
		Message:     map[string]interface{}{"agent": addr.Hex()},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != "0xdeadbeef" {
		t.Errorf("got %q, want 0xdeadbeef", sig)
	}
}

func TestRemoteSigner_SignTransaction_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(signResponse{Error: "SIGNER_NOT_FOUND"})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	_, err := signer.SignTransaction(context.Background(), addr, SignTxRequest{ChainID: "1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteSigner_SignTransaction_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	_, err := signer.SignTransaction(context.Background(), addr, SignTxRequest{ChainID: "1"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestRemoteSigner_SignTypedData_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(signResponse{Error: "UNSUPPORTED_TYPE"})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	_, err := signer.SignTypedData(context.Background(), addr, EIP712TypedData{
		PrimaryType: "Test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoteSigner_SignTypedData_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "bad gateway")
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	_, err := signer.SignTypedData(context.Background(), addr, EIP712TypedData{
		PrimaryType: "Test",
	})
	if err == nil {
		t.Fatal("expected error for HTTP 502")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestRemoteSigner_GetAddress_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "service unavailable")
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	_, err := signer.GetAddress(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestRemoteTransactOpts(t *testing.T) {
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	to := common.HexToAddress("0x8004A818BFB912233c491871b3d84c89A494BD9e")
	chainID := big.NewInt(84532)
	maxUint64 := new(big.Int).SetUint64(^uint64(0))
	feeCap := new(big.Int).Add(new(big.Int).Set(maxUint64), big.NewInt(25))
	tipCap := new(big.Int).Add(new(big.Int).Set(maxUint64), big.NewInt(7))
	value := new(big.Int).Add(new(big.Int).Set(maxUint64), big.NewInt(12345))
	var body map[string]json.RawMessage
	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This verifies the signer receives proper requests.
		if r.URL.Path == "/api/v1/keys" {
			json.NewEncoder(w).Encode(keysResponse{Keys: []string{addr.Hex()}})
			return
		}
		// For transaction signing, return the error since we can't easily
		// produce a valid signed tx in a unit test.
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		json.NewEncoder(w).Encode(signResponse{Error: "test: not implemented"})
	}))
	defer srv.Close()

	signer := NewRemoteSigner(srv.URL)
	opts := signer.RemoteTransactOpts(context.Background(), addr, chainID)

	if opts.From != addr {
		t.Errorf("From = %s, want %s", opts.From.Hex(), addr.Hex())
	}
	if opts.Signer == nil {
		t.Fatal("Signer should not be nil")
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     7,
		To:        &to,
		Gas:       100000,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
		Value:     value,
		Data:      []byte{0xde, 0xad, 0xbe, 0xef},
	})

	_, err := opts.Signer(addr, tx)
	if err == nil {
		t.Fatal("expected signer error")
	}
	if !strings.Contains(err.Error(), "test: not implemented") {
		t.Fatalf("unexpected signer error: %v", err)
	}

	if path != "/api/v1/sign/"+addr.Hex()+"/transaction" {
		t.Fatalf("unexpected request path: %s", path)
	}

	assertJSONString(t, body, "chain_id", "84532")
	assertJSONString(t, body, "nonce", "7")
	assertJSONString(t, body, "gas_limit", "100000")
	assertJSONString(t, body, "max_fee_per_gas", feeCap.String())
	assertJSONString(t, body, "max_priority_fee_per_gas", tipCap.String())
	assertJSONString(t, body, "value", value.String())
	assertJSONString(t, body, "to", to.Hex())
	assertJSONString(t, body, "data", "0xdeadbeef")
}

func assertJSONString(t *testing.T, body map[string]json.RawMessage, field, want string) {
	t.Helper()
	raw, ok := body[field]
	if !ok {
		t.Fatalf("missing field %q", field)
	}

	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("field %q should be a JSON string, got %s: %v", field, string(raw), err)
	}
	if got != want {
		t.Fatalf("field %q = %q, want %q", field, got, want)
	}
}

func TestHexToBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"0xdeadbeef", 4, false},
		{"deadbeef", 4, false},
		{"0x", 0, false},
		{"invalid", 0, true},
	}
	for _, tt := range tests {
		b, err := hexToBytes(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("hexToBytes(%q) error = %v, wantErr %v", tt.input, err, tt.err)
		}
		if !tt.err && len(b) != tt.want {
			t.Errorf("hexToBytes(%q) len = %d, want %d", tt.input, len(b), tt.want)
		}
	}
}
