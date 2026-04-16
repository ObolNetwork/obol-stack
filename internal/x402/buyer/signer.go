package buyer

import (
	"fmt"
	"math/big"
	"strings"
	"sync"

	x402types "github.com/coinbase/x402/go/types"
)

// PreSignedSigner implements Signer using pre-signed ERC-3009
// TransferWithAuthorization vouchers. It pops one auth from the pool per
// Sign() call. The pool is finite — once exhausted, CanSign returns false.
//
// Thread-safe via sync.Mutex.
type PreSignedSigner struct {
	network string
	payTo   string
	asset   string
	price   string

	onConsume func(*PreSignedAuth) error

	mu    sync.Mutex
	auths []*PreSignedAuth
	spent int
}

// NewPreSignedSigner creates a signer backed by a pool of pre-signed auths.
func NewPreSignedSigner(network, payTo, asset, price string, auths []*PreSignedAuth, spent int, onConsume func(*PreSignedAuth) error) *PreSignedSigner {
	pool := make([]*PreSignedAuth, len(auths))
	copy(pool, auths)

	return &PreSignedSigner{
		network:   normalizeNetworkID(network),
		payTo:     payTo,
		asset:     asset,
		price:     price,
		onConsume: onConsume,
		auths:     pool,
		spent:     spent,
	}
}

// Network returns the blockchain network this signer operates on.
func (s *PreSignedSigner) Network() string { return s.network }

// Scheme returns "exact" — the only payment scheme for EVM x402.
func (s *PreSignedSigner) Scheme() string { return "exact" }

// CanSign checks if this signer can satisfy the given payment requirement.
// Returns true if network, payTo, asset, and amount match and there are
// remaining auths in the pool.
func (s *PreSignedSigner) CanSign(req *x402types.PaymentRequirements) bool {
	if req == nil {
		return false
	}

	if !strings.EqualFold(normalizeNetworkID(req.Network), s.network) {
		return false
	}

	if !strings.EqualFold(req.PayTo, s.payTo) {
		return false
	}

	if !strings.EqualFold(req.Asset, s.asset) {
		return false
	}

	amount := req.Amount
	if amount == "" {
		if req.Extra != nil {
			if legacy, ok := req.Extra["maxAmountRequired"].(string); ok && legacy != "" {
				amount = legacy
			}
		}
	}
	if amount != "" && amount != s.price {
		return false
	}

	s.mu.Lock()
	remaining := len(s.auths)
	s.mu.Unlock()

	return remaining > 0
}

// Sign pops one pre-signed authorization from the pool and returns it as a
// PaymentPayload, then persists local consume only after ConfirmSpend succeeds.
// Returns an error when the pool is exhausted.
func (s *PreSignedSigner) Sign(req *x402types.PaymentRequirements) (*x402types.PaymentPayload, error) {
	payload, auth, err := s.HoldSign(req)
	if err != nil {
		return nil, err
	}
	if err := s.ConfirmSpend(auth); err != nil {
		s.ReleaseSpend(auth)
		return nil, err
	}
	return payload, nil
}

// HoldSign removes one auth from the pool and builds a payment payload without
// persisting consume (no onConsume). The caller must invoke exactly one of
// ConfirmSpend or ReleaseSpend with the returned auth.
func (s *PreSignedSigner) HoldSign(req *x402types.PaymentRequirements) (*x402types.PaymentPayload, *PreSignedAuth, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("payment requirements are nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.auths) == 0 {
		return nil, nil, fmt.Errorf("pre-signed auth pool exhausted (spent %d): %w",
			s.spent, ErrNoValidSigner)
	}

	auth := s.auths[0]
	s.auths = s.auths[1:]
	s.spent++

	accepted := *req
	if accepted.Scheme == "" {
		accepted.Scheme = "exact"
	}
	if accepted.Network == "" {
		accepted.Network = s.network
	} else {
		accepted.Network = normalizeNetworkID(accepted.Network)
	}
	if accepted.Amount == "" && s.price != "" {
		accepted.Amount = s.price
	}

	payload := &x402types.PaymentPayload{
		X402Version: 2,
		Accepted:    accepted,
		Payload: map[string]interface{}{
			"signature": auth.Signature,
			"authorization": map[string]interface{}{
				"from":        auth.From,
				"to":          auth.To,
				"value":       auth.Value,
				"validAfter":  auth.ValidAfter,
				"validBefore": auth.ValidBefore,
				"nonce":       auth.Nonce,
			},
		},
	}
	return payload, auth, nil
}

// ConfirmSpend persists a nonce as consumed after a successful paid upstream
// response. The auth must be the pointer returned from HoldSign for this hold.
func (s *PreSignedSigner) ConfirmSpend(auth *PreSignedAuth) error {
	if auth == nil {
		return nil
	}
	if s.onConsume == nil {
		return nil
	}
	return s.onConsume(auth)
}

// ReleaseSpend returns a held auth to the pool after a failed payment attempt
// (network error or upstream HTTP error). It reverses HoldSign's in-memory
// reservation so the voucher can be retried.
func (s *PreSignedSigner) ReleaseSpend(auth *PreSignedAuth) {
	if auth == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.auths = append([]*PreSignedAuth{auth}, s.auths...)
	if s.spent > 0 {
		s.spent--
	}
}

// GetPriority returns 0 (highest priority).
func (s *PreSignedSigner) GetPriority() int { return 0 }

// GetTokens returns the single USDC token this signer handles.
func (s *PreSignedSigner) GetTokens() []TokenConfig {
	return []TokenConfig{
		{Address: s.asset, Symbol: "USDC", Decimals: 6, Priority: 0},
	}
}

// GetMaxAmount returns nil (no per-call limit — bounded by pool size instead).
func (s *PreSignedSigner) GetMaxAmount() *big.Int { return nil }

// Remaining returns the number of pre-signed authorizations left in the pool.
func (s *PreSignedSigner) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.auths)
}

// Spent returns the number of authorizations consumed so far.
func (s *PreSignedSigner) Spent() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.spent
}

// normalizeNetworkID maps human-friendly chain names to CAIP-2 identifiers.
// Mirrors x402.NormalizeNetworkID — kept local to avoid an import cycle
// (x402 test files import buyer).
func normalizeNetworkID(network string) string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "base", "base-mainnet":
		return "eip155:8453"
	case "base-sepolia":
		return "eip155:84532"
	case "ethereum", "ethereum-mainnet", "mainnet":
		return "eip155:1"
	case "sepolia":
		return "eip155:11155111"
	case "polygon", "polygon-mainnet":
		return "eip155:137"
	case "polygon-amoy":
		return "eip155:80002"
	case "avalanche", "avalanche-mainnet":
		return "eip155:43114"
	case "avalanche-fuji":
		return "eip155:43113"
	case "arbitrum", "arbitrum-one":
		return "eip155:42161"
	case "arbitrum-sepolia":
		return "eip155:421614"
	default:
		return network
	}
}
