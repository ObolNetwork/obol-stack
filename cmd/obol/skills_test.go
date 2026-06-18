package main

import (
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func testSkillsCommand(t *testing.T) *cli.Command {
	t.Helper()
	return skillsCommand(newTestConfig(t))
}

// assertInt64FlagRequired covers *cli.Int64Flag, which the shared
// assertFlagRequired helper doesn't (ERC-8004 tokenIds exceed int32).
func assertInt64FlagRequired(t *testing.T, flags map[string]cli.Flag, name string) {
	t.Helper()
	f, ok := flags[name].(*cli.Int64Flag)
	if !ok {
		t.Fatalf("flag --%s is %T, want *cli.Int64Flag", name, flags[name])
	}
	if !f.Required {
		t.Errorf("flag --%s should be required", name)
	}
}

func TestSkillsCommand_Structure(t *testing.T) {
	cmd := testSkillsCommand(t)
	if cmd.Name != "skills" {
		t.Fatalf("command name = %q, want skills", cmd.Name)
	}

	calldata := findSubcommand(t, cmd, "calldata")
	findSubcommand(t, calldata, "set-hash")
	findSubcommand(t, calldata, "feedback")
	findSubcommand(t, cmd, "reputation")
	findSubcommand(t, cmd, "verify")
}

func TestSkillsCalldataSetHash_Flags(t *testing.T) {
	calldata := findSubcommand(t, testSkillsCommand(t), "calldata")
	setHash := findSubcommand(t, calldata, "set-hash")
	flags := flagMap(setHash)

	requireFlags(t, flags, "agent-id", "chain", "skill", "hash", "from-bundle")
	assertStringDefault(t, flags, "chain", "base")
	assertFlagHasAlias(t, flags, "from-bundle", "bundle")
	assertInt64FlagRequired(t, flags, "agent-id")
}

func TestSkillsCalldataFeedback_Flags(t *testing.T) {
	calldata := findSubcommand(t, testSkillsCommand(t), "calldata")
	feedback := findSubcommand(t, calldata, "feedback")
	flags := flagMap(feedback)

	requireFlags(t, flags, "agent-id", "value", "chain", "skill", "endpoint", "feedback-uri", "feedback-hash")
	assertStringDefault(t, flags, "chain", "base")
	assertFlagHasAlias(t, flags, "feedback-uri", "uri")
	assertFlagHasAlias(t, flags, "feedback-hash", "hash")
	assertInt64FlagRequired(t, flags, "agent-id")
	assertFlagRequired(t, flags, "value")
}

func TestSkillsReputation_Flags(t *testing.T) {
	reputation := findSubcommand(t, testSkillsCommand(t), "reputation")
	flags := flagMap(reputation)

	requireFlags(t, flags, "agent-id", "chain", "skill", "raters")
	assertStringDefault(t, flags, "chain", "base")
	assertInt64FlagRequired(t, flags, "agent-id")

	if _, ok := flags["raters"].(*cli.StringSliceFlag); !ok {
		t.Errorf("flag --raters is %T, want *cli.StringSliceFlag", flags["raters"])
	}
}

func TestSkillsVerify_Flags(t *testing.T) {
	verify := findSubcommand(t, testSkillsCommand(t), "verify")
	flags := flagMap(verify)

	requireFlags(t, flags, "agent-id", "skill", "chain")
	assertStringDefault(t, flags, "chain", "base")
	assertInt64FlagRequired(t, flags, "agent-id")
	assertFlagRequired(t, flags, "skill")
}

// TestSkillsCalldataSetHash_PrintsCalldata runs the full command (no
// chain access — calldata building is pure) and checks the printer
// output carries the registry, the calldata, and the never-signs
// trailer.
func TestSkillsCalldataSetHash_PrintsCalldata(t *testing.T) {
	out := captureStdout(t, func() error {
		root := &cli.Command{Commands: []*cli.Command{skillsCommand(newTestConfig(t))}}
		return root.Run(t.Context(), []string{
			"obol", "skills", "calldata", "set-hash", "quant-notes@0.1.0",
			"--agent-id", "42",
			"--chain", "base-sepolia",
			"--hash", "0x" + strings.Repeat("ab", 32),
		})
	})

	for _, want := range []string{
		"skill.sha256:quant-notes@0.1.0",
		"IdentityRegistry (base-sepolia): 0x8004A818BFB912233c491871b3d84c89A494BD9e",
		"Calldata: 0x466648da", // setMetadata(uint256,string,bytes) selector
		"NEVER signs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestSkillsCalldataFeedback_PrintsTagsAndCalldata(t *testing.T) {
	out := captureStdout(t, func() error {
		root := &cli.Command{Commands: []*cli.Command{skillsCommand(newTestConfig(t))}}
		return root.Run(t.Context(), []string{
			"obol", "skills", "calldata", "feedback", "quant-notes@0.1.0",
			"--agent-id", "42",
			"--value", "95",
			"--chain", "base-sepolia",
		})
	})

	for _, want := range []string{
		"tag1: asr:skill",
		"tag2: eip155:84532:0x8004a818bfb912233c491871b3d84c89a494bd9e:42:quant-notes@0.1.0",
		"ReputationRegistry (base-sepolia): 0x8004B663056A597Dffe9eCcC1965A193B7388713",
		"Calldata: 0x3c036a7e", // giveFeedback selector
		"self-feedback",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestSkillsCalldataFeedback_RejectsOutOfRangeValue(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{skillsCommand(newTestConfig(t))}}
	err := root.Run(t.Context(), []string{
		"obol", "skills", "calldata", "feedback", "x@1", "--agent-id", "1", "--value", "101",
	})
	if err == nil || !strings.Contains(err.Error(), "0-100") {
		t.Fatalf("err = %v, want 0-100 range error", err)
	}
}

func TestSkillsCalldataSetHash_HashSourceXOR(t *testing.T) {
	run := func(args ...string) error {
		root := &cli.Command{Commands: []*cli.Command{skillsCommand(newTestConfig(t))}}
		full := append([]string{"obol", "skills", "calldata", "set-hash", "x@1", "--agent-id", "1"}, args...)
		return root.Run(t.Context(), full)
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "hash source required") {
		t.Errorf("no source: err = %v, want hash-source error", err)
	}
	if err := run("--hash", strings.Repeat("ab", 32), "--from-bundle", "x.tar.gz"); err == nil ||
		!strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("both sources: err = %v, want mutual-exclusion error", err)
	}
}

// ── pure helper tests ───────────────────────────────────────────────────────

func TestParseSkillHashArg(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain", in: valid, want: valid},
		{name: "0x prefix", in: "0x" + valid, want: valid},
		{name: "uppercase normalized", in: strings.ToUpper(valid), want: valid},
		{name: "whitespace trimmed", in: "  " + valid + "\n", want: valid},
		{name: "too short", in: valid[:62], wantErr: true},
		{name: "non-hex", in: strings.Repeat("zz", 32), wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSkillHashArg(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSkillHashArg(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("parseSkillHashArg(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSkillHashMatches(t *testing.T) {
	local := strings.Repeat("ab", 32)
	tests := []struct {
		name    string
		onChain string
		want    bool
	}{
		{name: "exact", onChain: local, want: true},
		{name: "0x prefixed on chain", onChain: "0x" + local, want: true},
		{name: "uppercase on chain", onChain: strings.ToUpper(local), want: true},
		{name: "whitespace on chain", onChain: " " + local + "\n", want: true},
		{name: "mismatch", onChain: strings.Repeat("cd", 32), want: false},
		{name: "empty", onChain: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skillHashMatches([]byte(tt.onChain), local); got != tt.want {
				t.Errorf("skillHashMatches(%q) = %v, want %v", tt.onChain, got, tt.want)
			}
		})
	}
}

func TestSkillScoreString(t *testing.T) {
	tests := []struct {
		name     string
		value    *big.Int
		decimals uint8
		want     string
	}{
		{name: "no scaling", value: big.NewInt(95), decimals: 0, want: "95"},
		{name: "two decimals", value: big.NewInt(9550), decimals: 2, want: "95.50"},
		{name: "zero", value: big.NewInt(0), decimals: 0, want: "0"},
		{name: "nil", value: nil, decimals: 2, want: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skillScoreString(tt.value, tt.decimals); got != tt.want {
				t.Errorf("skillScoreString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRaterAddresses(t *testing.T) {
	addrs, err := parseRaterAddresses([]string{
		"0x1111111111111111111111111111111111111111",
		" 0x2222222222222222222222222222222222222222 ",
		"",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 2 {
		t.Fatalf("len = %d, want 2 (empty entries skipped)", len(addrs))
	}

	if _, err := parseRaterAddresses([]string{"not-an-address"}); err == nil {
		t.Error("invalid address should error")
	}
}

func TestSha256File(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	// Well-known sha256("abc").
	if got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha256File = %s", got)
	}

	if _, err := sha256File(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file should error")
	}
}

// captureStdout redirects os.Stdout around fn — the calldata printers
// write with fmt.Printf, mirroring bountyFeedbackCommand.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	runErr := fn()

	_ = w.Close()
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for {
		n, readErr := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if readErr != nil {
			break
		}
	}
	os.Stdout = old

	if runErr != nil {
		t.Fatalf("command failed: %v", runErr)
	}
	return string(buf)
}
