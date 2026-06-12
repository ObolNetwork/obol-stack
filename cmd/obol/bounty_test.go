package main

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Command structure (house style: sell_test.go)
// ─────────────────────────────────────────────────────────────────────────────

func testBountyCommand(t *testing.T) *cli.Command {
	t.Helper()
	return bountyCommand(&config.Config{})
}

func TestBountyFundCommand_Flags(t *testing.T) {
	fund := findSubcommand(t, testBountyCommand(t), "fund")
	flags := flagMap(fund)

	requireFlags(t, flags, "namespace", "key", "signer-url", "spender", "deadline-hours")
	assertStringDefault(t, flags, "namespace", "hermes-obol-agent")
	assertIntDefault(t, flags, "deadline-hours", 72)
}

func TestBountyClaimCommand_BondVoucherFlags(t *testing.T) {
	claim := findSubcommand(t, testBountyCommand(t), "claim")
	flags := flagMap(claim)

	requireFlags(t, flags, "address", "bond-key", "bond-signer-url", "bond-recipient", "spender", "deadline-hours")
	assertFlagRequired(t, flags, "address")
	assertIntDefault(t, flags, "deadline-hours", 72)
}

func TestBountyEvalFundCommand_Flags(t *testing.T) {
	eval := findSubcommand(t, testBountyCommand(t), "eval")
	fund := findSubcommand(t, eval, "fund")
	flags := flagMap(fund)

	requireFlags(t, flags, "namespace", "key", "signer-url", "spender", "deadline-hours")
	assertStringDefault(t, flags, "namespace", "hermes-obol-agent")
	assertIntDefault(t, flags, "deadline-hours", 72)
}

func TestBountyEvalCalldata_DerivationFlags(t *testing.T) {
	eval := findSubcommand(t, testBountyCommand(t), "eval")
	calldata := findSubcommand(t, eval, "calldata")
	flags := flagMap(calldata)

	requireFlags(t, flags, "bounty", "address", "request-hash", "response", "network", "namespace")
	assertFlagRequired(t, flags, "response")

	// --request-hash became an explicit OVERRIDE: it must no longer be
	// required, since --bounty + --address derive it from the bounty UID.
	if f, ok := flags["request-hash"].(*cli.StringFlag); !ok || f.Required {
		t.Errorf("--request-hash must be an optional override (derive via --bounty/--address), got required=%v", ok && f.Required)
	}
}

func TestBountyFeedbackCommand_Flags(t *testing.T) {
	feedback := findSubcommand(t, testBountyCommand(t), "feedback")
	flags := flagMap(feedback)

	requireFlags(t, flags, "namespace", "agent-id", "feedback-uri")
	assertStringDefault(t, flags, "namespace", "hermes-obol-agent")

	// --agent-id is an Int64Flag (ERC-8004 tokenIds exceed int32), which the
	// shared assertFlagRequired helper doesn't cover — assert inline.
	f, ok := flags["agent-id"].(*cli.Int64Flag)
	if !ok {
		t.Fatalf("flag --agent-id is %T, want *cli.Int64Flag", flags["agent-id"])
	}
	if !f.Required {
		t.Error("flag --agent-id should be required")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Deterministic voucher nonce
// ─────────────────────────────────────────────────────────────────────────────

func TestBountyVoucherNonce_Deterministic(t *testing.T) {
	uid := "f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

	// Re-running a fund command must re-derive the SAME nonce, so re-funding
	// is idempotent and a consumed Permit2 nonce can never be double-captured.
	if a, b := bountyVoucherNonce(uid, "reward"), bountyVoucherNonce(uid, "reward"); a != b {
		t.Errorf("nonce not deterministic: %s != %s", a, b)
	}

	// Cross-check the exact derivation: uint256 of keccak256("<uid>|reward").
	want := new(big.Int).SetBytes(ethcrypto.Keccak256([]byte(uid + "|reward"))).String()
	if got := bountyVoucherNonce(uid, "reward"); got != want {
		t.Errorf("nonce derivation drifted: got %s, want %s", got, want)
	}

	// Every leg gets its own nonce — reward, bond, eval, and eval-r1 vouchers
	// for the same bounty must never collide.
	seen := map[string]string{}
	for _, leg := range []string{"reward", "bond", "eval", "eval-r1"} {
		nonce := bountyVoucherNonce(uid, leg)
		if prev, dup := seen[nonce]; dup {
			t.Errorf("legs %s and %s derived the same nonce %s", prev, leg, nonce)
		}
		seen[nonce] = leg

		// Permit2 nonces are uint256 decimal strings.
		v, ok := new(big.Int).SetString(nonce, 10)
		if !ok || v.Sign() < 0 || v.BitLen() > 256 {
			t.Errorf("leg %s nonce %q is not a decimal uint256", leg, nonce)
		}
	}

	// Distinct bounties must derive distinct nonces for the same leg.
	if bountyVoucherNonce(uid, "reward") == bountyVoucherNonce("other-uid", "reward") {
		t.Error("distinct bounty UIDs derived the same reward nonce")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Human → atomic amount conversion
// ─────────────────────────────────────────────────────────────────────────────

func TestHumanToAtomic(t *testing.T) {
	cases := []struct {
		amount   string
		decimals int
		want     string
		wantErr  bool
	}{
		{"500.00", 6, "500000000", false},
		{"0.5", 18, "500000000000000000", false},
		{"1", 6, "1000000", false},
		{"0.000001", 6, "1", false},
		{"1.230000", 2, "123", false}, // trailing zeros beyond precision OK
		{".5", 6, "500000", false},
		{"0.0000001", 6, "", true}, // sub-atomic remainder
		{"0", 6, "", true},         // must be positive
		{"-1", 6, "", true},
		{"abc", 6, "", true},
		{"", 6, "", true},
	}
	for _, tc := range cases {
		got, err := humanToAtomic(tc.amount, tc.decimals)
		if tc.wantErr {
			if err == nil {
				t.Errorf("humanToAtomic(%q, %d) = %q, want error", tc.amount, tc.decimals, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("humanToAtomic(%q, %d): %v", tc.amount, tc.decimals, err)
			continue
		}
		if got != tc.want {
			t.Errorf("humanToAtomic(%q, %d) = %q, want %q", tc.amount, tc.decimals, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Eval voucher seats mirror the controller's budget math
// ─────────────────────────────────────────────────────────────────────────────

func TestBountyEvalFundRecipients_MirrorsControllerMath(t *testing.T) {
	full := "0x1111111111111111111111111111111111111111"
	probation := "0x2222222222222222222222222222222222222222"
	shadow := "0x3333333333333333333333333333333333333333"
	panel := []monetizeapi.ServiceBountyPanelSeat{
		{Address: full, Seat: monetizeapi.PanelSeatFull},
		{Address: probation, Seat: monetizeapi.PanelSeatProbation},
		{Address: shadow, Seat: monetizeapi.PanelSeatShadow},
	}
	per := big.NewInt(1_000_000)

	// Round 0: full seat at full price, probation at half, shadow free —
	// exactly the controller's evalBudgetTotal / settleEvalBudget math.
	recipients := bountyEvalFundRecipients(panel, per, false)
	if len(recipients) != 2 {
		t.Fatalf("round-0 recipients = %d, want 2 (shadow evaluates free)", len(recipients))
	}
	if recipients[0].Address != full || recipients[0].Amount != "1000000" {
		t.Errorf("full seat = %+v, want %s at 1000000", recipients[0], full)
	}
	if recipients[1].Address != probation || recipients[1].Amount != "500000" {
		t.Errorf("probation seat = %+v, want %s at 500000 (half price)", recipients[1], probation)
	}

	// Voucher total must equal the controller's reserve: k×per − per/2 for
	// one sitting probation seat (k=2 counting seats here).
	total := new(big.Int)
	for _, r := range recipients {
		amount, ok := new(big.Int).SetString(r.Amount, 10)
		if !ok {
			t.Fatalf("recipient amount %q is not a decimal uint256", r.Amount)
		}
		total.Add(total, amount)
	}
	wantTotal := big.NewInt(2*1_000_000 - 1_000_000/2)
	if total.Cmp(wantTotal) != 0 {
		t.Errorf("voucher total = %s, want %s (k×per − per/2)", total, wantTotal)
	}

	// Escalation round: every seat full price, no discount, no free seats —
	// mirrors reserveEscalationBudget.
	escPanel := []monetizeapi.ServiceBountyPanelSeat{
		{Address: full, Seat: monetizeapi.PanelSeatFull},
		{Address: probation, Seat: monetizeapi.PanelSeatFull},
	}
	escRecipients := bountyEvalFundRecipients(escPanel, per, true)
	if len(escRecipients) != 2 {
		t.Fatalf("escalation recipients = %d, want 2", len(escRecipients))
	}
	for _, r := range escRecipients {
		if r.Amount != "1000000" {
			t.Errorf("escalation seat %s = %s, want full price 1000000", r.Address, r.Amount)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Voucher signing
// ─────────────────────────────────────────────────────────────────────────────

func TestSignBountyVoucher_LocalKeyRoundTrip(t *testing.T) {
	// anvil key 0 — test-only, never funded outside local forks.
	key, err := ethcrypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	spender := common.HexToAddress("0x4444444444444444444444444444444444444444")

	voucher := escrow.Permit2Voucher{
		Token:    "0x0a09371a8b011d5110656ceBCc70603e53FD2c78",
		Network:  "base-sepolia",
		Spender:  spender.Hex(),
		Nonce:    bountyVoucherNonce("uid-1", "reward"),
		Deadline: time.Now().Add(time.Hour).Unix(),
		Recipients: []escrow.BatchRecipient{
			{Address: "0x5555555555555555555555555555555555555555", Amount: "1000000"},
		},
	}

	// signBountyVoucher signs AND verifies against the spender binding.
	if err := signBountyVoucher(context.Background(), &voucher, "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", ""); err != nil {
		t.Fatalf("signBountyVoucher: %v", err)
	}
	if want := ethcrypto.PubkeyToAddress(key.PublicKey).Hex(); voucher.Owner != want {
		t.Errorf("voucher owner = %s, want signing key address %s", voucher.Owner, want)
	}
	chainID, err := escrow.ChainIDForNetwork(voucher.Network)
	if err != nil {
		t.Fatalf("ChainIDForNetwork: %v", err)
	}
	if err := escrow.VerifyVoucher(voucher, chainID, spender); err != nil {
		t.Errorf("signed voucher does not verify: %v", err)
	}
}

func TestSignBountyVoucher_RequiresASigner(t *testing.T) {
	voucher := escrow.Permit2Voucher{
		Token:    "0x0a09371a8b011d5110656ceBCc70603e53FD2c78",
		Network:  "base-sepolia",
		Spender:  "0x4444444444444444444444444444444444444444",
		Nonce:    "1",
		Deadline: time.Now().Add(time.Hour).Unix(),
		Recipients: []escrow.BatchRecipient{
			{Address: "0x5555555555555555555555555555555555555555", Amount: "1"},
		},
	}
	err := signBountyVoucher(context.Background(), &voucher, "", "")
	if err == nil {
		t.Fatal("expected error with neither --key nor --signer-url")
	}
	if !strings.Contains(err.Error(), "controller NEVER signs") {
		t.Errorf("error %q must carry the controller-never-signs messaging", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Spender resolution
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveBountySpender(t *testing.T) {
	statusSpender := "0x4444444444444444444444444444444444444444"

	got, err := resolveBountySpender("", statusSpender)
	if err != nil || got != common.HexToAddress(statusSpender).Hex() {
		t.Errorf("status spender path = (%q, %v), want canonical %s", got, err, statusSpender)
	}

	override := "0x5555555555555555555555555555555555555555"
	got, err = resolveBountySpender(override, statusSpender)
	if err != nil || got != common.HexToAddress(override).Hex() {
		t.Errorf("override path = (%q, %v), want %s", got, err, override)
	}

	if _, err := resolveBountySpender("", ""); err == nil {
		t.Error("expected a helpful error when neither --spender nor status.escrowSpender is set")
	}

	if _, err := resolveBountySpender("not-an-address", statusSpender); err == nil {
		t.Error("expected error for malformed --spender")
	}
}
