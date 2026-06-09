package buyer

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	x402types "github.com/x402-foundation/x402/go/types"
)

func TestEncodePayment_RoundTrip(t *testing.T) {
	payload := x402types.PaymentPayload{
		X402Version: 2,
		Accepted: x402types.PaymentRequirements{
			Scheme:  "exact",
			Network: "eip155:84532",
			Amount:  "1000",
			Asset:   "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			PayTo:   "0xTo",
		},
		Payload: map[string]interface{}{
			"signature": "0xSig",
			"authorization": map[string]interface{}{
				"from": "0xFrom", "to": "0xTo", "value": "1000",
				"validAfter": "0", "validBefore": "9999999999", "nonce": "0xNonce",
			},
		},
	}

	encoded, err := EncodePayment(payload)
	if err != nil {
		t.Fatalf("EncodePayment: %v", err)
	}

	// Decode and verify round-trip.
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}

	var result x402types.PaymentPayload
	if err := json.Unmarshal(decoded, &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	if result.X402Version != 2 {
		t.Errorf("X402Version = %d, want 2", result.X402Version)
	}
	if result.Accepted.Scheme != "exact" {
		t.Errorf("Scheme = %q, want %q", result.Accepted.Scheme, "exact")
	}
	if result.Accepted.Network != "eip155:84532" {
		t.Errorf("Network = %q, want %q", result.Accepted.Network, "eip155:84532")
	}
}

func TestDecodeSettlement_RoundTrip(t *testing.T) {
	original := SettlementResponse{
		Success:     true,
		Transaction: "0xTxHash",
		Network:     "base-sepolia",
		Payer:       "0xPayer",
	}

	jsonBytes, _ := json.Marshal(original)
	encoded := base64.StdEncoding.EncodeToString(jsonBytes)

	result, err := DecodeSettlement(encoded)
	if err != nil {
		t.Fatalf("DecodeSettlement: %v", err)
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.Transaction != "0xTxHash" {
		t.Errorf("Transaction = %q, want %q", result.Transaction, "0xTxHash")
	}
	if result.Payer != "0xPayer" {
		t.Errorf("Payer = %q, want %q", result.Payer, "0xPayer")
	}
}

func TestDecodeSettlement_InvalidBase64(t *testing.T) {
	_, err := DecodeSettlement("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}
