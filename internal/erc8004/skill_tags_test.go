package erc8004

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSkillTag1_Constant(t *testing.T) {
	// ERC-8239 draft "asr" tag1 — changing this forks the rating
	// namespace for every previously submitted skill feedback entry.
	if SkillTag1 != "asr:skill" {
		t.Fatalf("SkillTag1 = %q, want %q", SkillTag1, "asr:skill")
	}
}

func TestSkillRef(t *testing.T) {
	tests := []struct {
		name    string
		skill   string
		version string
		want    string
		wantErr string
	}{
		{name: "ok", skill: "buy-x402", version: "0.1.0", want: "buy-x402@0.1.0"},
		{name: "ok with prerelease", skill: "monetize", version: "1.0.0-rc1", want: "monetize@1.0.0-rc1"},
		{name: "empty name", skill: "", version: "0.1.0", wantErr: "must not be empty"},
		{name: "empty version", skill: "buy-x402", version: "", wantErr: "must not be empty"},
		{name: "colon in name", skill: "buy:x402", version: "0.1.0", wantErr: "must not contain"},
		{name: "colon in version", skill: "buy-x402", version: "0:1", wantErr: "must not contain"},
		{name: "at in name", skill: "buy@x402", version: "0.1.0", wantErr: "must not contain"},
		{name: "at in version", skill: "buy-x402", version: "0@1", wantErr: "must not contain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SkillRef(tt.skill, tt.version)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("SkillRef = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSkillRef(t *testing.T) {
	tests := []struct {
		ref         string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		{ref: "buy-x402@0.1.0", wantName: "buy-x402", wantVersion: "0.1.0"},
		{ref: " buy-x402@0.1.0 ", wantName: "buy-x402", wantVersion: "0.1.0"},
		{ref: "buy-x402", wantErr: true},
		{ref: "@0.1.0", wantErr: true},
		{ref: "buy-x402@", wantErr: true},
		{ref: "a@b@c", wantErr: true}, // version part keeps the second '@'
		{ref: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			name, version, err := ParseSkillRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSkillRef(%q) = (%q, %q), want error", tt.ref, name, version)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != tt.wantName || version != tt.wantVersion {
				t.Errorf("ParseSkillRef(%q) = (%q, %q), want (%q, %q)", tt.ref, name, version, tt.wantName, tt.wantVersion)
			}
		})
	}
}

// TestSkillTag2_Golden pins the documented obol interim form of the
// ERC-8239 draft (PR #1704) tag2:
// eip155:<chainId>:<lowercase registry>:<agentId decimal>:<name>@<version>.
func TestSkillTag2_Golden(t *testing.T) {
	tests := []struct {
		name    string
		net     NetworkConfig
		agentID *big.Int
		ref     string
		want    string
	}{
		{
			name:    "base-sepolia",
			net:     BaseSepolia,
			agentID: big.NewInt(42),
			ref:     "buy-x402@0.1.0",
			want:    "eip155:84532:0x8004a818bfb912233c491871b3d84c89a494bd9e:42:buy-x402@0.1.0",
		},
		{
			name:    "base mainnet",
			net:     Base,
			agentID: big.NewInt(7),
			ref:     "monetize@1.2.3",
			want:    "eip155:8453:0x8004a169fb4a3325136eb29fa0ceb6d2e539a432:7:monetize@1.2.3",
		},
		{
			name:    "ethereum mainnet",
			net:     Ethereum,
			agentID: big.NewInt(1001),
			ref:     "quant@0.0.1",
			want:    "eip155:1:0x8004a169fb4a3325136eb29fa0ceb6d2e539a432:1001:quant@0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SkillTag2(tt.net, tt.agentID, tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("SkillTag2 = %q, want %q", got, tt.want)
			}
			// The registry segment must be lowercase: tags are
			// exact-match strings on-chain.
			if got != strings.ToLower(got) {
				t.Errorf("SkillTag2 = %q contains uppercase", got)
			}
		})
	}
}

func TestSkillTag2_BadInput(t *testing.T) {
	tests := []struct {
		name    string
		agentID *big.Int
		ref     string
	}{
		{name: "nil agent id", agentID: nil, ref: "buy-x402@0.1.0"},
		{name: "negative agent id", agentID: big.NewInt(-1), ref: "buy-x402@0.1.0"},
		{name: "ref without version", agentID: big.NewInt(1), ref: "buy-x402"},
		{name: "empty ref", agentID: big.NewInt(1), ref: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := SkillTag2(BaseSepolia, tt.agentID, tt.ref); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestSkillHashMetadataKey(t *testing.T) {
	if got := SkillHashMetadataKey("buy-x402@0.1.0"); got != "skill.sha256:buy-x402@0.1.0" {
		t.Fatalf("SkillHashMetadataKey = %q, want %q", got, "skill.sha256:buy-x402@0.1.0")
	}
}

// TestEncodeSetMetadata_Golden pins the exact calldata for fixed inputs
// and cross-checks the 4-byte selector against keccak256 of the
// canonical signature.
func TestEncodeSetMetadata_Golden(t *testing.T) {
	const (
		wantSelector = "466648da" // keccak256("setMetadata(uint256,string,bytes)")[:4]
		wantCalldata = "466648da" +
			"000000000000000000000000000000000000000000000000000000000000002a" +
			"0000000000000000000000000000000000000000000000000000000000000060" +
			"00000000000000000000000000000000000000000000000000000000000000a0" +
			"000000000000000000000000000000000000000000000000000000000000001b" +
			"736b696c6c2e7368613235363a6275792d7834303240302e312e300000000000" +
			"0000000000000000000000000000000000000000000000000000000000000040" +
			"3966383664303831383834633764363539613266656161306335356164303135" +
			"6133626634663162326230623832326364313564366331356230663030613038"
	)

	if got := hex.EncodeToString(crypto.Keccak256([]byte("setMetadata(uint256,string,bytes)"))[:4]); got != wantSelector {
		t.Fatalf("keccak selector = %s, want %s", got, wantSelector)
	}

	hash := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	data, err := EncodeSetMetadata(big.NewInt(42), SkillHashMetadataKey("buy-x402@0.1.0"), []byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(data[:4]); got != wantSelector {
		t.Errorf("calldata selector = %s, want %s", got, wantSelector)
	}
	if got := hex.EncodeToString(data); got != wantCalldata {
		t.Errorf("calldata = %s\nwant       %s", got, wantCalldata)
	}
}

func TestEncodeSetMetadata_RoundTrip(t *testing.T) {
	agentID := big.NewInt(123456)
	key := SkillHashMetadataKey("monetize@2.0.0")
	value := []byte(strings.Repeat("ab", 32))

	data, err := EncodeSetMetadata(agentID, key, value)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeSetMetadataCalldata(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AgentID.Cmp(agentID) != 0 {
		t.Errorf("agentID = %s, want %s", decoded.AgentID, agentID)
	}
	if decoded.Key != key {
		t.Errorf("key = %q, want %q", decoded.Key, key)
	}
	if !bytes.Equal(decoded.Value, value) {
		t.Errorf("value = %x, want %x", decoded.Value, value)
	}
}

func TestEncodeSetMetadata_BadInput(t *testing.T) {
	tests := []struct {
		name    string
		agentID *big.Int
		key     string
	}{
		{name: "nil agent id", agentID: nil, key: "skill.sha256:a@1"},
		{name: "negative agent id", agentID: big.NewInt(-5), key: "skill.sha256:a@1"},
		{name: "empty key", agentID: big.NewInt(1), key: ""},
		{name: "whitespace key", agentID: big.NewInt(1), key: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := EncodeSetMetadata(tt.agentID, tt.key, []byte("x")); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestDecodeSetMetadataCalldata_Errors(t *testing.T) {
	// Wrong selector (giveFeedback's) must be rejected.
	wrong, err := EncodeGiveFeedback(big.NewInt(1), big.NewInt(1), 0, "", "", "", "", common.Hash{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSetMetadataCalldata(wrong); err == nil {
		t.Fatal("expected selector mismatch error, got nil")
	}
	if _, err := DecodeSetMetadataCalldata([]byte{0x01}); err == nil {
		t.Fatal("expected too-short error, got nil")
	}
}

// TestEncodeGiveFeedback_SkillTags_Golden pins the full calldata of a
// skill rating: tag1="asr:skill", tag2 in the documented interim
// ERC-8239 form, score 95/100 with no fixed-point scaling.
func TestEncodeGiveFeedback_SkillTags_Golden(t *testing.T) {
	const wantCalldata = "3c036a7e000000000000000000000000000000000000000000000000000000000000002a000000000000000000000000000000000000000000000000000000000000005f00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000001c000000000000000000000000000000000000000000000000000000000000001e0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000096173723a736b696c6c000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000496569703135353a38343533323a3078383030346138313862666239313232333363343931383731623364383463383961343934626439653a34323a6275792d7834303240302e312e30000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

	tag2, err := SkillTag2(BaseSepolia, big.NewInt(42), "buy-x402@0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeGiveFeedback(big.NewInt(42), big.NewInt(95), 0, SkillTag1, tag2, "", "", common.Hash{})
	if err != nil {
		t.Fatal(err)
	}

	if got := hex.EncodeToString(data[:4]); got != "3c036a7e" {
		t.Errorf("selector = %s, want 3c036a7e (giveFeedback)", got)
	}
	if got := hex.EncodeToString(data); got != wantCalldata {
		t.Errorf("calldata mismatch:\n got %s\nwant %s", got, wantCalldata)
	}

	// And it must decode back to the skill-tag pair.
	decoded, err := DecodeGiveFeedbackCalldata(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Tag1 != SkillTag1 {
		t.Errorf("tag1 = %q, want %q", decoded.Tag1, SkillTag1)
	}
	if decoded.Tag2 != tag2 {
		t.Errorf("tag2 = %q, want %q", decoded.Tag2, tag2)
	}
	if decoded.Value.Int64() != 95 {
		t.Errorf("value = %s, want 95", decoded.Value)
	}
}
