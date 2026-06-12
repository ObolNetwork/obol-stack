package erc8004

import (
	"testing"
)

func TestResolveNetwork(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"base-sepolia", "base-sepolia", false},
		{"base", "base", false},
		{"base-mainnet", "base", false},
		{"ethereum", "ethereum", false},
		{"mainnet", "ethereum", false},
		{"ethereum-mainnet", "ethereum", false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveNetwork(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveNetwork(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if !tt.wantErr && got.Name != tt.want {
				t.Errorf("ResolveNetwork(%q).Name = %q, want %q", tt.name, got.Name, tt.want)
			}
		})
	}
}

func TestResolveNetworks(t *testing.T) {
	tests := []struct {
		csv     string
		want    int
		wantErr bool
	}{
		{"base-sepolia", 1, false},
		{"mainnet,base", 2, false},
		{"base-sepolia,base,ethereum", 3, false},
		{"base,base", 1, false},        // deduplicate
		{"mainnet,ethereum", 1, false}, // same network, different aliases
		{"", 0, true},
		{"unknown", 0, true},
		{"base,unknown", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.csv, func(t *testing.T) {
			got, err := ResolveNetworks(tt.csv)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveNetworks(%q) error = %v, wantErr %v", tt.csv, err, tt.wantErr)
			}
			if !tt.wantErr && len(got) != tt.want {
				t.Errorf("ResolveNetworks(%q) returned %d networks, want %d", tt.csv, len(got), tt.want)
			}
		})
	}
}

func TestNetworkConfig_CAIP10Registry(t *testing.T) {
	got := BaseSepolia.CAIP10Registry()
	want := "eip155:84532:" + IdentityRegistryBaseSepolia
	if got != want {
		t.Errorf("BaseSepolia.CAIP10Registry() = %q, want %q", got, want)
	}
}

func TestSupportedNetworks(t *testing.T) {
	nets := SupportedNetworks()
	if len(nets) != 3 {
		t.Fatalf("SupportedNetworks() returned %d, want 3", len(nets))
	}
}
