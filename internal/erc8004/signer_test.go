package erc8004

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
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

func TestRemoteTransactOpts(t *testing.T) {
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	chainID := big.NewInt(84532)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This verifies the signer receives proper requests.
		if r.URL.Path == "/api/v1/keys" {
			json.NewEncoder(w).Encode(keysResponse{Keys: []string{addr.Hex()}})
			return
		}
		// For transaction signing, return the error since we can't easily
		// produce a valid signed tx in a unit test.
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
