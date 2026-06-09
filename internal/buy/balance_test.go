package buy

import (
	"math/big"
	"strings"
	"testing"
)

// parseBalanceOutput is the pure half of FetchWalletInfo — it parses the
// well-known shape `buy.py balance` prints. Tested in isolation so we don't
// need a live cluster.
func TestParseBalanceOutput(t *testing.T) {
	t.Parallel()
	out := strings.Join([]string{
		"Wallet:  0xAbCDef0000000000000000000000000000000123",
		"Chain:   base-sepolia",
		"USDC:    1.234567 (1234567 micro-units)",
		"OBOL:    0.012345 (12345000000000000 base-units)",
	}, "\n")

	t.Run("USDC", func(t *testing.T) {
		t.Parallel()
		got, err := parseBalanceOutput(out, "USDC", "base-sepolia")
		if err != nil {
			t.Fatalf("parseBalanceOutput USDC: %v", err)
		}
		if got.Address != "0xAbCDef0000000000000000000000000000000123" {
			t.Fatalf("address = %q", got.Address)
		}
		if got.AtomicUnits.Cmp(big.NewInt(1234567)) != 0 {
			t.Fatalf("USDC atomic = %s", got.AtomicUnits)
		}
		if got.Decimals != 6 {
			t.Fatalf("USDC decimals = %d, want 6", got.Decimals)
		}
		if got.HumanBalance() != "1.234567" {
			t.Fatalf("USDC human = %s, want 1.234567", got.HumanBalance())
		}
	})

	t.Run("OBOL", func(t *testing.T) {
		t.Parallel()
		got, err := parseBalanceOutput(out, "OBOL", "base-sepolia")
		if err != nil {
			t.Fatalf("parseBalanceOutput OBOL: %v", err)
		}
		want, _ := new(big.Int).SetString("12345000000000000", 10)
		if got.AtomicUnits.Cmp(want) != 0 {
			t.Fatalf("OBOL atomic = %s, want %s", got.AtomicUnits, want)
		}
		if got.Decimals != 18 {
			t.Fatalf("OBOL decimals = %d, want 18", got.Decimals)
		}
	})

	t.Run("missing wallet", func(t *testing.T) {
		t.Parallel()
		_, err := parseBalanceOutput("USDC: 1.0 (1000000 micro-units)", "USDC", "base-sepolia")
		if err == nil || !strings.Contains(err.Error(), "Wallet:") {
			t.Fatalf("expected wallet-missing error, got %v", err)
		}
	})

	t.Run("missing token row", func(t *testing.T) {
		t.Parallel()
		_, err := parseBalanceOutput("Wallet:  0xabc\n", "USDC", "base-sepolia")
		if err == nil || !strings.Contains(err.Error(), "no USDC row") {
			t.Fatalf("expected missing-row error, got %v", err)
		}
	})
}
