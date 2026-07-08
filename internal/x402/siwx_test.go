package x402

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	signinwithx "github.com/x402-foundation/x402/go/v2/extensions/signinwithx"
)

// signSIWX builds and signs a valid EIP-4361 message with a fresh key,
// mirroring what a wallet's personal_sign produces (V as 27/28).
func signSIWX(t *testing.T, domain, nonce string, issuedAt time.Time) (msg, sig, wallet string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	msg = fmt.Sprintf(`%s wants you to sign in with your Ethereum account:
%s

Sign in to access your paid results.

URI: https://%s/services/audit/auth
Version: 1
Chain ID: 8453
Nonce: %s
Issued At: %s`, domain, addr, domain, nonce, issuedAt.UTC().Format(time.RFC3339))

	digest := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(msg), msg)))
	raw, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw[64] += 27 // wallet-style V
	return msg, "0x" + hex.EncodeToString(raw), strings.ToLower(addr)
}

func TestSIWX_VerifyMessage_RoundTrip(t *testing.T) {
	a, err := NewSIWXAuthenticator(0, 0)
	if err != nil {
		t.Fatalf("NewSIWXAuthenticator: %v", err)
	}
	now := time.Now()
	msg, sig, wallet := signSIWX(t, "shop.example.com", "n-1", now)

	got, err := a.VerifyMessage(msg, sig, "shop.example.com", now)
	if err != nil {
		t.Fatalf("VerifyMessage: %v", err)
	}
	if got != wallet {
		t.Fatalf("wallet = %s, want %s", got, wallet)
	}

	// Nonce replay must fail.
	if _, err := a.VerifyMessage(msg, sig, "shop.example.com", now); err == nil {
		t.Fatal("replayed nonce accepted")
	}
}

func TestSIWX_VerifyMessage_Rejections(t *testing.T) {
	a, _ := NewSIWXAuthenticator(0, 0)
	now := time.Now()

	// Wrong domain binding.
	msg, sig, _ := signSIWX(t, "evil.example.com", "n-2", now)
	if _, err := a.VerifyMessage(msg, sig, "shop.example.com", now); err == nil {
		t.Error("wrong-domain message accepted")
	}

	// Stale Issued At (outside the window).
	msg, sig, _ = signSIWX(t, "shop.example.com", "n-3", now.Add(-time.Hour))
	if _, err := a.VerifyMessage(msg, sig, "shop.example.com", now); err == nil {
		t.Error("stale message accepted")
	}

	// Future-dated Issued At.
	msg, sig, _ = signSIWX(t, "shop.example.com", "n-4", now.Add(time.Hour))
	if _, err := a.VerifyMessage(msg, sig, "shop.example.com", now); err == nil {
		t.Error("future-dated message accepted")
	}

	// Signature from a different key than the claimed address.
	msg, _, _ = signSIWX(t, "shop.example.com", "n-5", now)
	_, otherSig, _ := signSIWX(t, "shop.example.com", "n-6", now)
	if _, err := a.VerifyMessage(msg, otherSig, "shop.example.com", now); err == nil {
		t.Error("mismatched signer accepted")
	}
}

func TestSIWX_Session_MintVerifyExpiry(t *testing.T) {
	a, _ := NewSIWXAuthenticator(0, time.Hour)
	now := time.Now()
	token := a.MintSession("0xAbC0000000000000000000000000000000000001", now)

	wallet, err := a.VerifySession(token, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if wallet != "0xabc0000000000000000000000000000000000001" {
		t.Fatalf("wallet = %s, want lowercased mint address", wallet)
	}

	if _, err := a.VerifySession(token, now.Add(2*time.Hour)); err == nil {
		t.Error("expired session accepted")
	}
	if _, err := a.VerifySession(token+"x", now); err == nil {
		t.Error("tampered session accepted")
	}

	// A different authenticator (fresh secret, e.g. restarted verifier)
	// must reject the token.
	b, _ := NewSIWXAuthenticator(0, time.Hour)
	if _, err := b.VerifySession(token, now); err == nil {
		t.Error("cross-secret session accepted")
	}
}

func TestSIWX_Authenticate_HeaderAndCookieForms(t *testing.T) {
	a, _ := NewSIWXAuthenticator(0, 0)
	now := time.Now()
	msg, sig, wallet := signSIWX(t, "shop.example.com", "n-7", now)

	// SIWX header form.
	req := httptest.NewRequest(http.MethodGet, "/services/audit/jobs/1/result", nil)
	req.Header.Set("Authorization", "SIWX "+
		base64.StdEncoding.EncodeToString([]byte(msg))+"."+
		base64.StdEncoding.EncodeToString([]byte(sig)))
	got, err := a.Authenticate(req, "shop.example.com", now)
	if err != nil || got != wallet {
		t.Fatalf("SIWX header auth = (%q, %v), want (%q, nil)", got, err, wallet)
	}

	// Session token via Bearer and cookie.
	token := a.MintSession(wallet, now)
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if got, err := a.Authenticate(req, "shop.example.com", now); err != nil || got != wallet {
		t.Fatalf("bearer session auth = (%q, %v)", got, err)
	}
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: SIWXSessionCookie, Value: token})
	if got, err := a.Authenticate(req, "shop.example.com", now); err != nil || got != wallet {
		t.Fatalf("cookie session auth = (%q, %v)", got, err)
	}

	// No credential at all.
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, err := a.Authenticate(req, "shop.example.com", now); err == nil {
		t.Error("credential-less request authenticated")
	}
}

// TestSIWX_SpecHeader_RoundTrip pins the additive x402 sign-in-with-x
// transport (F3): a base64-JSON payload in the SIGN-IN-WITH-X header, built
// and signed exactly as a stock x402 SDK client would, authenticates through
// the same checks as the native Authorization: SIWX form.
func TestSIWX_SpecHeader_RoundTrip(t *testing.T) {
	a, err := NewSIWXAuthenticator(0, 0)
	if err != nil {
		t.Fatalf("NewSIWXAuthenticator: %v", err)
	}
	now := time.Now()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	payload := signinwithx.Payload{
		Domain:   "shop.example.com",
		Address:  addr,
		URI:      "https://shop.example.com/services/audit/auth",
		Version:  "1",
		ChainID:  "eip155:8453",
		Type:     "eip191",
		Nonce:    "specnonce0001",
		IssuedAt: now.UTC().Format(time.RFC3339),
	}
	// Sign the canonical EIP-4361 message the server will reconstruct.
	message, err := signinwithx.CreateMessage(payload)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	digest := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)))
	raw, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw[64] += 27
	payload.Signature = "0x" + hex.EncodeToString(raw)

	header, err := signinwithx.EncodeHeader(payload)
	if err != nil {
		t.Fatalf("EncodeHeader: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/services/audit/reports/1", nil)
	req.Header.Set("SIGN-IN-WITH-X", header)

	got, err := a.Authenticate(req, "shop.example.com", now)
	if err != nil {
		t.Fatalf("Authenticate(SIGN-IN-WITH-X) = %v", err)
	}
	if got != strings.ToLower(addr) {
		t.Fatalf("wallet = %s, want %s", got, strings.ToLower(addr))
	}

	// Wrong host must be rejected (domain binding still enforced).
	req2 := httptest.NewRequest(http.MethodGet, "/services/audit/reports/1", nil)
	req2.Header.Set("SIGN-IN-WITH-X", header)
	if _, err := a.Authenticate(req2, "evil.example.com", now); err == nil {
		t.Fatal("spec header authenticated against the wrong domain")
	}
}
