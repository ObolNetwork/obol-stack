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

// AuthCaptureUnlockConfig configures the paid sign-in applied to AGENT offers:
// their SIWX session is minted only after an auth-capture payment verifies and
// settles. Which offers it covers is decided by type (see isUnlockOffer), not
// by config — this struct only carries how the payment is priced and split.
//
// Price, Network and PayTo are OVERRIDES. Left empty they fall back to the
// agent offer's own values, so a stack with several agents needs no per-agent
// configuration and each seller is paid to their own address.
type AuthCaptureUnlockConfig struct {
	Enabled             bool   `yaml:"enabled"`
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

// Validate applies deadline defaults and rejects unusable auth-capture
// configuration before a payment is sent to the facilitator.
func (c *AuthCaptureUnlockConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("authCaptureUnlock config is nil")
	}
	if c.CaptureDeadlineSecs == 0 {
		c.CaptureDeadlineSecs = defaultCaptureDeadlineSecs
	}
	if c.RefundDeadlineSecs == 0 {
		c.RefundDeadlineSecs = defaultRefundDeadlineSecs
	}

	if c.Enabled {
		if c.FeeRecipient == "" {
			return fmt.Errorf("feeRecipient must be non-empty when enabled")
		}
		if c.CaptureAuthorizer == "" {
			return fmt.Errorf("captureAuthorizer must be non-empty when enabled")
		}
		// Price is resolved per-offer from the agent's own price, so it is
		// deliberately NOT required here — but if it never resolves, the
		// requirement builder would advertise an empty amount. handlePaidUnlock
		// fills it from the rule before calling Validate's caller, so an empty
		// price at this point means neither config nor offer priced it.
		if c.Price == "" {
			return fmt.Errorf("price is empty and the offer does not declare one")
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
