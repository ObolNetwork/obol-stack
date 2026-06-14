// Package escrow defines the conditional-settlement seam between the
// servicebounty-controller and the x402 facilitator: hold a reward
// authorization now, release it to the fulfiller on an accepted verdict, or
// return it to the poster on expiry/rejection.
//
// The controller is only a bounded release TRIGGER, never a signer: the
// poster's agent pre-signs the upto authorization (payTo is signed into it, so
// whoever triggers settlement can only release the signed transfer to the
// signed recipient — or nothing). The facilitator holds the auth and performs
// settlement; the controller authenticates to it with a bearer token. This
// preserves the "controller holds no keys" invariant exactly as today.
//
// Two implementations:
//   - HTTPGateway: POSTs to the facilitator's /escrow/{reserve,capture,void}
//     routes (the ConditionalSettleFacilitator wrapper around x402-rs — the
//     next slice on the facilitator side).
//   - LedgerGateway: in-memory dev mode for local-first stacks and tests. It
//     is escrow THEATER — nothing is held anywhere — and every receipt is
//     labeled dev-ledger so it can never be mistaken for settlement.
package escrow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// States reported by a Gateway. They feed ServiceBountyStatus.EscrowState.
const (
	StateReserved = "Reserved"
	StateCaptured = "Captured"
	StateVoided   = "Voided"
)

// Permit2Voucher is a Uniswap Permit2 SignatureTransfer
// PermitBatchTransferFrom authorization signed by Owner, executable only by
// Spender (the escrow facilitator), with the recipients declared at signing
// time. The voucher binds owner, token, spender, nonce, deadline, and every
// recipient seat into one EIP-712 signature, so whoever holds it can only
// move the signed amounts to the signed recipients — or nothing.
type Permit2Voucher struct {
	// Owner is the signer whose funds the voucher moves.
	Owner string `json:"owner"`
	// Token is the ERC-20 token contract address.
	Token string `json:"token"`
	// Network is the chain alias the voucher settles on (e.g. base-sepolia).
	Network string `json:"network"`
	// Spender is the only address allowed to execute the transfer — the
	// escrow facilitator.
	Spender string `json:"spender"`
	// Nonce is the Permit2 unordered nonce as a uint256 decimal string.
	Nonce string `json:"nonce"`
	// Deadline is the unix timestamp the voucher expires at.
	Deadline int64 `json:"deadline"`
	// Recipients are the payout seats, amounts in atomic token units — one
	// permitted entry per recipient seat.
	Recipients []BatchRecipient `json:"recipients"`
	// Signature is the 0x-hex 65-byte EIP-712 signature over the permit.
	Signature string `json:"signature"`
}

// ReserveRequest identifies the reward authorization the facilitator should
// verify and hold for a bounty.
type ReserveRequest struct {
	// ID is the stable escrow key — the ServiceBounty UID.
	ID string `json:"id"`
	// Network, PayTo, Asset, Amount describe the reward leg (PayTo is the
	// poster's refund address; the fulfiller payout address is bound in the
	// pre-signed auth itself at claim time).
	Network string `json:"network"`
	PayTo   string `json:"payTo"`
	Asset   string `json:"asset"`
	Amount  string `json:"amount"`
	// Scheme is the x402 settlement scheme (upto today, authCapture later).
	Scheme string `json:"scheme"`
	// Voucher is the optional Permit2 batch-transfer authorization backing
	// the reservation (real escrow). Gateways that hold nothing (the dev
	// ledger) may ignore it.
	Voucher *Permit2Voucher `json:"voucher,omitempty"`
}

// Receipt is the gateway's record of an escrow operation.
type Receipt struct {
	State  string `json:"state"`
	TxHash string `json:"txHash,omitempty"`
	// Spender is the facilitator address vouchers must name as the only
	// executor; surfaced so signers can bind it before reserving.
	Spender string `json:"spender,omitempty"`
}

// Gateway is the Hold/Release/Refund seam. Implementations must be safe for
// concurrent use by reconcile workers.
type Gateway interface {
	// Reserve verifies + holds the reward auth for id. Idempotent.
	Reserve(ctx context.Context, req ReserveRequest) (Receipt, error)
	// Capture settles the held auth to the fulfiller. Idempotent: capturing
	// an already-captured id returns the original receipt.
	Capture(ctx context.Context, id string) (Receipt, error)
	// Void drops the held auth (poster keeps funds). Voiding an unknown or
	// already-voided id is not an error — refund flows must be re-runnable.
	Void(ctx context.Context, id string) (Receipt, error)
}

// BatchRecipient is one payee of a split capture (the eval-payment leg: each
// revealed counting evaluator gets perEvaluator from the held budget).
type BatchRecipient struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

// BatchGateway is the x402 batch-settlement seam: one held authorization
// captured to k recipients in one settlement (the eval budget → evaluators).
// Optional — callers type-assert and fall back to plain Capture.
type BatchGateway interface {
	// CaptureBatch settles the held auth for id split across recipients.
	CaptureBatch(ctx context.Context, id string, recipients []BatchRecipient) (Receipt, error)
}

// ── HTTPGateway ─────────────────────────────────────────────────────────────

// HTTPGateway drives the facilitator's escrow routes.
type HTTPGateway struct {
	// Base is the facilitator URL, e.g. https://x402.gcp.obol.tech.
	Base string
	// Token authenticates capture/void (the release-authority credential).
	Token  string
	Client *http.Client
}

func (g *HTTPGateway) Reserve(ctx context.Context, req ReserveRequest) (Receipt, error) {
	return g.post(ctx, "reserve", req.ID, req)
}

func (g *HTTPGateway) Capture(ctx context.Context, id string) (Receipt, error) {
	return g.post(ctx, "capture", id, nil)
}

func (g *HTTPGateway) Void(ctx context.Context, id string) (Receipt, error) {
	return g.post(ctx, "void", id, nil)
}

// CaptureBatch drives the facilitator's batch-settlement capture: the held
// auth for id is settled to all recipients in one transaction.
func (g *HTTPGateway) CaptureBatch(ctx context.Context, id string, recipients []BatchRecipient) (Receipt, error) {
	return g.post(ctx, "capture", id, map[string]any{"recipients": recipients})
}

func (g *HTTPGateway) post(ctx context.Context, op, id string, body any) (Receipt, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return Receipt{}, err
		}
		payload = bytes.NewReader(raw)
	}

	url := strings.TrimRight(g.Base, "/") + "/escrow/" + op + "/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return Receipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Receipt{}, fmt.Errorf("escrow %s %s: %w", op, id, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Receipt{}, fmt.Errorf("escrow %s %s: facilitator returned %d: %s", op, id, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var receipt Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("escrow %s %s: decode receipt: %w", op, id, err)
	}
	return receipt, nil
}

// ── LedgerGateway (dev) ─────────────────────────────────────────────────────

// LedgerGateway records escrow state in memory. Local-first dev mode only —
// no funds are verified or held anywhere. Receipts carry a dev-ledger TxHash
// so downstream surfaces can never present them as settlement.
type LedgerGateway struct {
	mu     sync.Mutex
	states map[string]Receipt
}

func NewLedgerGateway() *LedgerGateway {
	return &LedgerGateway{states: make(map[string]Receipt)}
}

func (g *LedgerGateway) Reserve(_ context.Context, req ReserveRequest) (Receipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.states[req.ID]; ok {
		return r, nil
	}
	r := Receipt{State: StateReserved, TxHash: "dev-ledger:" + req.ID}
	g.states[req.ID] = r
	return r, nil
}

func (g *LedgerGateway) Capture(_ context.Context, id string) (Receipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.states[id]
	if !ok {
		return Receipt{}, fmt.Errorf("escrow capture %s: nothing reserved", id)
	}
	if r.State == StateVoided {
		return Receipt{}, fmt.Errorf("escrow capture %s: already voided", id)
	}
	r.State = StateCaptured
	g.states[id] = r
	return r, nil
}

// CaptureBatch marks the held budget captured with a dev-ledger receipt
// naming the recipient count — escrow theater, honestly labeled, like the
// rest of the ledger.
func (g *LedgerGateway) CaptureBatch(ctx context.Context, id string, recipients []BatchRecipient) (Receipt, error) {
	r, err := g.Capture(ctx, id)
	if err != nil {
		return Receipt{}, err
	}
	r.TxHash = fmt.Sprintf("dev-ledger:%s:batch[%d]", id, len(recipients))
	g.mu.Lock()
	g.states[id] = r
	g.mu.Unlock()
	return r, nil
}

func (g *LedgerGateway) Void(_ context.Context, id string) (Receipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.states[id]
	if !ok {
		// Re-runnable refunds: voiding the unknown is a no-op success.
		return Receipt{State: StateVoided, TxHash: "dev-ledger:" + id}, nil
	}
	if r.State == StateCaptured {
		return Receipt{}, fmt.Errorf("escrow void %s: already captured", id)
	}
	r.State = StateVoided
	g.states[id] = r
	return r, nil
}
