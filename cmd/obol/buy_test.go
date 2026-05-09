package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestUSDCToMicroUnits(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		{in: "1", want: "1000000"},
		{in: "10", want: "10000000"},
		{in: "0.001", want: "1000"},
		{in: "0.000001", want: "1"},
		{in: "  0.5  ", want: "500000"},
		{in: "", wantErr: "empty"},
		{in: "abc", wantErr: "valid number"},
		{in: "0", wantErr: "positive"},
		{in: "-1", wantErr: "positive"},
		{in: "0.0000001", wantErr: "more precision"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := usdcToMicroUnits(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("usdcToMicroUnits(%q) err = %v, want substring %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("usdcToMicroUnits(%q) unexpected err: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("usdcToMicroUnits(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildBuyPyArgv(t *testing.T) {
	tests := []struct {
		name string
		opts buyPyOptions
		want []string
	}{
		{
			name: "minimal",
			opts: buyPyOptions{
				Name:        "default-paid",
				Seller:      "https://demo.example/services/x",
				BudgetMicro: "10000000",
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "default-paid",
				"--endpoint", "https://demo.example/services/x",
				"--budget", "10000000",
			},
		},
		{
			name: "all optional flags",
			opts: buyPyOptions{
				Name:            "demo",
				Seller:          "https://s.example",
				Model:           "qwen3.5:9b",
				BudgetMicro:     "5000000",
				AutoRefill:      true,
				RefillThreshold: 3,
				RefillCount:     7,
				Force:           true,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "5000000",
				"--model", "qwen3.5:9b",
				"--auto-refill",
				"--refill-threshold", "3",
				"--refill-count", "7",
				"--force",
			},
		},
		{
			name: "auto-refill without explicit counts",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				BudgetMicro: "1000000",
				AutoRefill:  true,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
				"--auto-refill",
			},
		},
		{
			name: "auto-refill off does not emit refill flags",
			opts: buyPyOptions{
				Name:            "demo",
				Seller:          "https://s.example",
				BudgetMicro:     "1000000",
				AutoRefill:      false,
				RefillThreshold: 3,
				RefillCount:     7,
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
			},
		},
		{
			name: "model whitespace trimmed",
			opts: buyPyOptions{
				Name:        "demo",
				Seller:      "https://s.example",
				Model:       "  qwen3.5:9b  ",
				BudgetMicro: "1000000",
			},
			want: []string{
				hermesPython, hermesBuyPyPath, "buy", "demo",
				"--endpoint", "https://s.example",
				"--budget", "1000000",
				"--model", "qwen3.5:9b",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildBuyPyArgv(tc.opts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildBuyPyArgv() = %#v\n want                  %#v", got, tc.want)
			}
		})
	}
}
