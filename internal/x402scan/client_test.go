package x402scan

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// localSigner implements MessageSigner with an in-memory key, mirroring the
// remote-signer's EIP-191 semantics (prefix + keccak + 27-offset v).
type localSigner struct{ key *ecdsa.PrivateKey }

func (s localSigner) SignMessage(_ context.Context, _ common.Address, message string) (string, error) {
	sig, err := crypto.Sign(accounts.TextHash([]byte(message)), s.key)
	if err != nil {
		return "", err
	}
	sig[64] += 27
	return "0x" + hex.EncodeToString(sig), nil
}

// recoverSIWX re-renders the SIWE message from a decoded payload and
// recovers the signer address — the same verification x402scan performs.
func recoverSIWX(t *testing.T, payload siwxPayload) common.Address {
	t.Helper()
	msg := FormatSIWEMessage(payload.SIWXInfo, common.HexToAddress(payload.Address))
	sig, err := hex.DecodeString(strings.TrimPrefix(payload.Signature, "0x"))
	if err != nil || len(sig) != 65 {
		t.Fatalf("bad signature encoding: %v (len %d)", err, len(sig))
	}
	sig[64] -= 27
	pub, err := crypto.SigToPub(accounts.TextHash([]byte(msg)), sig)
	if err != nil {
		t.Fatalf("recover signer: %v", err)
	}
	return crypto.PubkeyToAddress(*pub)
}

// newRegistry stands up a fake x402scan: 402 + SIWX challenge without the
// header, full verification + canned result with it.
func newRegistry(t *testing.T, result string) (*httptest.Server, *SIWXInfo) {
	t.Helper()
	issued := &SIWXInfo{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != registerOriginPath {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Origin string `json:"origin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Origin == "" {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		header := r.Header.Get(siwxHeader)
		if header == "" {
			*issued = SIWXInfo{
				Domain:         "x402scan.test",
				URI:            "http://" + r.Host + registerOriginPath,
				Version:        "1",
				ChainID:        "eip155:8453",
				Type:           "eip191",
				Nonce:          "a3f1c2d4e5b6978812345678deadbeef",
				IssuedAt:       "2026-07-03T12:00:00.000Z",
				ExpirationTime: "2026-07-03T12:05:00.000Z",
				Statement:      "Sign in to verify your wallet identity",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			challenge := map[string]any{
				"x402Version": 2,
				"error":       "SIWX authentication required",
				"accepts":     []any{},
				"extensions": map[string]any{
					"sign-in-with-x": map[string]any{
						"info": issued,
						"supportedChains": []map[string]string{
							{"chainId": "eip155:8453", "type": "eip191"},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(challenge)
			return
		}

		raw, err := base64.StdEncoding.DecodeString(header)
		if err != nil {
			http.Error(w, `{"error":"siwx_malformed"}`, http.StatusPaymentRequired)
			return
		}
		var payload siwxPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			http.Error(w, `{"error":"siwx_malformed"}`, http.StatusPaymentRequired)
			return
		}
		if payload.Nonce != issued.Nonce || payload.Domain != issued.Domain || payload.URI != issued.URI {
			http.Error(w, `{"error":"siwx_challenge_mismatch"}`, http.StatusPaymentRequired)
			return
		}
		if got := recoverSIWX(t, payload); got != common.HexToAddress(payload.Address) {
			http.Error(w, `{"error":"siwx_invalid_signature"}`, http.StatusPaymentRequired)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(srv.Close)
	return srv, issued
}

func TestRegisterOrigin_FullSIWXHandshake(t *testing.T) {
	srv, _ := newRegistry(t, `{"success":true,"registered":2,"siwx":0,"failed":1,"skipped":0,"deprecated":1,"total":3,"source":"openapi","failedDetails":[{"url":"https://s.example/services/x","error":"no 402 challenge","status":200}],"warning":"no contact email"}`)

	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)

	result, err := NewClient(srv.URL).RegisterOrigin(context.Background(), "https://seller.example", addr, localSigner{key})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Registered != 2 || result.Total != 3 || result.Source != "openapi" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.FailedList) != 1 || result.FailedList[0].Error != "no 402 challenge" {
		t.Fatalf("unexpected failedDetails: %+v", result.FailedList)
	}
	if result.Warning != "no contact email" {
		t.Fatalf("unexpected warning: %q", result.Warning)
	}
}

func TestRegisterOrigin_WrongKeyIsRejected(t *testing.T) {
	srv, _ := newRegistry(t, `{"success":true}`)

	key, _ := crypto.GenerateKey()
	imposter, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey) // claims key's address...

	_, err := NewClient(srv.URL).RegisterOrigin(context.Background(), "https://seller.example", addr, localSigner{imposter})
	if err == nil || !strings.Contains(err.Error(), "siwx_invalid_signature") {
		t.Fatalf("expected siwx_invalid_signature rejection, got %v", err)
	}
}

func TestRegisterOrigin_NoDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"error":{"type":"no_discovery","message":"no discovery document found"}}`))
	}))
	t.Cleanup(srv.Close)

	key, _ := crypto.GenerateKey()
	_, err := NewClient(srv.URL).RegisterOrigin(context.Background(), "https://seller.example", crypto.PubkeyToAddress(key.PublicKey), localSigner{key})
	if !errors.Is(err, ErrNoDiscovery) {
		t.Fatalf("expected ErrNoDiscovery, got %v", err)
	}
	if !strings.Contains(err.Error(), "no discovery document found") {
		t.Fatalf("expected registry message in error, got %v", err)
	}
}

func TestRegisterOrigin_NoValidResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"success":false,"error":{"type":"no_valid_resources","message":"0 of 2 endpoints answered a 402"}}`))
	}))
	t.Cleanup(srv.Close)

	key, _ := crypto.GenerateKey()
	_, err := NewClient(srv.URL).RegisterOrigin(context.Background(), "https://seller.example", crypto.PubkeyToAddress(key.PublicKey), localSigner{key})
	if !errors.Is(err, ErrNoValidResources) {
		t.Fatalf("expected ErrNoValidResources, got %v", err)
	}
}

func TestRegisterOrigin_NoValidResources_FailedDetails(t *testing.T) {
	// Mirrors the 200-partial-failure golden fixture's failedDetails shape,
	// but on the 422 rejection path — the asymmetry that dropped per-endpoint
	// diagnostics before registryError grew a FailedDetails field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"success":false,"error":{"type":"no_valid_resources","message":"0 of 2 endpoints answered a 402"},"failedDetails":[{"url":"https://s.example/services/x","error":"wrong network: eip155:84532 not indexed","status":402}]}`))
	}))
	t.Cleanup(srv.Close)

	key, _ := crypto.GenerateKey()
	_, err := NewClient(srv.URL).RegisterOrigin(context.Background(), "https://seller.example", crypto.PubkeyToAddress(key.PublicKey), localSigner{key})
	if !errors.Is(err, ErrNoValidResources) {
		t.Fatalf("expected ErrNoValidResources, got %v", err)
	}
	if !strings.Contains(err.Error(), "https://s.example/services/x") || !strings.Contains(err.Error(), "wrong network: eip155:84532 not indexed") || !strings.Contains(err.Error(), "status 402") {
		t.Fatalf("expected per-endpoint failure detail in error, got %v", err)
	}
}

func TestFormatSIWEMessage_Golden(t *testing.T) {
	info := SIWXInfo{
		Domain:         "x402scan.com",
		URI:            "https://x402scan.com/api/x402/registry/register-origin",
		Version:        "1",
		ChainID:        "eip155:8453",
		Type:           "eip191",
		Nonce:          "deadbeefdeadbeefdeadbeefdeadbeef",
		IssuedAt:       "2026-07-03T12:00:00.000Z",
		ExpirationTime: "2026-07-03T12:05:00.000Z",
		Statement:      "Sign in to verify your wallet identity",
	}
	addr := common.HexToAddress("0x8ba1f109551bd432803012645ac136ddd64dba72")

	want := "x402scan.com wants you to sign in with your Ethereum account:\n" +
		"0x8ba1f109551bD432803012645Ac136ddd64DBA72\n" +
		"\n" +
		"Sign in to verify your wallet identity\n" +
		"\n" +
		"URI: https://x402scan.com/api/x402/registry/register-origin\n" +
		"Version: 1\n" +
		"Chain ID: 8453\n" +
		"Nonce: deadbeefdeadbeefdeadbeefdeadbeef\n" +
		"Issued At: 2026-07-03T12:00:00.000Z\n" +
		"Expiration Time: 2026-07-03T12:05:00.000Z"

	if got := FormatSIWEMessage(info, addr); got != want {
		t.Fatalf("SIWE message mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatSIWEMessage_NoStatementFollowsABNF(t *testing.T) {
	info := SIWXInfo{
		Domain:   "x402scan.com",
		URI:      "https://x402scan.com/api",
		Version:  "1",
		ChainID:  "eip155:8453",
		Nonce:    "n0nce",
		IssuedAt: "2026-07-03T12:00:00.000Z",
	}
	addr := common.HexToAddress("0x8ba1f109551bd432803012645ac136ddd64dba72")
	got := FormatSIWEMessage(info, addr)
	// EIP-4361 ABNF without a statement: address LF, blank line, blank line, fields.
	if !strings.Contains(got, addr.Hex()+"\n\n\nURI: ") {
		t.Fatalf("no-statement layout wrong:\n%s", got)
	}
}
