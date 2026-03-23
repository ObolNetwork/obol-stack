package erc8004

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSplitSignature(t *testing.T) {
	// 65-byte signature: 32 bytes r + 32 bytes s + 1 byte v
	sig := "0x" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + // r (32 bytes)
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + // s (32 bytes)
		"00" // v = 0

	r, s, v, err := splitSignature(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != "0x"+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("r mismatch")
	}
	if s != "0x"+"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("s mismatch")
	}
	if v != 0 {
		t.Errorf("v = %d, want 0", v)
	}
}

func TestSplitSignature_V27(t *testing.T) {
	sig := "0x" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" +
		"1b" // v = 27 → yParity 0

	_, _, v, err := splitSignature(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Errorf("v = %d, want 0", v)
	}
}

func TestSplitSignature_V28(t *testing.T) {
	sig := "0x" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" +
		"1c" // v = 28 → yParity 1

	_, _, v, err := splitSignature(sig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 1 {
		t.Errorf("v = %d, want 1", v)
	}
}

func TestSplitSignature_InvalidLength(t *testing.T) {
	_, _, _, err := splitSignature("0xdeadbeef")
	if err == nil {
		t.Fatal("expected error for invalid length")
	}
}

func TestPostSponsor_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req SponsoredRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.AgentAddress != "0x1234" {
			t.Errorf("agentAddress = %q, want 0x1234", req.AgentAddress)
		}

		json.NewEncoder(w).Encode(SponsoredRegisterResponse{
			Success: true,
			AgentID: 42,
			TxHash:  "0xabc",
		})
	}))
	defer srv.Close()

	result, err := postSponsor(context.Background(), http.DefaultClient, srv.URL, SponsoredRegisterRequest{
		AgentAddress: "0x1234",
		AgentURI:     "https://example.com/.well-known/agent-registration.json",
		Deadline:     9999999999,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AgentID != 42 {
		t.Errorf("agentId = %d, want 42", result.AgentID)
	}
	if result.TxHash != "0xabc" {
		t.Errorf("txHash = %q, want 0xabc", result.TxHash)
	}
}

func TestPostSponsor_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()

	_, err := postSponsor(context.Background(), http.DefaultClient, srv.URL, SponsoredRegisterRequest{})
	if err == nil {
		t.Fatal("expected error for non-JSON 500 response")
	}
}

func TestSponsoredRegister_NoSponsor(t *testing.T) {
	signer := NewRemoteSigner("http://unused")
	_, _, err := SponsoredRegister(context.Background(), signer, "https://example.com", BaseSepolia)
	if err == nil {
		t.Fatal("expected error for unsupported sponsor")
	}
}

func TestSponsoredRegister_Integration(t *testing.T) {
	// Mock both the remote-signer and sponsor APIs.
	signerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/keys":
			json.NewEncoder(w).Encode(keysResponse{
				Keys: []string{"0xAbCd1234567890abcdef1234567890abcdef1234"},
			})
		default:
			// Return a 65-byte signature for any signing request.
			sig := "0x" +
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" +
				"1b"
			json.NewEncoder(w).Encode(signResponse{Signature: sig})
		}
	}))
	defer signerSrv.Close()

	sponsorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SponsoredRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode sponsor request: %v", err)
		}
		if req.AgentAddress == "" {
			t.Error("expected non-empty agentAddress")
		}
		if req.IntentSignature == "" {
			t.Error("expected non-empty intentSignature")
		}
		json.NewEncoder(w).Encode(SponsoredRegisterResponse{
			Success: true,
			AgentID: 99,
			TxHash:  "0xdeadbeef",
		})
	}))
	defer sponsorSrv.Close()

	signer := NewRemoteSigner(signerSrv.URL)
	net := NetworkConfig{
		Name:            "test-sponsored",
		ChainID:         1,
		RegistryAddress: "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432",
		SponsorURL:      sponsorSrv.URL,
		DelegateAddress: "0x0000000000000000000000000000000000001234",
	}

	agentID, txHash, err := SponsoredRegister(context.Background(), signer, "https://example.com/.well-known/agent-registration.json", net)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentID.Int64() != 99 {
		t.Errorf("agentID = %d, want 99", agentID.Int64())
	}
	if txHash != "0xdeadbeef" {
		t.Errorf("txHash = %q, want 0xdeadbeef", txHash)
	}
}

func TestPostSponsor_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SponsoredRegisterResponse{
			Success: false,
			Error:   "insufficient funds",
		})
	}))
	defer srv.Close()

	_, err := postSponsor(context.Background(), http.DefaultClient, srv.URL, SponsoredRegisterRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
}
