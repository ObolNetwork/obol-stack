package erc8004

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestReputationABI_Parses(t *testing.T) {
	if _, err := reputationABI(); err != nil {
		t.Fatalf("embedded reputation ABI failed to parse: %v", err)
	}
}

// TestReputationABI_SelectorGoldenValues pins the 4-byte selectors of the
// verified v2.0.0 signatures (spec: https://eips.ethereum.org/EIPS/eip-8004;
// ABI: https://github.com/erc-8004/erc-8004-contracts). Each golden value is
// cross-checked against keccak256 of the canonical signature string and the
// parsed ABI method.
func TestReputationABI_SelectorGoldenValues(t *testing.T) {
	parsed, err := reputationABI()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		method   string
		sig      string
		selector string
	}{
		{"giveFeedback", "giveFeedback(uint256,int128,uint8,string,string,string,string,bytes32)", "3c036a7e"},
		{"revokeFeedback", "revokeFeedback(uint256,uint64)", "4ab3ca99"},
		{"appendResponse", "appendResponse(uint256,address,uint64,string,bytes32)", "c2349ab2"},
		{"getSummary", "getSummary(uint256,address[],string,string)", "81bbba58"},
		{"readFeedback", "readFeedback(uint256,address,uint64)", "232b0810"},
		{"getLastIndex", "getLastIndex(uint256,address)", "f2d81759"},
		{"getClients", "getClients(uint256)", "42dd519c"},
		{"getIdentityRegistry", "getIdentityRegistry()", "bc4d861b"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			m, ok := parsed.Methods[tt.method]
			if !ok {
				t.Fatalf("method %q missing from parsed ABI", tt.method)
			}
			if m.Sig != tt.sig {
				t.Errorf("signature = %q, want %q", m.Sig, tt.sig)
			}
			if got := hex.EncodeToString(m.ID); got != tt.selector {
				t.Errorf("parsed selector = 0x%s, want 0x%s", got, tt.selector)
			}
			if got := hex.EncodeToString(crypto.Keccak256([]byte(tt.sig))[:4]); got != tt.selector {
				t.Errorf("keccak256(%q)[:4] = 0x%s, want 0x%s", tt.sig, got, tt.selector)
			}
		})
	}
}

func TestReputationABI_EventsPresent(t *testing.T) {
	parsed, err := reputationABI()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"NewFeedback", "FeedbackRevoked", "ResponseAppended"} {
		if _, ok := parsed.Events[name]; !ok {
			t.Errorf("missing event %q in parsed ABI", name)
		}
	}
}

func TestEncodeGiveFeedback_RoundTrip(t *testing.T) {
	agentID := big.NewInt(42)
	value := big.NewInt(-875) // -87.5 with valueDecimals=1
	feedbackHash := crypto.Keccak256Hash([]byte("feedback payload"))

	data, err := EncodeGiveFeedback(agentID, value, 1, "code-review", "go", "https://agent.example/v1", "ipfs://bafy.../fb.json", feedbackHash)
	if err != nil {
		t.Fatalf("EncodeGiveFeedback: %v", err)
	}
	if got := hex.EncodeToString(data[:4]); got != "3c036a7e" {
		t.Errorf("selector = 0x%s, want 0x3c036a7e", got)
	}

	decoded, err := DecodeGiveFeedbackCalldata(data)
	if err != nil {
		t.Fatalf("DecodeGiveFeedbackCalldata: %v", err)
	}
	if decoded.AgentID.Cmp(agentID) != 0 {
		t.Errorf("agentId = %s, want %s", decoded.AgentID, agentID)
	}
	if decoded.Value.Cmp(value) != 0 {
		t.Errorf("value = %s, want %s", decoded.Value, value)
	}
	if decoded.ValueDecimals != 1 {
		t.Errorf("valueDecimals = %d, want 1", decoded.ValueDecimals)
	}
	if decoded.Tag1 != "code-review" || decoded.Tag2 != "go" {
		t.Errorf("tags = (%q, %q), want (code-review, go)", decoded.Tag1, decoded.Tag2)
	}
	if decoded.Endpoint != "https://agent.example/v1" {
		t.Errorf("endpoint = %q", decoded.Endpoint)
	}
	if decoded.FeedbackURI != "ipfs://bafy.../fb.json" {
		t.Errorf("feedbackURI = %q", decoded.FeedbackURI)
	}
	if decoded.FeedbackHash != feedbackHash {
		t.Errorf("feedbackHash = %s, want %s", decoded.FeedbackHash, feedbackHash)
	}
}

func TestEncodeRevokeFeedback_RoundTrip(t *testing.T) {
	data, err := EncodeRevokeFeedback(big.NewInt(42), 7)
	if err != nil {
		t.Fatalf("EncodeRevokeFeedback: %v", err)
	}
	if got := hex.EncodeToString(data[:4]); got != "4ab3ca99" {
		t.Errorf("selector = 0x%s, want 0x4ab3ca99", got)
	}

	decoded, err := DecodeRevokeFeedbackCalldata(data)
	if err != nil {
		t.Fatalf("DecodeRevokeFeedbackCalldata: %v", err)
	}
	if decoded.AgentID.Cmp(big.NewInt(42)) != 0 || decoded.FeedbackIndex != 7 {
		t.Errorf("decoded = %+v, want agentId=42 feedbackIndex=7", decoded)
	}
}

func TestEncodeAppendResponse_RoundTrip(t *testing.T) {
	client := common.HexToAddress("0x4444444444444444444444444444444444444444")
	respHash := crypto.Keccak256Hash([]byte("response payload"))

	data, err := EncodeAppendResponse(big.NewInt(42), client, 7, "ipfs://bafy.../resp.json", respHash)
	if err != nil {
		t.Fatalf("EncodeAppendResponse: %v", err)
	}
	if got := hex.EncodeToString(data[:4]); got != "c2349ab2" {
		t.Errorf("selector = 0x%s, want 0xc2349ab2", got)
	}

	decoded, err := DecodeAppendResponseCalldata(data)
	if err != nil {
		t.Fatalf("DecodeAppendResponseCalldata: %v", err)
	}
	if decoded.AgentID.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("agentId = %s, want 42", decoded.AgentID)
	}
	if decoded.ClientAddress != client {
		t.Errorf("clientAddress = %s, want %s", decoded.ClientAddress, client)
	}
	if decoded.FeedbackIndex != 7 {
		t.Errorf("feedbackIndex = %d, want 7", decoded.FeedbackIndex)
	}
	if decoded.ResponseURI != "ipfs://bafy.../resp.json" {
		t.Errorf("responseURI = %q", decoded.ResponseURI)
	}
	if decoded.ResponseHash != respHash {
		t.Errorf("responseHash = %s, want %s", decoded.ResponseHash, respHash)
	}
}

func TestEncodeGiveFeedback_BadInput(t *testing.T) {
	hash := crypto.Keccak256Hash([]byte("x"))
	overMax := new(big.Int).Add(maxFeedbackAbsValue, big.NewInt(1))
	underMin := new(big.Int).Neg(overMax)

	tests := []struct {
		name string
		fn   func() ([]byte, error)
	}{
		{"nil agentId", func() ([]byte, error) {
			return EncodeGiveFeedback(nil, big.NewInt(1), 0, "", "", "", "", hash)
		}},
		{"negative agentId", func() ([]byte, error) {
			return EncodeGiveFeedback(big.NewInt(-1), big.NewInt(1), 0, "", "", "", "", hash)
		}},
		{"nil value", func() ([]byte, error) {
			return EncodeGiveFeedback(big.NewInt(1), nil, 0, "", "", "", "", hash)
		}},
		{"value over 1e38", func() ([]byte, error) {
			return EncodeGiveFeedback(big.NewInt(1), overMax, 0, "", "", "", "", hash)
		}},
		{"value under -1e38", func() ([]byte, error) {
			return EncodeGiveFeedback(big.NewInt(1), underMin, 0, "", "", "", "", hash)
		}},
		{"valueDecimals 19", func() ([]byte, error) {
			return EncodeGiveFeedback(big.NewInt(1), big.NewInt(1), 19, "", "", "", "", hash)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.fn(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}

	// Boundary values must be accepted.
	if _, err := EncodeGiveFeedback(big.NewInt(1), maxFeedbackAbsValue, MaxFeedbackValueDecimals, "", "", "", "", common.Hash{}); err != nil {
		t.Errorf("value 1e38, decimals 18 should be accepted: %v", err)
	}
	if _, err := EncodeGiveFeedback(big.NewInt(1), new(big.Int).Neg(maxFeedbackAbsValue), 0, "", "", "", "", common.Hash{}); err != nil {
		t.Errorf("value -1e38 should be accepted: %v", err)
	}
}

func TestEncodeRevokeFeedback_BadInput(t *testing.T) {
	if _, err := EncodeRevokeFeedback(nil, 1); err == nil {
		t.Error("nil agentId: expected error")
	}
	if _, err := EncodeRevokeFeedback(big.NewInt(1), 0); err == nil {
		t.Error("feedbackIndex 0: expected error")
	}
}

func TestEncodeAppendResponse_BadInput(t *testing.T) {
	client := common.HexToAddress("0x4444444444444444444444444444444444444444")
	if _, err := EncodeAppendResponse(nil, client, 1, "u", common.Hash{}); err == nil {
		t.Error("nil agentId: expected error")
	}
	if _, err := EncodeAppendResponse(big.NewInt(1), common.Address{}, 1, "u", common.Hash{}); err == nil {
		t.Error("zero clientAddress: expected error")
	}
	if _, err := EncodeAppendResponse(big.NewInt(1), client, 0, "u", common.Hash{}); err == nil {
		t.Error("feedbackIndex 0: expected error")
	}
	if _, err := EncodeAppendResponse(big.NewInt(1), client, 1, "", common.Hash{}); err == nil {
		t.Error("empty responseURI: expected error")
	}
}

func TestDecodeReputationCalldata_Errors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		if _, err := DecodeGiveFeedbackCalldata([]byte{0x3c}); err == nil {
			t.Error("expected error for short calldata")
		}
	})

	t.Run("wrong selector", func(t *testing.T) {
		data, err := EncodeRevokeFeedback(big.NewInt(1), 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeGiveFeedbackCalldata(data); err == nil {
			t.Error("expected selector mismatch error")
		} else if !strings.Contains(err.Error(), "selector mismatch") {
			t.Errorf("error = %v, want selector mismatch", err)
		}
	})

	t.Run("truncated args", func(t *testing.T) {
		data, err := EncodeGiveFeedback(big.NewInt(1), big.NewInt(50), 0, "t1", "t2", "e", "u", common.Hash{})
		if err != nil {
			t.Fatal(err)
		}
		// Cut the entire trailing dynamic section so the feedbackURI offset
		// points past the end of the payload.
		if _, err := DecodeGiveFeedbackCalldata(data[:len(data)-96]); err == nil {
			t.Error("expected error for truncated calldata")
		}
	})
}

func TestReputationRegistryAddress(t *testing.T) {
	tests := []struct {
		network string
		want    string
		wantErr bool
	}{
		{"base-sepolia", ReputationRegistryBaseSepolia, false},
		{"base", ReputationRegistryMainnet, false},
		{"base-mainnet", ReputationRegistryMainnet, false},
		{"ethereum", ReputationRegistryMainnet, false},
		{"mainnet", ReputationRegistryMainnet, false},
		{"solana", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.network, func(t *testing.T) {
			got, err := ReputationRegistryAddress(tt.network)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got address %s", tt.network, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReputationRegistryAddress(%q): %v", tt.network, err)
			}
			if got != tt.want {
				t.Errorf("address = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNewReputationReader_BadInput(t *testing.T) {
	if _, err := NewReputationReader(nil, ReputationRegistryBaseSepolia); err == nil {
		t.Error("nil caller: expected error")
	}
	if _, err := NewReputationReader(&stubCaller{}, "0xZZ"); err == nil {
		t.Error("bad address: expected error")
	}
}

func TestReputationReader_Summary(t *testing.T) {
	parsed, err := reputationABI()
	if err != nil {
		t.Fatal(err)
	}
	ret, err := parsed.Methods["getSummary"].Outputs.Pack(uint64(12), big.NewInt(925), uint8(1))
	if err != nil {
		t.Fatal(err)
	}

	caller := &stubCaller{ret: ret}
	reader, err := NewReputationReader(caller, ReputationRegistryBaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	summary, err := reader.Summary(context.Background(), big.NewInt(42), nil, "code-review", "")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Count != 12 {
		t.Errorf("count = %d, want 12", summary.Count)
	}
	if summary.SummaryValue.Cmp(big.NewInt(925)) != 0 {
		t.Errorf("summaryValue = %s, want 925", summary.SummaryValue)
	}
	if summary.SummaryValueDecimals != 1 {
		t.Errorf("summaryValueDecimals = %d, want 1", summary.SummaryValueDecimals)
	}

	wantData, err := parsed.Pack("getSummary", big.NewInt(42), []common.Address{}, "code-review", "")
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(caller.lastCall.Data) != hex.EncodeToString(wantData) {
		t.Errorf("call data = 0x%x, want 0x%x", caller.lastCall.Data, wantData)
	}

	if _, err := reader.Summary(context.Background(), nil, nil, "", ""); err == nil {
		t.Error("nil agentId: expected error")
	}
}

func TestReputationReader_ReadFeedback(t *testing.T) {
	parsed, err := reputationABI()
	if err != nil {
		t.Fatal(err)
	}
	ret, err := parsed.Methods["readFeedback"].Outputs.Pack(big.NewInt(-50), uint8(0), "code-review", "go", true)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewReputationReader(&stubCaller{ret: ret}, ReputationRegistryBaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := reader.ReadFeedback(context.Background(), big.NewInt(42), common.HexToAddress("0x4444444444444444444444444444444444444444"), 3)
	if err != nil {
		t.Fatalf("ReadFeedback: %v", err)
	}
	if entry.Value.Cmp(big.NewInt(-50)) != 0 {
		t.Errorf("value = %s, want -50", entry.Value)
	}
	if entry.ValueDecimals != 0 {
		t.Errorf("valueDecimals = %d, want 0", entry.ValueDecimals)
	}
	if entry.Tag1 != "code-review" || entry.Tag2 != "go" {
		t.Errorf("tags = (%q, %q)", entry.Tag1, entry.Tag2)
	}
	if !entry.IsRevoked {
		t.Error("isRevoked = false, want true")
	}
}

func TestReputationReader_LastIndex(t *testing.T) {
	parsed, err := reputationABI()
	if err != nil {
		t.Fatal(err)
	}
	ret, err := parsed.Methods["getLastIndex"].Outputs.Pack(uint64(9))
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewReputationReader(&stubCaller{ret: ret}, ReputationRegistryBaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	idx, err := reader.LastIndex(context.Background(), big.NewInt(42), common.HexToAddress("0x4444444444444444444444444444444444444444"))
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if idx != 9 {
		t.Errorf("lastIndex = %d, want 9", idx)
	}
}
