package erc8004

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// stubCaller is a bind.ContractCaller that returns canned ABI-encoded output.
// Shared by validation and reputation reader tests. Never hits the network.
type stubCaller struct {
	ret      []byte
	err      error
	lastCall ethereum.CallMsg
}

func (s *stubCaller) CodeAt(_ context.Context, _ common.Address, _ *big.Int) ([]byte, error) {
	return []byte{0x01}, nil
}

func (s *stubCaller) CallContract(_ context.Context, call ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	s.lastCall = call
	return s.ret, s.err
}

func TestValidationABI_Parses(t *testing.T) {
	if _, err := validationABI(); err != nil {
		t.Fatalf("embedded validation ABI failed to parse: %v", err)
	}
}

// TestValidationABI_SelectorGoldenValues pins the 4-byte selectors of the
// verified v2.0.0 signatures (spec: https://eips.ethereum.org/EIPS/eip-8004;
// ABI: https://github.com/erc-8004/erc-8004-contracts). Each golden value is
// cross-checked against keccak256 of the canonical signature string and the
// parsed ABI method.
func TestValidationABI_SelectorGoldenValues(t *testing.T) {
	parsed, err := validationABI()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		method   string
		sig      string
		selector string
	}{
		{"validationRequest", "validationRequest(address,uint256,string,bytes32)", "aaf400c4"},
		{"validationResponse", "validationResponse(bytes32,uint8,string,bytes32,string)", "3d659a96"},
		{"getValidationStatus", "getValidationStatus(bytes32)", "ff2febfc"},
		{"getSummary", "getSummary(uint256,address[],string)", "1b7cabd6"},
		{"getAgentValidations", "getAgentValidations(uint256)", "8d5d0c2d"},
		{"getValidatorRequests", "getValidatorRequests(address)", "4bf3158c"},
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

func TestValidationABI_EventsPresent(t *testing.T) {
	parsed, err := validationABI()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ValidationRequest", "ValidationResponse"} {
		if _, ok := parsed.Events[name]; !ok {
			t.Errorf("missing event %q in parsed ABI", name)
		}
	}
}

func TestEncodeValidationRequest_RoundTrip(t *testing.T) {
	validator := common.HexToAddress("0x1111111111111111111111111111111111111111")
	agentID := big.NewInt(42)
	requestURI := "https://example.org/bounty/42/request.json"
	requestHash := crypto.Keccak256Hash([]byte("request payload"))

	data, err := EncodeValidationRequest(validator, agentID, requestURI, requestHash)
	if err != nil {
		t.Fatalf("EncodeValidationRequest: %v", err)
	}
	if got := hex.EncodeToString(data[:4]); got != "aaf400c4" {
		t.Errorf("selector = 0x%s, want 0xaaf400c4", got)
	}

	decoded, err := DecodeValidationRequestCalldata(data)
	if err != nil {
		t.Fatalf("DecodeValidationRequestCalldata: %v", err)
	}
	if decoded.ValidatorAddress != validator {
		t.Errorf("validatorAddress = %s, want %s", decoded.ValidatorAddress, validator)
	}
	if decoded.AgentID.Cmp(agentID) != 0 {
		t.Errorf("agentId = %s, want %s", decoded.AgentID, agentID)
	}
	if decoded.RequestURI != requestURI {
		t.Errorf("requestURI = %q, want %q", decoded.RequestURI, requestURI)
	}
	if decoded.RequestHash != requestHash {
		t.Errorf("requestHash = %s, want %s", decoded.RequestHash, requestHash)
	}
}

func TestEncodeValidationResponse_RoundTrip(t *testing.T) {
	requestHash := crypto.Keccak256Hash([]byte("request payload"))
	responseHash := crypto.Keccak256Hash([]byte("evaluation artifact"))

	data, err := EncodeValidationResponse(requestHash, 87, "ipfs://bafy.../eval.json", responseHash, "code-review")
	if err != nil {
		t.Fatalf("EncodeValidationResponse: %v", err)
	}
	if got := hex.EncodeToString(data[:4]); got != "3d659a96" {
		t.Errorf("selector = 0x%s, want 0x3d659a96", got)
	}

	decoded, err := DecodeValidationResponseCalldata(data)
	if err != nil {
		t.Fatalf("DecodeValidationResponseCalldata: %v", err)
	}
	if decoded.RequestHash != requestHash {
		t.Errorf("requestHash = %s, want %s", decoded.RequestHash, requestHash)
	}
	if decoded.Response != 87 {
		t.Errorf("response = %d, want 87", decoded.Response)
	}
	if decoded.ResponseURI != "ipfs://bafy.../eval.json" {
		t.Errorf("responseURI = %q", decoded.ResponseURI)
	}
	if decoded.ResponseHash != responseHash {
		t.Errorf("responseHash = %s, want %s", decoded.ResponseHash, responseHash)
	}
	if decoded.Tag != "code-review" {
		t.Errorf("tag = %q, want %q", decoded.Tag, "code-review")
	}
}

func TestEncodeValidationResponse_OptionalFieldsZero(t *testing.T) {
	requestHash := crypto.Keccak256Hash([]byte("req"))
	data, err := EncodeValidationResponse(requestHash, 0, "", common.Hash{}, "")
	if err != nil {
		t.Fatalf("EncodeValidationResponse with zero optionals: %v", err)
	}
	decoded, err := DecodeValidationResponseCalldata(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Response != 0 || decoded.ResponseURI != "" || decoded.Tag != "" || decoded.ResponseHash != (common.Hash{}) {
		t.Errorf("zero optionals did not round-trip: %+v", decoded)
	}
}

func TestEncodeValidationRequest_BadInput(t *testing.T) {
	validator := common.HexToAddress("0x1111111111111111111111111111111111111111")
	hash := crypto.Keccak256Hash([]byte("x"))

	tests := []struct {
		name string
		fn   func() ([]byte, error)
	}{
		{"zero validator", func() ([]byte, error) {
			return EncodeValidationRequest(common.Address{}, big.NewInt(1), "u", hash)
		}},
		{"nil agentId", func() ([]byte, error) {
			return EncodeValidationRequest(validator, nil, "u", hash)
		}},
		{"negative agentId", func() ([]byte, error) {
			return EncodeValidationRequest(validator, big.NewInt(-1), "u", hash)
		}},
		{"zero requestHash", func() ([]byte, error) {
			return EncodeValidationRequest(validator, big.NewInt(1), "u", common.Hash{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.fn(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestEncodeValidationResponse_BadInput(t *testing.T) {
	hash := crypto.Keccak256Hash([]byte("x"))

	if _, err := EncodeValidationResponse(common.Hash{}, 50, "", common.Hash{}, ""); err == nil {
		t.Error("zero requestHash: expected error, got nil")
	}
	if _, err := EncodeValidationResponse(hash, 101, "", common.Hash{}, ""); err == nil {
		t.Error("response 101: expected error, got nil")
	}
	if _, err := EncodeValidationResponse(hash, MaxValidationResponse, "", common.Hash{}, ""); err != nil {
		t.Errorf("response 100 should be accepted: %v", err)
	}
}

func TestDecodeValidationCalldata_Errors(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		if _, err := DecodeValidationResponseCalldata([]byte{0x3d, 0x65}); err == nil {
			t.Error("expected error for short calldata")
		}
	})

	t.Run("wrong selector", func(t *testing.T) {
		// validationRequest calldata fed to the validationResponse decoder.
		data, err := EncodeValidationRequest(
			common.HexToAddress("0x2222222222222222222222222222222222222222"),
			big.NewInt(7), "u", crypto.Keccak256Hash([]byte("y")))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeValidationResponseCalldata(data); err == nil {
			t.Error("expected selector mismatch error")
		} else if !strings.Contains(err.Error(), "selector mismatch") {
			t.Errorf("error = %v, want selector mismatch", err)
		}
	})

	t.Run("truncated args", func(t *testing.T) {
		data, err := EncodeValidationResponse(crypto.Keccak256Hash([]byte("z")), 10, "uri", common.Hash{}, "tag")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeValidationResponseCalldata(data[:len(data)-40]); err == nil {
			t.Error("expected error for truncated calldata")
		}
	})
}

func TestValidationRegistryAddress(t *testing.T) {
	tests := []struct {
		network string
		want    string
		wantErr bool
	}{
		{"base-sepolia", ValidationRegistryV2BaseSepolia, false},
		{" Base-Sepolia ", ValidationRegistryV2BaseSepolia, false},
		{"base", ValidationRegistryV2Mainnet, false},
		{"base-mainnet", ValidationRegistryV2Mainnet, false},
		{"ethereum", ValidationRegistryV2Mainnet, false},
		{"mainnet", ValidationRegistryV2Mainnet, false},
		{"solana", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.network, func(t *testing.T) {
			got, err := ValidationRegistryAddress(tt.network)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got address %s", tt.network, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidationRegistryAddress(%q): %v", tt.network, err)
			}
			if got != tt.want {
				t.Errorf("address = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNewValidationReader_BadInput(t *testing.T) {
	if _, err := NewValidationReader(nil, ValidationRegistryV2BaseSepolia); err == nil {
		t.Error("nil caller: expected error")
	}
	if _, err := NewValidationReader(&stubCaller{}, "not-an-address"); err == nil {
		t.Error("bad address: expected error")
	}
}

func TestValidationReader_ValidationStatus(t *testing.T) {
	parsed, err := validationABI()
	if err != nil {
		t.Fatal(err)
	}

	validator := common.HexToAddress("0x3333333333333333333333333333333333333333")
	agentID := big.NewInt(42)
	respHash := crypto.Keccak256Hash([]byte("artifact"))
	lastUpdate := big.NewInt(1765432100)

	ret, err := parsed.Methods["getValidationStatus"].Outputs.Pack(
		validator, agentID, uint8(91), [32]byte(respHash), "code-review", lastUpdate)
	if err != nil {
		t.Fatalf("pack outputs: %v", err)
	}

	caller := &stubCaller{ret: ret}
	reader, err := NewValidationReader(caller, ValidationRegistryV2BaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	reqHash := crypto.Keccak256Hash([]byte("request"))
	status, err := reader.ValidationStatus(context.Background(), reqHash)
	if err != nil {
		t.Fatalf("ValidationStatus: %v", err)
	}

	if status.ValidatorAddress != validator {
		t.Errorf("validatorAddress = %s, want %s", status.ValidatorAddress, validator)
	}
	if status.AgentID.Cmp(agentID) != 0 {
		t.Errorf("agentId = %s, want %s", status.AgentID, agentID)
	}
	if status.Response != 91 {
		t.Errorf("response = %d, want 91", status.Response)
	}
	if status.ResponseHash != respHash {
		t.Errorf("responseHash = %s, want %s", status.ResponseHash, respHash)
	}
	if status.Tag != "code-review" {
		t.Errorf("tag = %q, want %q", status.Tag, "code-review")
	}
	if status.LastUpdate.Cmp(lastUpdate) != 0 {
		t.Errorf("lastUpdate = %s, want %s", status.LastUpdate, lastUpdate)
	}

	// The reader must have issued a getValidationStatus(requestHash) call.
	wantData, err := parsed.Pack("getValidationStatus", reqHash)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(caller.lastCall.Data) != hex.EncodeToString(wantData) {
		t.Errorf("call data = 0x%x, want 0x%x", caller.lastCall.Data, wantData)
	}
}

func TestValidationReader_Summary(t *testing.T) {
	parsed, err := validationABI()
	if err != nil {
		t.Fatal(err)
	}
	ret, err := parsed.Methods["getSummary"].Outputs.Pack(uint64(5), uint8(78))
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewValidationReader(&stubCaller{ret: ret}, ValidationRegistryV2BaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	count, avg, err := reader.Summary(context.Background(), big.NewInt(42), nil, "")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if count != 5 || avg != 78 {
		t.Errorf("summary = (%d, %d), want (5, 78)", count, avg)
	}

	if _, _, err := reader.Summary(context.Background(), nil, nil, ""); err == nil {
		t.Error("nil agentId: expected error")
	}
}

func TestValidationReader_AgentValidations(t *testing.T) {
	parsed, err := validationABI()
	if err != nil {
		t.Fatal(err)
	}
	h1 := crypto.Keccak256Hash([]byte("a"))
	h2 := crypto.Keccak256Hash([]byte("b"))
	ret, err := parsed.Methods["getAgentValidations"].Outputs.Pack([][32]byte{h1, h2})
	if err != nil {
		t.Fatal(err)
	}

	reader, err := NewValidationReader(&stubCaller{ret: ret}, ValidationRegistryV2BaseSepolia)
	if err != nil {
		t.Fatal(err)
	}

	hashes, err := reader.AgentValidations(context.Background(), big.NewInt(42))
	if err != nil {
		t.Fatalf("AgentValidations: %v", err)
	}
	if len(hashes) != 2 || hashes[0] != h1 || hashes[1] != h2 {
		t.Errorf("hashes = %v, want [%s %s]", hashes, h1, h2)
	}
}
