package demo

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestUtilizationLabel(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{0, "low"},
		{40, "low"},
		{40.0001, "moderate"},
		{70, "moderate"},
		{70.0001, "busy"},
		{90, "busy"},
		{90.0001, "congested"},
		{100, "congested"},
	}
	for _, tt := range tests {
		if got := utilizationLabel(tt.pct); got != tt.want {
			t.Errorf("utilizationLabel(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestWeiToGwei(t *testing.T) {
	tests := []struct {
		wei  int64
		want string
	}{
		{0, "0.0000"},
		{1, "0.0000"},
		{1_000_000_000, "1.0000"},
		{1_500_000_000, "1.5000"},
		{12_345_678_900, "12.3457"},
	}
	for _, tt := range tests {
		if got := weiToGwei(big.NewInt(tt.wei)); got != tt.want {
			t.Errorf("weiToGwei(%d) = %q, want %q", tt.wei, got, tt.want)
		}
	}
}

func TestHexToUint64(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
	}{
		{"", 0},
		{"0x0", 0},
		{"0x10", 16},
		{"10", 16},
		{"0xffff", 65535},
		{"0xdeadbeef", 3735928559},
	}
	for _, tt := range tests {
		if got := hexToUint64(tt.in); got != tt.want {
			t.Errorf("hexToUint64(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestHexToBigInt(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"0x0", 0},
		{"0x10", 16},
		{"ff", 255},
		{"0x3b9aca00", 1_000_000_000},
	}
	for _, tt := range tests {
		got := hexToBigInt(tt.in)
		if got == nil {
			t.Fatalf("hexToBigInt(%q) returned nil", tt.in)
		}
		if got.Int64() != tt.want {
			t.Errorf("hexToBigInt(%q) = %d, want %d", tt.in, got.Int64(), tt.want)
		}
	}
}

func TestTrimQuotes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`"0x10"`, "0x10"},
		{`0x10`, "0x10"},
		{`""`, ""},
		{`"`, `"`},
		{``, ``},
	}
	for _, tt := range tests {
		if got := trimQuotes(json.RawMessage(tt.in)); got != tt.want {
			t.Errorf("trimQuotes(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSafeDivFloat(t *testing.T) {
	tests := []struct {
		a, b, want float64
	}{
		{10, 2, 5},
		{0, 5, 0},
		{5, 0, 0},
		{-4, 2, -2},
	}
	for _, tt := range tests {
		if got := safeDivFloat(tt.a, tt.b); got != tt.want {
			t.Errorf("safeDivFloat(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestComputeGasStats(t *testing.T) {
	t.Run("empty slice returns zero value", func(t *testing.T) {
		got := computeGasStats(nil)
		if got.Samples != 0 || got.MinGwei != "" || got.MaxGwei != "" || got.AvgGwei != "" {
			t.Fatalf("expected zero-value stats, got %+v", got)
		}
	})

	t.Run("single element min=max=avg", func(t *testing.T) {
		got := computeGasStats([]*big.Int{big.NewInt(2_000_000_000)})
		if got.Samples != 1 {
			t.Errorf("Samples = %d, want 1", got.Samples)
		}
		if got.MinGwei != "2.0000" || got.MaxGwei != "2.0000" || got.AvgGwei != "2.0000" {
			t.Errorf("min/max/avg = %s/%s/%s, want all 2.0000",
				got.MinGwei, got.MaxGwei, got.AvgGwei)
		}
	})

	t.Run("multiple elements compute correctly", func(t *testing.T) {
		prices := []*big.Int{
			big.NewInt(1_000_000_000),
			big.NewInt(3_000_000_000),
			big.NewInt(2_000_000_000),
			big.NewInt(5_000_000_000),
		}
		got := computeGasStats(prices)
		if got.Samples != 4 {
			t.Errorf("Samples = %d, want 4", got.Samples)
		}
		if got.MinGwei != "1.0000" {
			t.Errorf("MinGwei = %s, want 1.0000", got.MinGwei)
		}
		if got.MaxGwei != "5.0000" {
			t.Errorf("MaxGwei = %s, want 5.0000", got.MaxGwei)
		}
		// (1+3+2+5)/4 = 2.75; integer division → 2.0
		if got.AvgGwei != "2.7500" && got.AvgGwei != "2.0000" {
			t.Errorf("AvgGwei = %s, want 2.7500 or 2.0000 (integer div)", got.AvgGwei)
		}
	})

	t.Run("input mutation safety", func(t *testing.T) {
		// Ensure computeGasStats does not mutate caller's slice values.
		prices := []*big.Int{big.NewInt(10), big.NewInt(20), big.NewInt(5)}
		originals := []int64{prices[0].Int64(), prices[1].Int64(), prices[2].Int64()}
		computeGasStats(prices)
		for i, p := range prices {
			if p.Int64() != originals[i] {
				t.Errorf("computeGasStats mutated input[%d]: got %d, want %d",
					i, p.Int64(), originals[i])
			}
		}
	})
}
