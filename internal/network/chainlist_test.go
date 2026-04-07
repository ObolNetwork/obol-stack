package network

import (
	"testing"
)

// sampleChainListJSON is a minimal fixture mimicking the ChainList rpcs.json format.
var sampleChainListJSON = []byte(`[
	{
		"name": "Base",
		"chain": "ETH",
		"chainId": 8453,
		"rpc": [
			{
				"url": "https://mainnet.base.org",
				"tracking": "none",
				"trackingDetails": "No tracking"
			},
			{
				"url": "https://base-rpc.publicnode.com",
				"tracking": "none"
			},
			{
				"url": "https://base.drpc.org",
				"tracking": "limited"
			},
			{
				"url": "http://base-insecure.example.com",
				"tracking": "none"
			},
			{
				"url": "https://base-tracked.example.com",
				"tracking": "yes"
			},
			{
				"url": "https://base-api.example.com/${API_KEY}",
				"tracking": "none"
			},
			{
				"url": "https://base-extra.example.com",
				"tracking": "unknown"
			},
			"https://base-string-only.example.com"
		]
	},
	{
		"name": "Ethereum Mainnet",
		"chain": "ETH",
		"chainId": 1,
		"rpc": [
			{
				"url": "https://eth.drpc.org",
				"tracking": "none"
			},
			{
				"url": "https://rpc.ankr.com/eth",
				"tracking": "limited"
			}
		]
	},
	{
		"name": "Arbitrum One",
		"chain": "ETH",
		"chainId": 42161,
		"rpc": [
			{
				"url": "https://arb1.arbitrum.io/rpc",
				"tracking": "none"
			}
		]
	}
]`)

func TestParseChainListResponse(t *testing.T) {
	endpoints, name, err := ParseAndFilterRPCs(sampleChainListJSON, 8453, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "Base" {
		t.Errorf("expected chain name 'Base', got %q", name)
	}

	// Should find endpoints (filtered from the 8 entries).
	if len(endpoints) == 0 {
		t.Fatal("expected at least one endpoint")
	}

	// Verify no HTTP-only endpoints.
	for _, ep := range endpoints {
		if ep.URL[:8] != "https://" {
			t.Errorf("non-HTTPS endpoint found: %s", ep.URL)
		}
	}

	// Verify no tracked endpoints.
	for _, ep := range endpoints {
		if ep.Tracking == "yes" {
			t.Errorf("tracked endpoint found: %s", ep.URL)
		}
	}

	// Verify no API key placeholder endpoints.
	for _, ep := range endpoints {
		if ep.URL == "https://base-api.example.com/${API_KEY}" {
			t.Error("API key placeholder endpoint should be filtered out")
		}
	}
}

func TestParseChainListResponse_NotFound(t *testing.T) {
	_, _, err := ParseAndFilterRPCs(sampleChainListJSON, 99999, 10)
	if err == nil {
		t.Fatal("expected error for unknown chain ID")
	}
}

func TestParseChainListResponse_InvalidJSON(t *testing.T) {
	_, _, err := ParseAndFilterRPCs([]byte("not json"), 1, 10)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFilterFreeRPCs(t *testing.T) {
	endpoints := []RPCEndpoint{
		{URL: "https://good.example.com", Tracking: "none"},
		{URL: "https://limited.example.com", Tracking: "limited"},
		{URL: "https://tracked.example.com", Tracking: "yes"},
		{URL: "http://insecure.example.com", Tracking: "none"},
		{URL: "https://api-key.example.com/${KEY}", Tracking: "none"},
		{URL: "https://brace-key.example.com/{key}", Tracking: "none"},
		{URL: "wss://websocket.example.com", Tracking: "none"},
	}

	result := FilterFreeRPCs(endpoints)

	if len(result) != 2 {
		t.Fatalf("expected 2 filtered endpoints, got %d", len(result))
	}

	expectedURLs := map[string]bool{
		"https://good.example.com":    false,
		"https://limited.example.com": false,
	}

	for _, ep := range result {
		if _, ok := expectedURLs[ep.URL]; ok {
			expectedURLs[ep.URL] = true
		} else {
			t.Errorf("unexpected endpoint: %s", ep.URL)
		}
	}

	for url, found := range expectedURLs {
		if !found {
			t.Errorf("expected endpoint not found: %s", url)
		}
	}
}

func TestSortByQuality(t *testing.T) {
	endpoints := []RPCEndpoint{
		{URL: "https://unknown.com", Tracking: "unknown"},
		{URL: "https://limited.com", Tracking: "limited"},
		{URL: "https://none.com", Tracking: "none"},
		{URL: "https://other.com", Tracking: "partial"},
	}

	SortByQuality(endpoints)

	expected := []string{"none", "limited", "unknown", "partial"}
	for i, ep := range endpoints {
		if ep.Tracking != expected[i] {
			t.Errorf("position %d: expected tracking=%q, got %q", i, expected[i], ep.Tracking)
		}
	}
}

func TestSortByQuality_StableOrder(t *testing.T) {
	// Endpoints with the same tracking score should maintain relative order.
	endpoints := []RPCEndpoint{
		{URL: "https://a.com", Tracking: "none"},
		{URL: "https://b.com", Tracking: "none"},
		{URL: "https://c.com", Tracking: "none"},
	}

	SortByQuality(endpoints)

	if endpoints[0].URL != "https://a.com" || endpoints[1].URL != "https://b.com" || endpoints[2].URL != "https://c.com" {
		t.Error("stable sort not preserved for equal elements")
	}
}

func TestParseChainListResponse_MaxRPCsCap(t *testing.T) {
	endpoints, _, err := ParseAndFilterRPCs(sampleChainListJSON, 8453, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints) > 2 {
		t.Errorf("expected at most 2 endpoints, got %d", len(endpoints))
	}
}

func TestResolveChainID(t *testing.T) {
	tests := []struct {
		input       string
		wantChainID int
		wantErr     bool
	}{
		{"base", 8453, false},
		{"Base", 8453, false},
		{"BASE", 8453, false},
		{"ethereum", 1, false},
		{"mainnet", 1, false},
		{"arbitrum", 42161, false},
		{"8453", 8453, false},
		{"137", 137, false},
		{"unknown-chain", 0, true},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		chainID, _, err := ResolveChainID(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ResolveChainID(%q): expected error, got chainID=%d", tt.input, chainID)
			}

			continue
		}

		if err != nil {
			t.Errorf("ResolveChainID(%q): unexpected error: %v", tt.input, err)
			continue
		}

		if chainID != tt.wantChainID {
			t.Errorf("ResolveChainID(%q): got chainID=%d, want %d", tt.input, chainID, tt.wantChainID)
		}
	}
}

func TestFetchChainListRPCs_WithFixture(t *testing.T) {
	// Test using a mock fetcher that returns the sample fixture.
	fetcher := func() ([]byte, error) {
		return sampleChainListJSON, nil
	}

	endpoints, name, err := FetchChainListRPCs(8453, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "Base" {
		t.Errorf("expected name 'Base', got %q", name)
	}

	if len(endpoints) == 0 {
		t.Fatal("expected at least one endpoint")
	}

	// Default max is 3.
	if len(endpoints) > 3 {
		t.Errorf("expected at most 3 endpoints (default), got %d", len(endpoints))
	}

	// First endpoint should have tracking=none (best quality).
	if endpoints[0].Tracking != "none" {
		t.Errorf("first endpoint should have tracking=none, got %q", endpoints[0].Tracking)
	}
}

func TestFetchChainListRPCs_ChainNotFound(t *testing.T) {
	fetcher := func() ([]byte, error) {
		return sampleChainListJSON, nil
	}

	_, _, err := FetchChainListRPCs(99999, fetcher)
	if err == nil {
		t.Fatal("expected error for unknown chain ID")
	}
}
