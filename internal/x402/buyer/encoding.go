package buyer

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	x402types "github.com/x402-foundation/x402/go/types"
)

// EncodePayment converts a v2 PaymentPayload to a base64-encoded JSON string
// for the X-PAYMENT HTTP header.
func EncodePayment(payment x402types.PaymentPayload) (string, error) {
	paymentJSON, err := json.Marshal(payment)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payment: %w", err)
	}
	return base64.StdEncoding.EncodeToString(paymentJSON), nil
}

// SettlementResponse is the decoded X-PAYMENT-RESPONSE header.
type SettlementResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason,omitempty"`
	Transaction string `json:"transaction,omitempty"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
}

// DecodeSettlement decodes a base64-encoded X-PAYMENT-RESPONSE header.
func DecodeSettlement(encoded string) (SettlementResponse, error) {
	var settlement SettlementResponse

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return settlement, fmt.Errorf("failed to decode base64: %w", err)
	}

	if err := json.Unmarshal(decoded, &settlement); err != nil {
		return settlement, fmt.Errorf("failed to unmarshal settlement: %w", err)
	}

	return settlement, nil
}
