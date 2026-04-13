package buyer

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	x402types "github.com/coinbase/x402/go/types"
)

// Signer produces x402 v2 payment payloads for a specific network and scheme.
// The buyer proxy holds an array of signers and selects the first one that can
// satisfy an incoming 402's requirements.
type Signer interface {
	Network() string
	Scheme() string
	CanSign(req *x402types.PaymentRequirements) bool
	Sign(req *x402types.PaymentRequirements) (*x402types.PaymentPayload, error)
	GetPriority() int
	GetTokens() []TokenConfig
	GetMaxAmount() *big.Int
}

// TokenConfig describes a token a signer can pay with.
type TokenConfig struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	Priority int    `json:"priority"`
}

// PaymentSelector picks a requirement and signs it.
type PaymentSelector interface {
	SelectAndSign(requirements []x402types.PaymentRequirements, signers []Signer) (*x402types.PaymentPayload, error)
}

// DefaultPaymentSelector iterates signers by priority and picks the first match.
type DefaultPaymentSelector struct{}

// NewDefaultPaymentSelector returns a DefaultPaymentSelector.
func NewDefaultPaymentSelector() PaymentSelector {
	return &DefaultPaymentSelector{}
}

// SelectAndSign finds the first signer that can satisfy any requirement and signs it.
func (s *DefaultPaymentSelector) SelectAndSign(requirements []x402types.PaymentRequirements, signers []Signer) (*x402types.PaymentPayload, error) {
	for _, req := range requirements {
		for _, signer := range signers {
			if signer.CanSign(&req) {
				return signer.Sign(&req)
			}
		}
	}

	return nil, ErrNoValidSigner
}

// ErrNoValidSigner is returned when no signer in the pool can satisfy any requirement.
var ErrNoValidSigner = errors.New("no valid signer found for payment requirements")

// PaymentError wraps an error with an x402-specific error code.
type PaymentError struct {
	Code    string
	Message string
	Err     error
}

func (e *PaymentError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *PaymentError) Unwrap() error { return e.Err }

// NewPaymentError creates a PaymentError with the given code and message.
func NewPaymentError(code, msg string, err error) error {
	return &PaymentError{Code: code, Message: msg, Err: err}
}

// Error code constants.
const (
	ErrCodeInvalidRequirements = "invalid_requirements"
	ErrCodeSigningFailed       = "signing_failed"
)

// PaymentEventType identifies the kind of payment event.
type PaymentEventType string

const (
	PaymentEventAttempt PaymentEventType = "attempt"
	PaymentEventSuccess PaymentEventType = "success"
	PaymentEventFailure PaymentEventType = "failure"
)

// PaymentEvent is emitted by the buyer transport for Prometheus instrumentation.
type PaymentEvent struct {
	Type        PaymentEventType
	Timestamp   time.Time
	Duration    time.Duration
	Method      string
	URL         string
	Network     string
	Scheme      string
	Amount      string
	Asset       string
	Recipient   string
	Transaction string
	Payer       string
	Error       error
}

// PaymentCallback receives payment lifecycle events.
type PaymentCallback func(PaymentEvent)
