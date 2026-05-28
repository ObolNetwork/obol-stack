package network

import "testing"

func TestParseSince(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantNum  uint64 // Block for "before", Distance for "distance"
		wantErr  bool
	}{
		{"genesis", "all", 0, false},
		{"all", "all", 0, false},
		{"merge", "before", 15537394, false},
		{"MERGE", "before", 15537394, false},
		{"shanghai", "before", 17034870, false},
		{"cancun", "before", 19426587, false},
		{"prague", "before", 22431084, false},
		{"osaka", "before", 23935694, false},
		{"365d", "distance", 365 * 24 * 60 * 60 / 12, false},
		{"1y", "distance", 365 * 24 * 60 * 60 / 12, false},
		{"6mo", "distance", 6 * 30 * 24 * 60 * 60 / 12, false},
		{"15537394", "before", 15537394, false},
		{"", "", 0, true},
		{"yesterday", "", 0, true},
		{"-1y", "", 0, true},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseSince(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if got.Kind != c.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, c.wantKind)
			}
			switch got.Kind {
			case "before":
				if got.Block != c.wantNum {
					t.Fatalf("block = %d, want %d", got.Block, c.wantNum)
				}
			case "distance":
				if got.Distance != c.wantNum {
					t.Fatalf("distance = %d, want %d", got.Distance, c.wantNum)
				}
			}
		})
	}
}

func TestMainnetHardforkOrder(t *testing.T) {
	for i := 1; i < len(MainnetHardforks); i++ {
		if MainnetHardforks[i-1].Block >= MainnetHardforks[i].Block {
			t.Fatalf("hardforks must be ordered oldest first: %s (block %d) >= %s (block %d)",
				MainnetHardforks[i-1].Name, MainnetHardforks[i-1].Block,
				MainnetHardforks[i].Name, MainnetHardforks[i].Block)
		}
	}
}
