package buyer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	x402types "github.com/coinbase/x402/go/types"
)

// defaultFacilitatorURL mirrors x402.DefaultFacilitatorURL. Kept local to
// avoid an import cycle: buyer → x402 → (test) → buyer.
const defaultFacilitatorURL = "https://x402.gcp.obol.tech"

const defaultHTTPTimeout = 30 * time.Second

type facilitatorSettlementRequest struct {
	X402Version         int                           `json:"x402Version"`
	PaymentPayload      json.RawMessage               `json:"paymentPayload"`
	PaymentRequirements x402types.PaymentRequirements `json:"paymentRequirements"`
}

// facilitatorSettle settles a verified payment with the facilitator.
// If facilitatorURL is empty, it falls back to the Obol default facilitator.
func facilitatorSettle(
	ctx context.Context,
	facilitatorURL string,
	payment *x402types.PaymentPayload,
	requirement x402types.PaymentRequirements,
) (SettlementResponse, error) {
	if payment == nil {
		return SettlementResponse{}, fmt.Errorf("payment payload is nil")
	}
	if strings.TrimSpace(facilitatorURL) == "" {
		facilitatorURL = defaultFacilitatorURL
	}

	paymentJSON, err := json.Marshal(payment)
	if err != nil {
		return SettlementResponse{}, fmt.Errorf("marshal payment payload: %w", err)
	}

	body := facilitatorSettlementRequest{
		X402Version:         2,
		PaymentPayload:      json.RawMessage(paymentJSON),
		PaymentRequirements: requirement,
	}

	reqJSON, err := json.Marshal(body)
	if err != nil {
		return SettlementResponse{}, fmt.Errorf("marshal settle request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(facilitatorURL, "/")+"/settle", bytes.NewReader(reqJSON))
	if err != nil {
		return SettlementResponse{}, fmt.Errorf("create settle request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: defaultHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return SettlementResponse{}, fmt.Errorf("facilitator settle: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return SettlementResponse{}, fmt.Errorf("read settle response: %w", err)
	}

	var settlement SettlementResponse
	if err := json.Unmarshal(respBody, &settlement); err != nil {
		return SettlementResponse{}, fmt.Errorf("parse settle response (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if resp.StatusCode != http.StatusOK || !settlement.Success {
		if settlement.ErrorReason == "" {
			settlement.ErrorReason = strings.TrimSpace(string(respBody))
		}
		return SettlementResponse{}, fmt.Errorf("facilitator settle failed (%d): %s", resp.StatusCode, settlement.ErrorReason)
	}

	return settlement, nil
}

