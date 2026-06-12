package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/skillpkg"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

func TestSellCommand_IncludesSkillSubcommand(t *testing.T) {
	cfg := newTestConfig(t)
	if c := findSubcommand(t, sellCommand(cfg), "skill"); c.ArgsUsage != "<name>" {
		t.Errorf("sell skill ArgsUsage = %q, want <name>", c.ArgsUsage)
	}
}

func TestSellSkill_Flags(t *testing.T) {
	cfg := newTestConfig(t)
	skill := findSubcommand(t, sellCommand(cfg), "skill")
	flags := flagMap(skill)

	requireFlags(t, flags,
		"from", "from-embedded", "skill-name", "skill-version",
		"display-name", "description",
		"pay-to", "chain", "token", "price", "per-request",
		"path", "max-timeout", "namespace",
		"no-register", "register-name",
		"as-service", "agent",
	)

	// Payment flag set mirrors sell http.
	assertStringDefault(t, flags, "chain", "base")
	assertStringDefault(t, flags, "token", "USDC")
	assertStringDefault(t, flags, "namespace", "default")
	assertIntDefault(t, flags, "max-timeout", 300)
	assertFlagHasAlias(t, flags, "pay-to", "wallet")
	assertFlagHasAlias(t, flags, "namespace", "n")

	assertFlagRequired(t, flags, "skill-version")

	// Skills are per-request only in v0 — no per-mtok/per-hour.
	for _, name := range []string{"per-mtok", "per-hour"} {
		if _, ok := flags[name]; ok {
			t.Errorf("flag --%s must not exist on sell skill (per-request pricing only)", name)
		}
	}
}

func TestValidateSkillSourceFlags(t *testing.T) {
	tests := []struct {
		name         string
		from         string
		fromEmbedded string
		wantErr      string
	}{
		{name: "from only", from: "./skills/x", fromEmbedded: ""},
		{name: "embedded only", from: "", fromEmbedded: "buy-x402"},
		{name: "both", from: "./skills/x", fromEmbedded: "buy-x402", wantErr: "mutually exclusive"},
		{name: "neither", from: "", fromEmbedded: "", wantErr: "bundle source required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSkillSourceFlags(tt.from, tt.fromEmbedded)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestMaterializeEmbeddedSkill_PacksDeterministically exercises the
// --from-embedded path end to end: materialize a real embedded skill
// twice and prove the two packs hash identically (both source modes
// share one normalization).
func TestMaterializeEmbeddedSkill_PacksDeterministically(t *testing.T) {
	names, err := embed.GetEmbeddedSkillNames()
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded skills available: %v", err)
	}
	name := names[0]

	pack := func() string {
		dir, cleanup, err := materializeEmbeddedSkill(name)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Fatalf("materialized skill %s missing SKILL.md: %v", name, err)
		}
		_, hash, err := skillpkg.Pack(os.DirFS(dir))
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}

	if h1, h2 := pack(), pack(); h1 != h2 {
		t.Errorf("two materializations hash differently: %s vs %s", h1, h2)
	}
}

func TestMaterializeEmbeddedSkill_UnknownName(t *testing.T) {
	_, _, err := materializeEmbeddedSkill("definitely-not-a-skill")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found listing available skills", err)
	}
}

func TestSkillBundleConfigMapName(t *testing.T) {
	if got := skillBundleConfigMapName("quant-notes"); got != "quant-notes-skill-bundle" {
		t.Fatalf("skillBundleConfigMapName = %q, want quant-notes-skill-bundle", got)
	}
}

func TestBuildSkillBundleConfigMapManifest(t *testing.T) {
	gz := []byte{0x1f, 0x8b, 0x08, 0x00}
	m := buildSkillBundleConfigMapManifest("quant-skill-bundle", "default", gz)

	if m["kind"] != "ConfigMap" || m["apiVersion"] != "v1" {
		t.Fatalf("unexpected kind/apiVersion: %v/%v", m["kind"], m["apiVersion"])
	}
	md := m["metadata"].(map[string]any)
	if md["name"] != "quant-skill-bundle" || md["namespace"] != "default" {
		t.Errorf("metadata = %v", md)
	}
	bd := m["binaryData"].(map[string]any)
	enc, ok := bd[monetizeapi.SkillBundleKey].(string)
	if !ok {
		t.Fatalf("binaryData missing key %q", monetizeapi.SkillBundleKey)
	}
	dec, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || string(dec) != string(gz) {
		t.Errorf("binaryData does not base64 round-trip: %v", err)
	}
}

func TestBuildSkillShareOfferManifest(t *testing.T) {
	hash := strings.Repeat("AB", 32) // uppercase in, lowercase out
	in := skillShareOfferInputs{
		OfferName:       "quant-notes",
		Namespace:       "default",
		SkillName:       "quant-notes",
		Version:         "0.1.0",
		SHA256:          hash,
		BundleConfigMap: "quant-notes-skill-bundle",
		DisplayName:     "Quant Notes",
		Description:     "daily quant notes skill",
		PayTo:           "0x1111111111111111111111111111111111111111",
		Chain:           "base-sepolia",
		Price:           "0.25",
		MaxTimeout:      300,
		Registration: map[string]any{
			"enabled": true,
			"skills":  []string{"quant-notes"},
			"metadata": map[string]string{
				"skillName":    "quant-notes",
				"skillVersion": "0.1.0",
				"skillSha256":  strings.ToLower(hash),
			},
		},
	}
	m := buildSkillShareOfferManifest(in)

	spec := m["spec"].(map[string]any)
	if spec["type"] != "skill" {
		t.Fatalf("spec.type = %v, want skill", spec["type"])
	}

	skill := spec["skill"].(map[string]any)
	if skill["name"] != "quant-notes" || skill["version"] != "0.1.0" {
		t.Errorf("skill identity = %v", skill)
	}
	if skill["sha256"] != strings.ToLower(hash) {
		t.Errorf("sha256 = %v, want lowercase %s (CRD pattern is lowercase-only)", skill["sha256"], strings.ToLower(hash))
	}
	if skill["bundleConfigMap"] != "quant-notes-skill-bundle" {
		t.Errorf("bundleConfigMap = %v", skill["bundleConfigMap"])
	}
	if skill["displayName"] != "Quant Notes" || skill["description"] != "daily quant notes skill" {
		t.Errorf("display fields = %v", skill)
	}

	// Upstream is pinned to the controller's deterministic bundle-server
	// name — the anti-spoof invariant the controller enforces.
	up := spec["upstream"].(map[string]any)
	if up["service"] != monetizeapi.SkillBundleWorkloadName("quant-notes") {
		t.Errorf("upstream.service = %v, want %s", up["service"], monetizeapi.SkillBundleWorkloadName("quant-notes"))
	}
	if up["namespace"] != "default" || up["port"] != 8080 || up["healthPath"] != "/skill.json" {
		t.Errorf("upstream = %v", up)
	}

	pay := spec["payment"].(map[string]any)
	if pay["network"] != "base-sepolia" || pay["payTo"] != in.PayTo {
		t.Errorf("payment = %v", pay)
	}
	if price := pay["price"].(map[string]any); price["perRequest"] != "0.25" {
		t.Errorf("price = %v", price)
	}
	if _, hasPath := spec["path"]; hasPath {
		t.Error("spec.path must be omitted when unset")
	}
	if _, hasReg := spec["registration"]; !hasReg {
		t.Error("spec.registration missing")
	}

	// No-registration variant omits the block entirely.
	in.Registration = nil
	in.Path = "/services/custom"
	m2 := buildSkillShareOfferManifest(in)
	spec2 := m2["spec"].(map[string]any)
	if _, hasReg := spec2["registration"]; hasReg {
		t.Error("spec.registration must be omitted when nil")
	}
	if spec2["path"] != "/services/custom" {
		t.Errorf("spec.path = %v", spec2["path"])
	}
}

func TestBuildSkillShareOfferManifest_AssetTerms(t *testing.T) {
	in := skillShareOfferInputs{
		OfferName: "x", Namespace: "default", SkillName: "x", Version: "1",
		SHA256: strings.Repeat("a", 64), BundleConfigMap: "x-skill-bundle",
		PayTo: "0x1111111111111111111111111111111111111111", Chain: "ethereum",
		Price: "10", MaxTimeout: 300,
		AssetTerms: schemas.AssetTerms{Address: "0xdead", Symbol: "OBOL", Decimals: 18},
	}
	spec := buildSkillShareOfferManifest(in)["spec"].(map[string]any)
	if _, ok := spec["payment"].(map[string]any)["asset"]; !ok {
		t.Error("payment.asset missing for non-default token")
	}
}

func TestBuildSkillServiceOfferManifest(t *testing.T) {
	agent := &agentRefForSale{
		Name:      "quant",
		Namespace: "agent-quant",
		Runtime:   monetizeapi.AgentRuntimeHermes,
		Model:     "qwen3.5:9b",
		Skills:    []string{"quant-notes", "monetize"},
	}
	m := buildSkillServiceOfferManifest(skillServiceOfferInputs{
		OfferName:  "quant-svc",
		Agent:      agent,
		SkillName:  "quant-notes",
		Version:    "0.1.0",
		PayTo:      "0x2222222222222222222222222222222222222222",
		Chain:      "base",
		Price:      "0.01",
		Symbol:     "USDC",
		MaxTimeout: 300,
		Register:   true,
		RegName:    "quant-svc",
		RegDesc:    "serves quant notes",
	})

	md := m["metadata"].(map[string]any)
	if md["namespace"] != "agent-quant" {
		t.Errorf("offer must land in the agent's namespace, got %v", md["namespace"])
	}

	spec := m["spec"].(map[string]any)
	if spec["type"] != "agent" {
		t.Fatalf("spec.type = %v, want agent (SERVICE mode is sugar over type=agent)", spec["type"])
	}
	if _, hasSkill := spec["skill"]; hasSkill {
		t.Error("type=agent offers must not carry a spec.skill block")
	}

	ref := spec["agent"].(map[string]any)["ref"].(map[string]any)
	if ref["name"] != "quant" || ref["namespace"] != "agent-quant" {
		t.Errorf("agent.ref = %v", ref)
	}

	reg := spec["registration"].(map[string]any)
	if reg["enabled"] != true {
		t.Errorf("registration.enabled = %v", reg["enabled"])
	}
	skills := reg["skills"].([]any)
	if len(skills) != 2 || skills[0] != "quant-notes" || skills[1] != "monetize" {
		t.Errorf("registration.skills must keep the agent's full list, got %v", skills)
	}
	meta := reg["metadata"].(map[string]string)
	if meta["skillName"] != "quant-notes" || meta["skillVersion"] != "0.1.0" {
		t.Errorf("registration.metadata missing skill identity: %v", meta)
	}
	if meta["runtime"] != monetizeapi.AgentRuntimeHermes || meta["model"] != "qwen3.5:9b" {
		t.Errorf("agent metadata extras lost: %v", meta)
	}
	if spec["path"] != "/services/quant-svc" {
		t.Errorf("spec.path = %v", spec["path"])
	}
}

func TestSkillOfferBundle_ShapeAndType(t *testing.T) {
	cm := buildSkillBundleConfigMapManifest("x-skill-bundle", "default", []byte("gz"))
	offer := buildSkillShareOfferManifest(skillShareOfferInputs{
		OfferName: "x", Namespace: "default", SkillName: "x", Version: "1",
		SHA256: strings.Repeat("a", 64), BundleConfigMap: "x-skill-bundle",
		PayTo: "0x1111111111111111111111111111111111111111", Chain: "base",
		Price: "0.1", MaxTimeout: 300,
	})
	bundle := skillOfferBundle("default", "x", cm, offer)

	if bundle["kind"] != "List" {
		t.Fatalf("bundle kind = %v, want List", bundle["kind"])
	}
	items := bundle["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].(map[string]any)["kind"] != "ConfigMap" {
		t.Error("first item must be the bundle ConfigMap (replayed before the offer)")
	}
	if items[1].(map[string]any)["kind"] != "ServiceOffer" {
		t.Error("second item must be the ServiceOffer")
	}

	// The resume ledger reports the inner offer's type for List bundles.
	if got := manifestOfferType(bundle); got != "skill" {
		t.Errorf("manifestOfferType = %q, want skill", got)
	}
	if ns, name := manifestNSName(bundle); ns != "default" || name != "x" {
		t.Errorf("manifestNSName = (%q, %q)", ns, name)
	}
}

// TestSellSkill_RequiredFlagEnforced runs the command without
// --skill-version and expects urfave/cli's required-flag error before
// the action runs (no cluster involved).
func TestSellSkill_RequiredFlagEnforced(t *testing.T) {
	cfg := newTestConfig(t)
	root := &cli.Command{Commands: []*cli.Command{sellCommand(cfg)}}
	err := root.Run(t.Context(), []string{"obol", "sell", "skill", "x", "--from", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "skill-version") {
		t.Fatalf("err = %v, want required-flag error naming skill-version", err)
	}
}

// TestPrintSkillPurchaseInstructions_BinarySafe pins the buyer-facing
// copy: buy.py pay is text-only (diagnostics before the body, lossy
// decode), so the printed instructions must point it at /skill.json and
// must never tell buyers to redirect it into the bundle file.
func TestPrintSkillPurchaseInstructions_BinarySafe(t *testing.T) {
	var out, errOut bytes.Buffer
	u := ui.NewForTest(&out, &errOut)

	printSkillPurchaseInstructions(u, "https://x.example.com", "/services/gas-skill",
		"gas", "0.1.0", "base-sepolia", strings.Repeat("a", 64))
	got := out.String() + errOut.String()

	if strings.Contains(got, "buy.py pay https://x.example.com/services/gas-skill/bundle.tar.gz") {
		t.Error("instructions must not run buy.py pay against bundle.tar.gz (text-only, corrupts gzip bytes)")
	}
	if strings.Contains(got, "> gas-0.1.0.tar.gz") {
		t.Error("instructions must not redirect buy.py pay stdout into the bundle file")
	}
	for _, want := range []string{
		"buy.py pay https://x.example.com/services/gas-skill/skill.json",
		"binary-safe x402 client",
		"obol skills verify gas-0.1.0.tar.gz --agent-id <seller-agent-id> --skill gas@0.1.0 --chain base-sepolia",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing %q\noutput:\n%s", want, got)
		}
	}
}
