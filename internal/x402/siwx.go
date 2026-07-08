package x402

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	signinwithx "github.com/x402-foundation/x402/go/v2/extensions/signinwithx"
)

// SIWX (Sign-In With X, EIP-4361) authentication for gate:auth routes.
//
// Two credential forms are accepted, checked in this order:
//
//  1. A one-shot signed message: `Authorization: SIWX <b64 msg>.<b64 sig>`
//     — an EIP-4361 message signed with EIP-191 personal_sign. Verified
//     statelessly (domain binding + issuedAt window) plus an in-memory
//     nonce cache bounding replay to a verifier restart within the window.
//  2. A session token minted by a prior successful verification, sent as
//     `Authorization: Bearer <token>` or the `obol_siwx` cookie. The token
//     is verifier-signed (HMAC) and self-contained — no session store.
//
// The session secret is generated at startup: a verifier restart invalidates
// outstanding sessions (single-replica deployment; buyers re-sign, browsers
// re-challenge). Smart-wallet (EIP-1271) verification is not yet wired —
// EOA signatures only; contract wallets get a distinct error so callers can
// fall back to capability tokens.

// SIWXSessionCookie is the browser session cookie name.
const SIWXSessionCookie = "obol_siwx"

const (
	// DefaultSIWXWindow bounds how old (or future-dated) a signed
	// message's Issued At may be.
	DefaultSIWXWindow = 10 * time.Minute
	// DefaultSIWXSessionTTL is the lifetime of a minted session token.
	DefaultSIWXSessionTTL = 24 * time.Hour
	// siwxNonceCacheMax caps the replay cache; at the default window a
	// full cache means >160 verifications/sec sustained — beyond that we
	// evict oldest-first rather than grow unbounded.
	siwxNonceCacheMax = 100_000
)

// SIWXAuthenticator verifies EIP-4361 messages and manages session tokens.
type SIWXAuthenticator struct {
	window     time.Duration
	sessionTTL time.Duration
	secret     []byte

	mu     sync.Mutex
	nonces map[string]time.Time // nonce → seen-at
}

// NewSIWXAuthenticator creates an authenticator with a fresh random session
// secret. window/sessionTTL of zero take the defaults.
func NewSIWXAuthenticator(window, sessionTTL time.Duration) (*SIWXAuthenticator, error) {
	if window <= 0 {
		window = DefaultSIWXWindow
	}
	if sessionTTL <= 0 {
		sessionTTL = DefaultSIWXSessionTTL
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate siwx session secret: %w", err)
	}
	return &SIWXAuthenticator{
		window:     window,
		sessionTTL: sessionTTL,
		secret:     secret,
		nonces:     make(map[string]time.Time),
	}, nil
}

// Window returns the accepted Issued-At age, for advertising in challenges.
func (a *SIWXAuthenticator) Window() time.Duration { return a.window }

// SIWXMessage is the parsed subset of an EIP-4361 message we verify.
type SIWXMessage struct {
	Domain         string
	Address        string
	URI            string
	Version        string
	Nonce          string
	IssuedAt       time.Time
	ExpirationTime *time.Time
}

// ParseSIWXMessage parses the EIP-4361 plaintext format. It is strict about
// the fields verification depends on (domain header line, address, URI,
// Version, Nonce, Issued At) and ignores optional fields it doesn't use
// (Statement, Chain ID, Not Before, Request ID, Resources).
func ParseSIWXMessage(text string) (*SIWXMessage, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("siwx: message too short")
	}
	const headerSuffix = " wants you to sign in with your Ethereum account:"
	if !strings.HasSuffix(lines[0], headerSuffix) {
		return nil, fmt.Errorf("siwx: first line is not an EIP-4361 header")
	}
	msg := &SIWXMessage{Domain: strings.TrimSuffix(lines[0], headerSuffix)}
	if msg.Domain == "" {
		return nil, fmt.Errorf("siwx: empty domain")
	}
	msg.Address = strings.TrimSpace(lines[1])
	if !isHexAddress(msg.Address) {
		return nil, fmt.Errorf("siwx: line 2 is not an 0x address")
	}
	for _, line := range lines[2:] {
		key, val, ok := strings.Cut(line, ": ")
		if !ok {
			continue // statement/blank lines
		}
		val = strings.TrimSpace(val)
		switch key {
		case "URI":
			msg.URI = val
		case "Version":
			msg.Version = val
		case "Nonce":
			msg.Nonce = val
		case "Issued At":
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return nil, fmt.Errorf("siwx: invalid Issued At: %w", err)
			}
			msg.IssuedAt = t
		case "Expiration Time":
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return nil, fmt.Errorf("siwx: invalid Expiration Time: %w", err)
			}
			msg.ExpirationTime = &t
		}
	}
	switch {
	case msg.Version != "1":
		return nil, fmt.Errorf("siwx: unsupported Version %q (want 1)", msg.Version)
	case msg.URI == "":
		return nil, fmt.Errorf("siwx: missing URI")
	case msg.Nonce == "":
		return nil, fmt.Errorf("siwx: missing Nonce")
	case msg.IssuedAt.IsZero():
		return nil, fmt.Errorf("siwx: missing Issued At")
	}
	return msg, nil
}

// VerifyMessage checks an EIP-4361 message + EIP-191 signature against the
// expected domain (the request's host authority) and the freshness window,
// consuming the nonce. Returns the checksum-less lowercase wallet address.
func (a *SIWXAuthenticator) VerifyMessage(messageText, signature string, expectedDomain string, now time.Time) (string, error) {
	msg, err := ParseSIWXMessage(messageText)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(msg.Domain, expectedDomain) {
		return "", fmt.Errorf("siwx: message domain %q does not match request host %q", msg.Domain, expectedDomain)
	}
	// Freshness: Issued At within [now-window, now+2m clock skew], plus the
	// message's own Expiration Time when present.
	if msg.IssuedAt.Before(now.Add(-a.window)) {
		return "", fmt.Errorf("siwx: message issued more than %s ago — re-sign with a fresh Issued At", a.window)
	}
	if msg.IssuedAt.After(now.Add(2 * time.Minute)) {
		return "", fmt.Errorf("siwx: message Issued At is in the future — check client clock")
	}
	if msg.ExpirationTime != nil && now.After(*msg.ExpirationTime) {
		return "", fmt.Errorf("siwx: message expired at %s", msg.ExpirationTime.Format(time.RFC3339))
	}

	recovered, err := recoverPersonalSign(messageText, signature)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(recovered, msg.Address) {
		return "", fmt.Errorf("siwx: signature recovers %s, message claims %s", recovered, msg.Address)
	}

	if !a.consumeNonce(msg.Nonce, now) {
		return "", fmt.Errorf("siwx: nonce already used — sign a fresh message")
	}
	return strings.ToLower(recovered), nil
}

// consumeNonce records a nonce, rejecting reuse within the window. Expired
// entries are pruned opportunistically; a full cache evicts oldest-first.
func (a *SIWXAuthenticator) consumeNonce(nonce string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := now.Add(-a.window - 2*time.Minute)
	for n, seen := range a.nonces {
		if seen.Before(cutoff) {
			delete(a.nonces, n)
		}
	}
	if _, dup := a.nonces[nonce]; dup {
		return false
	}
	if len(a.nonces) >= siwxNonceCacheMax {
		var oldest string
		var oldestAt time.Time
		for n, seen := range a.nonces {
			if oldest == "" || seen.Before(oldestAt) {
				oldest, oldestAt = n, seen
			}
		}
		delete(a.nonces, oldest)
	}
	a.nonces[nonce] = now
	return true
}

// recoverPersonalSign recovers the signer address of an EIP-191
// personal_sign signature over the given message text.
func recoverPersonalSign(message, signature string) (string, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil {
		return "", fmt.Errorf("siwx: signature is not hex: %w", err)
	}
	if len(sig) != 65 {
		return "", fmt.Errorf("siwx: signature must be 65 bytes, got %d", len(sig))
	}
	// Wallets emit V as 27/28; go-ethereum expects 0/1.
	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v != 0 && v != 1 {
		return "", fmt.Errorf("siwx: invalid recovery id %d (EIP-1271 contract-wallet signatures are not supported yet — use an EOA)", sig[64])
	}
	norm := make([]byte, 65)
	copy(norm, sig[:64])
	norm[64] = v

	digest := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)))
	pub, err := crypto.SigToPub(digest, norm)
	if err != nil {
		return "", fmt.Errorf("siwx: recover signer: %w", err)
	}
	return crypto.PubkeyToAddress(*pub).Hex(), nil
}

// siwxSessionClaims is the signed session token payload.
type siwxSessionClaims struct {
	Wallet string `json:"wallet"`
	Exp    int64  `json:"exp"`
}

// MintSession returns a self-contained signed session token for a verified
// wallet: base64url(claims).base64url(hmac-sha256).
func (a *SIWXAuthenticator) MintSession(wallet string, now time.Time) string {
	claims, _ := json.Marshal(siwxSessionClaims{
		Wallet: strings.ToLower(wallet),
		Exp:    now.Add(a.sessionTTL).Unix(),
	})
	body := base64.RawURLEncoding.EncodeToString(claims)
	return body + "." + a.signSession(body)
}

// VerifySession validates a session token and returns its wallet.
func (a *SIWXAuthenticator) VerifySession(token string, now time.Time) (string, error) {
	body, mac, ok := strings.Cut(token, ".")
	if !ok {
		return "", fmt.Errorf("siwx: malformed session token")
	}
	if subtle.ConstantTimeCompare([]byte(a.signSession(body)), []byte(mac)) != 1 {
		return "", fmt.Errorf("siwx: session signature mismatch (sessions do not survive verifier restarts — re-authenticate)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("siwx: malformed session claims")
	}
	var claims siwxSessionClaims
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Wallet == "" {
		return "", fmt.Errorf("siwx: malformed session claims")
	}
	if now.Unix() >= claims.Exp {
		return "", fmt.Errorf("siwx: session expired — re-authenticate")
	}
	return claims.Wallet, nil
}

func (a *SIWXAuthenticator) signSession(body string) string {
	h := hmac.New(sha256.New, a.secret)
	h.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Authenticate extracts and verifies whichever SIWX credential the request
// carries. expectedDomain is the request's public host authority. Returns
// the authenticated wallet (lowercase) or an error describing what to send.
func (a *SIWXAuthenticator) Authenticate(r *http.Request, expectedDomain string, now time.Time) (string, error) {
	// Spec transport (x402 sign-in-with-x): a base64-JSON payload in the
	// SIGN-IN-WITH-X header, per docs.x402.org/extensions/sign-in-with-x.
	// Accepted alongside the Authorization forms so stock x402 clients (e.g.
	// the SDK's client extension) interoperate without an obol-specific
	// credential shape. We borrow only the SDK's canonical EIP-4361
	// serialization and run the result through the same domain-binding,
	// freshness, nonce-replay, and EOA-recovery checks as the native form.
	if hdr := r.Header.Get("SIGN-IN-WITH-X"); hdr != "" {
		return a.verifySpecHeader(hdr, expectedDomain, now)
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		if cred, ok := strings.CutPrefix(auth, "SIWX "); ok {
			msgB64, sigB64, found := strings.Cut(strings.TrimSpace(cred), ".")
			if !found {
				return "", fmt.Errorf("siwx: Authorization SIWX credential must be <base64 message>.<base64 signature>")
			}
			msg, err := base64.StdEncoding.DecodeString(msgB64)
			if err != nil {
				return "", fmt.Errorf("siwx: message part is not base64: %w", err)
			}
			sig, err := base64.StdEncoding.DecodeString(sigB64)
			if err != nil {
				return "", fmt.Errorf("siwx: signature part is not base64: %w", err)
			}
			return a.VerifyMessage(string(msg), strings.TrimSpace(string(sig)), expectedDomain, now)
		}
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return a.VerifySession(strings.TrimSpace(token), now)
		}
	}
	if c, err := r.Cookie(SIWXSessionCookie); err == nil && c.Value != "" {
		return a.VerifySession(c.Value, now)
	}
	return "", fmt.Errorf("siwx: no credential — sign in at the offer's /auth page, send the SIGN-IN-WITH-X header, or Authorization: SIWX <b64 message>.<b64 signature>")
}

// verifySpecHeader verifies an x402 sign-in-with-x credential carried in the
// SIGN-IN-WITH-X header (base64-JSON payload). It decodes the payload with the
// x402 SDK, rebuilds the canonical EIP-4361 message the client signed, then
// runs it through VerifyMessage so the domain, freshness, nonce, and EOA
// checks are identical to the native transport. EVM (eip155) EOA only — the
// same limitation as the rest of this file; Solana/EIP-1271 payloads get a
// clear error rather than a silent pass.
func (a *SIWXAuthenticator) verifySpecHeader(header, expectedDomain string, now time.Time) (string, error) {
	payload, err := signinwithx.ParseHeader(header)
	if err != nil {
		return "", fmt.Errorf("siwx: %w", err)
	}
	if !strings.HasPrefix(payload.ChainID, "eip155:") {
		return "", fmt.Errorf("siwx: unsupported chain %q — this server verifies EVM (eip155) EOA signatures only", payload.ChainID)
	}
	message, err := signinwithx.CreateMessage(payload)
	if err != nil {
		return "", fmt.Errorf("siwx: %w", err)
	}
	return a.VerifyMessage(message, payload.Signature, expectedDomain, now)
}

func isHexAddress(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}
