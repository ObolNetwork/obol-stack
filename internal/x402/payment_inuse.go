package x402

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	x402types "github.com/x402-foundation/x402/go/v2/types"
)

const (
	// paymentInUseSettledCap bounds how long a settled authorization stays
	// blocked in the registry. The facilitator rejects a nonce once it is
	// used on-chain, so the registry only needs to cover the window between
	// settlement and the chain state being visible to verify; the
	// authorization's own expiry (validBefore / deadline) is used when it
	// comes sooner.
	paymentInUseSettledCap = 10 * time.Minute

	// paymentInUseMaxEntries caps the registry. Every entry corresponds to a
	// payment that passed facilitator verification, so reaching the cap
	// requires that many distinct signed authorizations in flight or settled
	// within paymentInUseSettledCap.
	paymentInUseMaxEntries = 100_000

	// paymentInUseSweepInterval is how often claim() lazily drops expired
	// settled entries.
	paymentInUseSweepInterval = 30 * time.Second
)

// PaymentInUseRegistry tracks signed payment authorizations that are currently
// being served (verified, not yet finished) or that recently settled, so the
// same EIP-3009 authorization / Permit2 nonce cannot be presented on several
// concurrent requests. Verify alone cannot catch that: the nonce is only
// spent on-chain at settlement, so every concurrent copy verifies fine.
//
// The registry is per-process, in memory. The x402-verifier runs a single
// replica (internal/embed/infrastructure/base/templates/x402.yaml); scaling
// it out requires moving this registry to a shared store, otherwise each
// replica only sees its own claims.
//
// The zero value is not usable; construct with NewPaymentInUseRegistry.
type PaymentInUseRegistry struct {
	mu         sync.Mutex
	entries    map[string]paymentInUseEntry
	maxEntries int
	settledCap time.Duration
	lastSweep  time.Time
	now        func() time.Time
}

type paymentInUseEntry struct {
	inFlight bool
	expires  time.Time // only meaningful when !inFlight
}

// NewPaymentInUseRegistry returns an empty registry with the default bounds.
func NewPaymentInUseRegistry() *PaymentInUseRegistry {
	return &PaymentInUseRegistry{
		entries:    make(map[string]paymentInUseEntry),
		maxEntries: paymentInUseMaxEntries,
		settledCap: paymentInUseSettledCap,
		now:        time.Now,
	}
}

// claimResult is the outcome of PaymentInUseRegistry.claim.
type claimResult int

const (
	claimOK claimResult = iota
	claimInUse
	claimFull
)

// claim marks key as in flight. It returns claimInUse when the key is already
// in flight or settled and not yet expired, and claimFull when the registry
// is at capacity even after dropping expired entries.
func (p *PaymentInUseRegistry) claim(key string) claimResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if e, ok := p.entries[key]; ok {
		if e.inFlight || now.Before(e.expires) {
			return claimInUse
		}
		delete(p.entries, key)
	}
	if now.Sub(p.lastSweep) >= paymentInUseSweepInterval || len(p.entries) >= p.maxEntries {
		p.sweepLocked(now)
	}
	if len(p.entries) >= p.maxEntries {
		return claimFull
	}
	p.entries[key] = paymentInUseEntry{inFlight: true}
	return claimOK
}

// release ends the in-flight claim on key. An unsettled request frees the key
// immediately so the buyer can retry with the same authorization. A settled
// one keeps the key blocked until the authorization expires (deadline, when
// known) or settledCap elapses, whichever is sooner.
func (p *PaymentInUseRegistry) release(key string, settled bool, deadline time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !settled {
		delete(p.entries, key)
		return
	}
	now := p.now()
	expires := now.Add(p.settledCap)
	if !deadline.IsZero() && deadline.Before(expires) {
		expires = deadline
	}
	if !now.Before(expires) {
		delete(p.entries, key)
		return
	}
	p.entries[key] = paymentInUseEntry{expires: expires}
}

func (p *PaymentInUseRegistry) sweepLocked(now time.Time) {
	p.lastSweep = now
	for k, e := range p.entries {
		if !e.inFlight && !now.Before(e.expires) {
			delete(p.entries, k)
		}
	}
}

// len reports the number of tracked keys (tests).
func (p *PaymentInUseRegistry) len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// paymentInUseKey derives the registry key for a payment: the authorization's
// replay-protection identity on-chain. EIP-3009 nonces are scoped per token
// contract and signer; Permit2 nonces are scoped per signer across all tokens.
// When neither shape is recognisable, the key falls back to a hash of the raw
// payment header, which still dedups byte-identical replays.
func paymentInUseKey(payload x402types.PaymentPayload, rawHeader string) string {
	network := strings.ToLower(strings.TrimSpace(payload.Accepted.Network))
	if chain, err := ResolveChainInfo(payload.Accepted.Network); err == nil && chain.CAIP2Network != "" {
		network = strings.ToLower(chain.CAIP2Network)
	}
	scheme := strings.ToLower(strings.TrimSpace(payload.Accepted.Scheme))

	if auth, ok := payload.Payload["authorization"].(map[string]any); ok {
		from := normalizeAddressField(auth["from"])
		nonce := normalizeNonceField(auth["nonce"])
		if from != "" && nonce != "" {
			asset := strings.ToLower(strings.TrimSpace(payload.Accepted.Asset))
			return fmt.Sprintf("eip3009|%s|%s|%s|%s|%s", scheme, network, asset, from, nonce)
		}
	}
	if auth, ok := payload.Payload["permit2Authorization"].(map[string]any); ok {
		from := normalizeAddressField(auth["from"])
		nonce := normalizeNonceField(auth["nonce"])
		if from != "" && nonce != "" {
			return fmt.Sprintf("permit2|%s|%s|%s|%s", scheme, network, from, nonce)
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawHeader)))
	return "raw|" + hex.EncodeToString(sum[:])
}

// paymentDeadline returns the authorization's expiry (EIP-3009 validBefore or
// Permit2 deadline), or the zero time when the payload carries none.
func paymentDeadline(payload x402types.PaymentPayload) time.Time {
	var raw any
	if auth, ok := payload.Payload["authorization"].(map[string]any); ok {
		raw = auth["validBefore"]
	} else if auth, ok := payload.Payload["permit2Authorization"].(map[string]any); ok {
		raw = auth["deadline"]
	}
	secs, ok := unixSecondsField(raw)
	if !ok || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

func normalizeAddressField(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeNonceField renders a nonce as a canonical lowercase string.
// EIP-3009 nonces are bytes32 hex; Permit2 nonces are uint256, usually a
// decimal string but occasionally a JSON number.
func normalizeNonceField(v any) string {
	switch t := v.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(t))
	case json.Number:
		return t.String()
	case float64:
		if t >= 0 && t == math.Trunc(t) && t < 1e18 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return ""
	}
}

func unixSecondsField(v any) (int64, bool) {
	switch t := v.(type) {
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case float64:
		if t > math.MaxInt64 || t < math.MinInt64 {
			return 0, false
		}
		return int64(t), true
	default:
		return 0, false
	}
}
