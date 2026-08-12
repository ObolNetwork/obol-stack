package x402

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	x402types "github.com/x402-foundation/x402/go/v2/types"
)

const (
	defaultCaptureDeadlineSecs = 900
	defaultRefundDeadlineSecs  = 1800
)

// AuthCaptureUnlockConfig configures the single offer whose SIWX session is
// minted only after an auth-capture payment verifies and settles.
type AuthCaptureUnlockConfig struct {
	Enabled             bool   `yaml:"enabled"`
	OfferPrefix         string `yaml:"offerPrefix"`
	Price               string `yaml:"price"`
	Network             string `yaml:"network"`
	PayTo               string `yaml:"payTo"`
	Asset               string `yaml:"asset"`
	FeeRecipient        string `yaml:"feeRecipient"`
	MinFeeBps           uint16 `yaml:"minFeeBps"`
	MaxFeeBps           uint16 `yaml:"maxFeeBps"`
	CaptureAuthorizer   string `yaml:"captureAuthorizer"`
	CaptureDeadlineSecs uint64 `yaml:"captureDeadlineSecs"`
	RefundDeadlineSecs  uint64 `yaml:"refundDeadlineSecs"`
}

// applyDeadlineDefaults fills unset capture/refund windows. Split out of
// Validate so callers that need the effective deadlines without a priced
// requirement (the per-request fee bounds a client-echoed captureDeadline
// against them) get the same numbers Validate would have applied.
func (c *AuthCaptureUnlockConfig) applyDeadlineDefaults() {
	if c.CaptureDeadlineSecs == 0 {
		c.CaptureDeadlineSecs = defaultCaptureDeadlineSecs
	}
	if c.RefundDeadlineSecs == 0 {
		c.RefundDeadlineSecs = defaultRefundDeadlineSecs
	}
}

// Validate applies deadline defaults and rejects unusable auth-capture
// configuration before a payment is sent to the facilitator.
func (c *AuthCaptureUnlockConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("authCaptureUnlock config is nil")
	}
	c.applyDeadlineDefaults()

	if c.Enabled {
		// offerPrefix is deliberately NOT required: it selects the standalone
		// paid-unlock offer, and the same config block now also drives the
		// per-request platform fee, which selects by offer type instead. Empty
		// means "no unlock offer, fee only" (see isUnlockOffer).
		if c.FeeRecipient == "" {
			return fmt.Errorf("feeRecipient must be non-empty when enabled")
		}
		if c.CaptureAuthorizer == "" {
			return fmt.Errorf("captureAuthorizer must be non-empty when enabled")
		}
	}
	if c.MinFeeBps > c.MaxFeeBps {
		return fmt.Errorf("minFeeBps %d exceeds maxFeeBps %d", c.MinFeeBps, c.MaxFeeBps)
	}
	if c.MaxFeeBps > 10000 {
		return fmt.Errorf("maxFeeBps %d exceeds 10000", c.MaxFeeBps)
	}
	if c.MaxFeeBps > 0 && !validNonZeroAddress(c.FeeRecipient) {
		return fmt.Errorf("feeRecipient must be a valid non-zero 0x address when maxFeeBps is positive")
	}
	if !validNonZeroAddress(c.CaptureAuthorizer) {
		return fmt.Errorf("captureAuthorizer must be a valid non-zero 0x address")
	}
	if c.CaptureDeadlineSecs <= 6 {
		return fmt.Errorf("captureDeadlineSecs must be greater than 6, got %d", c.CaptureDeadlineSecs)
	}
	if c.RefundDeadlineSecs < c.CaptureDeadlineSecs {
		return fmt.Errorf("refundDeadlineSecs %d is less than captureDeadlineSecs %d", c.RefundDeadlineSecs, c.CaptureDeadlineSecs)
	}

	price, _, err := new(big.Float).SetPrec(128).Parse(c.Price, 10)
	if err != nil {
		return fmt.Errorf("price %q is not a valid decimal: %w", c.Price, err)
	}
	if price == nil || price.Sign() <= 0 {
		return fmt.Errorf("price must be a positive decimal, got %q", c.Price)
	}

	return nil
}

func validNonZeroAddress(address string) bool {
	return common.IsHexAddress(address) && common.HexToAddress(address) != (common.Address{})
}

// BuildAuthCaptureRequirement builds the strict auth-capture wire shape
// expected by the facilitator.
func BuildAuthCaptureRequirement(chain ChainInfo, asset AssetInfo, c *AuthCaptureUnlockConfig, payTo string, now time.Time) (x402types.PaymentRequirements, error) {
	if err := c.Validate(); err != nil {
		return x402types.PaymentRequirements{}, fmt.Errorf("validate auth-capture config: %w", err)
	}
	atomicAmount, err := decimalToAtomic(c.Price, asset.Decimals)
	if err != nil {
		return x402types.PaymentRequirements{}, fmt.Errorf("invalid price %q: %w", c.Price, err)
	}

	return x402types.PaymentRequirements{
		Scheme:            "auth-capture",
		Network:           chain.CAIP2Network,
		Asset:             asset.Address,
		Amount:            atomicAmount,
		PayTo:             payTo,
		MaxTimeoutSeconds: int(ClampMaxTimeoutSeconds(int64(c.CaptureDeadlineSecs))),
		Extra: map[string]interface{}{
			"name":                asset.EIP712Name,
			"version":             asset.EIP712Version,
			"captureAuthorizer":   c.CaptureAuthorizer,
			"captureDeadline":     now.Unix() + int64(c.CaptureDeadlineSecs),
			"refundDeadline":      now.Unix() + int64(c.RefundDeadlineSecs),
			"feeRecipient":        c.FeeRecipient,
			"minFeeBps":           c.MinFeeBps,
			"maxFeeBps":           c.MaxFeeBps,
			"autoCapture":         true,
			"assetTransferMethod": asset.TransferMethod,
		},
	}, nil
}
