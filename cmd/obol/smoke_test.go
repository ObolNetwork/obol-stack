package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/urfave/cli/v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Command structure (house style: sell_test.go)
// ─────────────────────────────────────────────────────────────────────────────

func testSmokeCommand(t *testing.T) *cli.Command {
	t.Helper()
	return smokeCommand(&config.Config{})
}

func TestSmokeCalldataCommand_Flags(t *testing.T) {
	calldata := findSubcommand(t, testSmokeCommand(t), "calldata")
	flags := flagMap(calldata)

	requireFlags(t, flags, "target", "run-id", "request-hash", "response", "response-uri", "response-hash", "tag", "network")
	assertFlagRequired(t, flags, "target")
	assertFlagRequired(t, flags, "run-id")
	assertFlagRequired(t, flags, "response")
	assertStringDefault(t, flags, "network", "base-sepolia")
	assertStringDefault(t, flags, "tag", "obol/smoke-test/v1")

	// --request-hash is an optional OVERRIDE (mirrors bounty eval calldata):
	// the default derivation comes from --target/--run-id.
	if f, ok := flags["request-hash"].(*cli.StringFlag); !ok || f.Required {
		t.Errorf("--request-hash must be an optional override (derive via --target/--run-id), got required=%v", ok && f.Required)
	}
	if f, ok := flags["response-hash"].(*cli.StringFlag); !ok || f.Required {
		t.Errorf("--response-hash must be optional (zero responseHash is allowed), got required=%v", ok && f.Required)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Golden calldata
// ─────────────────────────────────────────────────────────────────────────────

// TestBuildSmokeCalldata_Golden pins the full validationResponse calldata for
// fixed inputs: the 4-byte selector (validationResponse(bytes32,uint8,string,
// bytes32,string) == 0x3d659a96), the derived request hash (the erc8004
// smoke golden vector), and the exact ABI-encoded bytes. Any drift here
// changes what operators submit on-chain, so the hex is hardcoded.
func TestBuildSmokeCalldata_Golden(t *testing.T) {
	const (
		target       = "http://obol.stack:8080"
		runID        = "20260101T000000Z-ab12cd"
		responseURI  = "https://github.com/example/obol-smoke-reports/blob/0011223344556677889900112233445566778899/reports/obol.stack-8080/20260101T000000Z-ab12cd.md"
		responseHash = "0x9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

		goldenRequestHash = "0x2a28aa12a52a28414de4933bbe8d1e52e42828ba08006748f544596823ce7a57"
		goldenSelector    = "3d659a96"
		goldenCalldata    = "3d659a96" +
			"2a28aa12a52a28414de4933bbe8d1e52e42828ba08006748f544596823ce7a57" +
			"0000000000000000000000000000000000000000000000000000000000000054" +
			"00000000000000000000000000000000000000000000000000000000000000a0" +
			"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" +
			"0000000000000000000000000000000000000000000000000000000000000160" +
			"000000000000000000000000000000000000000000000000000000000000008e" +
			"68747470733a2f2f6769746875622e636f6d2f6578616d706c652f6f626f6c2d" +
			"736d6f6b652d7265706f7274732f626c6f622f303031313232333334343535363" +
			"637373838393930303131323233333434353536363737383839392f7265706f72" +
			"74732f6f626f6c2e737461636b2d383038302f3230323630313031543030303030" +
			"305a2d6162313263642e6d64000000000000000000000000000000000000" +
			"0000000000000000000000000000000000000000000000000000000000000012" +
			"6f626f6c2f736d6f6b652d746573742f76310000000000000000000000000000"
	)

	res, err := buildSmokeCalldata(smokeCalldataInput{
		Target:       target,
		RunID:        runID,
		Response:     84,
		ResponseURI:  responseURI,
		ResponseHash: responseHash,
		Tag:          "obol/smoke-test/v1",
		Network:      "base-sepolia",
	})
	if err != nil {
		t.Fatalf("buildSmokeCalldata: %v", err)
	}

	if res.RequestHash.Hex() != goldenRequestHash {
		t.Errorf("request hash = %s, want %s", res.RequestHash.Hex(), goldenRequestHash)
	}
	if res.Registry != erc8004.ValidationRegistryV2BaseSepolia {
		t.Errorf("registry = %s, want %s", res.Registry, erc8004.ValidationRegistryV2BaseSepolia)
	}

	got := hex.EncodeToString(res.Calldata)
	if !strings.HasPrefix(got, goldenSelector) {
		t.Errorf("selector = 0x%s, want 0x%s (validationResponse)", got[:8], goldenSelector)
	}
	if got != goldenCalldata {
		t.Errorf("calldata drifted:\n got 0x%s\nwant 0x%s", got, goldenCalldata)
	}

	// Round-trip through the shared decoder: every field the operator submits
	// must come back exactly.
	decoded, err := erc8004.DecodeValidationResponseCalldata(res.Calldata)
	if err != nil {
		t.Fatalf("DecodeValidationResponseCalldata: %v", err)
	}
	if decoded.RequestHash.Hex() != goldenRequestHash {
		t.Errorf("decoded request hash = %s, want %s", decoded.RequestHash.Hex(), goldenRequestHash)
	}
	if decoded.Response != 84 {
		t.Errorf("decoded response = %d, want 84", decoded.Response)
	}
	if decoded.ResponseURI != responseURI {
		t.Errorf("decoded responseURI = %q, want %q", decoded.ResponseURI, responseURI)
	}
	if decoded.ResponseHash.Hex() != responseHash {
		t.Errorf("decoded responseHash = %s, want %s", decoded.ResponseHash.Hex(), responseHash)
	}
	if decoded.Tag != "obol/smoke-test/v1" {
		t.Errorf("decoded tag = %q, want obol/smoke-test/v1", decoded.Tag)
	}
}

// TestBuildSmokeCalldata_RequestHashOverride proves --request-hash wins over
// the --target/--run-id derivation, mirroring bounty eval calldata.
func TestBuildSmokeCalldata_RequestHashOverride(t *testing.T) {
	const override = "0x1111111111111111111111111111111111111111111111111111111111111111"

	res, err := buildSmokeCalldata(smokeCalldataInput{
		Target:              "http://obol.stack:8080",
		RunID:               "20260101T000000Z-ab12cd",
		RequestHashOverride: override,
		Response:            100,
		Network:             "base-sepolia",
	})
	if err != nil {
		t.Fatalf("buildSmokeCalldata: %v", err)
	}
	if res.RequestHash.Hex() != override {
		t.Errorf("request hash = %s, want override %s", res.RequestHash.Hex(), override)
	}

	if _, err := buildSmokeCalldata(smokeCalldataInput{
		Target:              "http://obol.stack:8080",
		RunID:               "20260101T000000Z-ab12cd",
		RequestHashOverride: "0x1234",
		Response:            100,
		Network:             "base-sepolia",
	}); err == nil {
		t.Error("expected error for malformed --request-hash override")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Flag validation
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildSmokeCalldata_RejectsResponseOutOfRange(t *testing.T) {
	base := smokeCalldataInput{
		Target:  "http://obol.stack:8080",
		RunID:   "20260101T000000Z-ab12cd",
		Network: "base-sepolia",
	}

	for _, response := range []int{-1, 101, 255} {
		in := base
		in.Response = response
		if _, err := buildSmokeCalldata(in); err == nil {
			t.Errorf("response %d: expected out-of-range error (registry reverts above %d)", response, erc8004.MaxValidationResponse)
		}
	}

	// Boundary values must pass.
	for _, response := range []int{0, 100} {
		in := base
		in.Response = response
		if _, err := buildSmokeCalldata(in); err != nil {
			t.Errorf("response %d: unexpected error: %v", response, err)
		}
	}
}

func TestBuildSmokeCalldata_RejectsMalformedResponseHash(t *testing.T) {
	base := smokeCalldataInput{
		Target:   "http://obol.stack:8080",
		RunID:    "20260101T000000Z-ab12cd",
		Response: 50,
		Network:  "base-sepolia",
	}

	for _, malformed := range []string{
		"0x1234", // too short
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",     // missing 0x
		"0x9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00aZZ",   // non-hex
		"0x9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a0800", // too long
	} {
		in := base
		in.ResponseHash = malformed
		if _, err := buildSmokeCalldata(in); err == nil {
			t.Errorf("response hash %q: expected malformed-hash error", malformed)
		}
	}

	// Empty response hash is explicitly allowed (zero responseHash per spec).
	in := base
	in.ResponseHash = ""
	if _, err := buildSmokeCalldata(in); err != nil {
		t.Errorf("empty response hash should be allowed (zero hash): %v", err)
	}
}

func TestBuildSmokeCalldata_RejectsUnknownNetwork(t *testing.T) {
	if _, err := buildSmokeCalldata(smokeCalldataInput{
		Target:   "http://obol.stack:8080",
		RunID:    "20260101T000000Z-ab12cd",
		Response: 50,
		Network:  "not-a-chain",
	}); err == nil {
		t.Error("expected error for a network without a verified validation registry deployment")
	}
}
