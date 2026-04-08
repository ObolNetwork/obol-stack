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
		network:   network,
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
func (s *PreSignedSigner) CanSign(req *x402types.PaymentRequirementsV1) bool {
	if req == nil {
		return false
	}

	if !strings.EqualFold(req.Network, s.network) {
		return false
	}

	if !strings.EqualFold(req.PayTo, s.payTo) {
		return false
	}

	if !strings.EqualFold(req.Asset, s.asset) {
		return false
	}

	if req.MaxAmountRequired != "" && req.MaxAmountRequired != s.price {
		return false
	}

	s.mu.Lock()
	remaining := len(s.auths)
	s.mu.Unlock()

	return remaining > 0
}

// Sign pops one pre-signed authorization from the pool and returns it as a
// PaymentPayload. Returns an error when the pool is exhausted.
func (s *PreSignedSigner) Sign(req *x402types.PaymentRequirementsV1) (*x402types.PaymentPayloadV1, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.auths) == 0 {
		return nil, fmt.Errorf("pre-signed auth pool exhausted (spent %d): %w",
			s.spent, ErrNoValidSigner)
	}

	// Pop from the front.
	auth := s.auths[0]
	s.auths = s.auths[1:]

	s.spent++
	if s.onConsume != nil {
		if err := s.onConsume(auth); err != nil {
			s.auths = append([]*PreSignedAuth{auth}, s.auths...)
			s.spent--

			return nil, err
		}
	}

	return &x402types.PaymentPayloadV1{
		X402Version: 1,
		Scheme:      "exact",
		Network:     s.network,
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
	}, nil
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
