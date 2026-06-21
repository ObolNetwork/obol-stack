package agentcrd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"quant", false},
		{"q1", false},
		{"my-agent-2", false},
		{"", true},
		{"-leading-dash", true},
		{"UPPERCASE", true},
		{"with_underscore", true},
		{"with.dot", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 63), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateName(%q) err=%v, wantErr=%v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestParseSkills(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"  ", nil, false},
		{"addresses", []string{"addresses"}, false},
		{"addresses,gas", []string{"addresses", "gas"}, false},
		{" addresses , gas ", []string{"addresses", "gas"}, false},
		{"addresses,,gas", []string{"addresses", "gas"}, false},
		{"addresses,GAS", nil, true},
		{"addresses,with_underscore", nil, true},
		{"addresses,-leading-dash", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseSkills(tc.in)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
				return
			}
			if !equalSlice(got, tc.want) {
				t.Errorf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestBuildAgent_OmitsEmpties(t *testing.T) {
	got := BuildAgent("quant", AgentOptions{Skills: []string{"addresses"}})
	spec := got["spec"].(map[string]any)

	// Required: runtime defaulted to hermes
	if spec["runtime"] != "hermes" {
		t.Errorf("runtime = %v, want hermes", spec["runtime"])
	}
	// Empties should not be present so YAML stays small + diffs clean.
	for _, k := range []string{"model", "objective", "wallet"} {
		if _, ok := spec[k]; ok {
			t.Errorf("spec.%s set despite empty input: %v", k, spec[k])
		}
	}
	// Skills must be []any so the apiserver round-trips it as a JSON array.
	if _, ok := spec["skills"].([]any); !ok {
		t.Errorf("skills wrong type: %T", spec["skills"])
	}
}

func TestBuildAgent_Populated(t *testing.T) {
	got := BuildAgent("quant", AgentOptions{
		Model:        "qwen3.5:9b",
		Skills:       []string{"addresses", "gas"},
		Objective:    "  EVM analyst.  ",
		CreateWallet: true,
	})
	spec := got["spec"].(map[string]any)

	if spec["model"] != "qwen3.5:9b" {
		t.Errorf("model = %v", spec["model"])
	}
	// Objective must be trimmed before serialisation (matches what the soul
	// renderer expects); leading/trailing whitespace would otherwise leak
	// into the rendered system prompt as visible padding.
	if spec["objective"] != "EVM analyst." {
		t.Errorf("objective = %q, want trimmed", spec["objective"])
	}
	wallet, ok := spec["wallet"].(map[string]any)
	if !ok || wallet["create"] != true {
		t.Errorf("wallet block wrong: %v", spec["wallet"])
	}

	meta := got["metadata"].(map[string]any)
	if meta["namespace"] != "agent-quant" {
		t.Errorf("namespace = %v, want agent-quant", meta["namespace"])
	}
}

func TestSeedHostFiles_FreshAgent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	wrote, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses", "gas"},
		"You are a chain analyst.",
		SeedOptions{},
	)
	if err != nil {
		t.Fatalf("SeedHostFiles: %v", err)
	}
	if !wrote {
		t.Error("expected soulWritten=true on fresh seed")
	}

	soul := HostSoulPath(cfg, "quant")
	body, err := os.ReadFile(soul)
	if err != nil {
		t.Fatalf("read SOUL.md: %v", err)
	}
	if !strings.Contains(string(body), "You are a chain analyst") {
		t.Error("rendered soul missing operator objective")
	}
	for _, skill := range []string{"addresses", "gas"} {
		skillFile := filepath.Join(HostSkillsPath(cfg, "quant"), skill, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			t.Errorf("missing %s: %v", skillFile, err)
		}
	}

	marker := HostNoBundledSkillsMarkerPath(cfg, "quant")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("no-bundled-skills marker missing: %v", err)
	}

	// A fresh seed must also drop the pay_mcp plugin into the user-plugins dir
	// so a wallet-bearing sell agent can settle paid MCP tools.
	pluginInit := filepath.Join(HostPluginsPath(cfg, "quant"), "pay_mcp", "__init__.py")
	if _, err := os.Stat(pluginInit); err != nil {
		t.Errorf("pay_mcp plugin not seeded: %v", err)
	}
}

// SeedHostPlugins seeds the embedded plugins into the agent's user-plugins dir
// and must leave a user-added plugin with a different name untouched on re-seed.
func TestSeedHostPlugins_SeedsAndPreserves(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	custom := filepath.Join(HostPluginsPath(cfg, "quant"), "operator-plugin")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "plugin.yaml"), []byte("name: operator-plugin\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SeedHostPlugins(cfg, "quant"); err != nil {
		t.Fatalf("SeedHostPlugins: %v", err)
	}

	if _, err := os.Stat(filepath.Join(HostPluginsPath(cfg, "quant"), "pay_mcp", "plugin.yaml")); err != nil {
		t.Errorf("pay_mcp not seeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "plugin.yaml")); err != nil {
		t.Errorf("operator plugin clobbered by seed: %v", err)
	}
}

// The marker must already exist on a re-seed (e.g. agent objective change) —
// SeedHostFiles is idempotent, and a missing marker would cause Hermes to
// re-seed its bundled skills on the next sync. Lock the invariant.
func TestSeedHostFiles_MarkerIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if _, err := SeedHostFiles(cfg, "quant", []string{"gas"}, "obj v1", SeedOptions{}); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	marker := HostNoBundledSkillsMarkerPath(cfg, "quant")
	stat1, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("marker missing after first seed: %v", err)
	}

	if _, err := SeedHostFiles(cfg, "quant", []string{"gas"}, "obj v2", SeedOptions{OverwriteSoul: true}); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	stat2, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("marker missing after second seed: %v", err)
	}
	// Same inode/mtime → we did not rewrite it. The marker is a presence flag,
	// not content, so touching it on every reconcile would be needless churn.
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("marker rewritten on second seed; should be left alone")
	}
}

// The `.no-bundled-skills` marker must be written ONLY by SeedHostFiles, never
// by the lower-level seed primitives (WriteSoul, embed.WriteSkillSubset) that
// the master/objective-only paths could reuse. SeedHostFiles is the
// sub-agents-for-sale seam; the master agent (internal/hermes) seeds via those
// primitives and must keep its ~80 bundled skills. If a future refactor moved
// the marker write down into a shared primitive, the master would silently lose
// them — this test fails first.
func TestMarkerOnlyWrittenBySeedHostFiles(t *testing.T) {
	// WriteSoul on its own — the objective-rewrite path — must not drop a marker.
	t.Run("WriteSoul", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &config.Config{DataDir: dir}

		if _, err := WriteSoul(cfg, "quant", "objective only", true); err != nil {
			t.Fatalf("WriteSoul: %v", err)
		}
		if _, err := os.Stat(HostNoBundledSkillsMarkerPath(cfg, "quant")); !os.IsNotExist(err) {
			t.Fatalf("WriteSoul created a .no-bundled-skills marker (err=%v); only SeedHostFiles may", err)
		}
	})

	// Writing the skill subset into the agent's skills dir — the primitive a
	// master/full-skill path would call — must not drop a marker either.
	t.Run("WriteSkillSubset", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &config.Config{DataDir: dir}

		if err := embed.WriteSkillSubset(HostSkillsPath(cfg, "quant"), []string{"addresses"}); err != nil {
			t.Fatalf("WriteSkillSubset: %v", err)
		}
		if _, err := os.Stat(HostNoBundledSkillsMarkerPath(cfg, "quant")); !os.IsNotExist(err) {
			t.Fatalf("embed.WriteSkillSubset created a .no-bundled-skills marker (err=%v); only SeedHostFiles may", err)
		}
	})

	// Sanity anchor: the full SeedHostFiles seam DOES write the marker, so the
	// negative assertions above are meaningful and not testing a dead path.
	t.Run("SeedHostFiles", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &config.Config{DataDir: dir}

		if _, err := SeedHostFiles(cfg, "quant", []string{"addresses"}, "obj", SeedOptions{}); err != nil {
			t.Fatalf("SeedHostFiles: %v", err)
		}
		if _, err := os.Stat(HostNoBundledSkillsMarkerPath(cfg, "quant")); err != nil {
			t.Fatalf("SeedHostFiles must write the .no-bundled-skills marker: %v", err)
		}
	})
}

func TestSeedHostFiles_PreservesExistingSoul(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	// Pretend the agent has already self-edited its SOUL.md.
	if err := os.MkdirAll(filepath.Dir(HostSoulPath(cfg, "quant")), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("# I rewrote my own soul")
	if err := os.WriteFile(HostSoulPath(cfg, "quant"), custom, 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"This should NOT overwrite",
		SeedOptions{},
	)
	if err != nil {
		t.Fatalf("SeedHostFiles: %v", err)
	}
	if wrote {
		t.Error("expected soulWritten=false because SOUL.md already exists")
	}

	body, err := os.ReadFile(HostSoulPath(cfg, "quant"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(custom) {
		t.Errorf("agent's SOUL.md was clobbered: got %q", string(body))
	}
}

func TestSeedHostFiles_MigratesLegacyLowercaseSoul(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if err := os.MkdirAll(filepath.Dir(HostLegacySoulPath(cfg, "quant")), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("# legacy lowercase identity")
	if err := os.WriteFile(HostLegacySoulPath(cfg, "quant"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"This should not replace legacy identity",
		SeedOptions{},
	)
	if err != nil {
		t.Fatalf("SeedHostFiles: %v", err)
	}
	if !wrote {
		t.Error("expected soulWritten=true when migrating legacy soul.md")
	}

	body, err := os.ReadFile(HostSoulPath(cfg, "quant"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(legacy) {
		t.Errorf("legacy soul was not migrated verbatim: %q", string(body))
	}
}

func TestSeedHostFiles_OverwriteSoulWhenForced(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if err := os.MkdirAll(filepath.Dir(HostSoulPath(cfg, "quant")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HostSoulPath(cfg, "quant"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"Replacement objective",
		SeedOptions{OverwriteSoul: true},
	)
	if err != nil {
		t.Fatalf("SeedHostFiles: %v", err)
	}
	if !wrote {
		t.Error("OverwriteSoul=true must force a write")
	}

	body, _ := os.ReadFile(HostSoulPath(cfg, "quant"))
	if !strings.Contains(string(body), "Replacement objective") {
		t.Errorf("overwrite did not replace contents: %q", string(body))
	}
}

func TestSeedHostFiles_ExactSkillsRemovesStaleDirs(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses", "gas"},
		"Objective",
		SeedOptions{},
	); err != nil {
		t.Fatalf("initial SeedHostFiles: %v", err)
	}
	customPath := filepath.Join(HostSkillsPath(cfg, "quant"), "addresses", "custom.txt")
	if err := os.WriteFile(customPath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write retained custom file: %v", err)
	}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"Objective",
		SeedOptions{ExactSkills: true},
	); err != nil {
		t.Fatalf("exact SeedHostFiles: %v", err)
	}

	if _, err := os.Stat(filepath.Join(HostSkillsPath(cfg, "quant"), "addresses", "SKILL.md")); err != nil {
		t.Fatalf("expected addresses skill to remain: %v", err)
	}
	if body, err := os.ReadFile(customPath); err != nil {
		t.Fatalf("expected retained custom file to survive exact sync: %v", err)
	} else if string(body) != "keep me" {
		t.Fatalf("retained custom file changed during exact sync: %q", string(body))
	}
	if _, err := os.Stat(filepath.Join(HostSkillsPath(cfg, "quant"), "gas")); !os.IsNotExist(err) {
		t.Fatalf("expected gas skill dir to be removed, err=%v", err)
	}
}

func TestWriteSoul_DoesNotTouchSkillFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"Original objective",
		SeedOptions{},
	); err != nil {
		t.Fatalf("initial SeedHostFiles: %v", err)
	}

	skillPath := filepath.Join(HostSkillsPath(cfg, "quant"), "addresses", "SKILL.md")
	before, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill before WriteSoul: %v", err)
	}

	if _, err := WriteSoul(cfg, "quant", "Updated objective", true); err != nil {
		t.Fatalf("WriteSoul: %v", err)
	}

	after, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill after WriteSoul: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("WriteSoul modified embedded skill contents unexpectedly")
	}
}

func TestWriteSoul_ReplacesSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if err := os.MkdirAll(filepath.Dir(HostSoulPath(cfg, "quant")), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, HostSoulPath(cfg, "quant")); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSoul(cfg, "quant", "Updated objective", true); err != nil {
		t.Fatalf("WriteSoul: %v", err)
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "outside" {
		t.Fatal("WriteSoul followed symlink and modified external file")
	}
	if info, err := os.Lstat(HostSoulPath(cfg, "quant")); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("WriteSoul left SOUL.md as a symlink instead of atomically replacing it")
	}
}

func TestSeedHostFiles_ExactSkillsRollbackOnUnknownSkill(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses", "gas"},
		"Objective",
		SeedOptions{},
	); err != nil {
		t.Fatalf("initial SeedHostFiles: %v", err)
	}

	skillPath := filepath.Join(HostSkillsPath(cfg, "quant"), "addresses", "SKILL.md")
	before, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill before failed exact sync: %v", err)
	}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses", "definitely-not-an-embedded-skill"},
		"Objective",
		SeedOptions{ExactSkills: true},
	); err == nil {
		t.Fatal("expected exact skill sync to fail for unknown skill")
	}

	after, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill after failed exact sync: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("failed exact sync changed existing skill contents")
	}
	if _, err := os.Stat(filepath.Join(HostSkillsPath(cfg, "quant"), "gas")); err != nil {
		t.Fatalf("failed exact sync should preserve gas skill dir: %v", err)
	}
}

func TestSeedHostFiles_ExactSkillsRejectsSymlinkedSkillsRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(HostSkillsPath(cfg, "quant")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, HostSkillsPath(cfg, "quant")); err != nil {
		t.Fatal(err)
	}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"Objective",
		SeedOptions{ExactSkills: true},
	); err == nil {
		t.Fatal("expected exact skill sync to reject a symlinked skills root")
	}
}

func TestSeedHostFiles_ExactSkillsRejectsSymlinkedRetainedEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses", "gas"},
		"Objective",
		SeedOptions{},
	); err != nil {
		t.Fatalf("initial SeedHostFiles: %v", err)
	}

	target := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(HostSkillsPath(cfg, "quant"), "addresses", "evil-link")); err != nil {
		t.Fatal(err)
	}

	if _, err := SeedHostFiles(cfg, "quant",
		[]string{"addresses"},
		"Objective",
		SeedOptions{ExactSkills: true},
	); err == nil {
		t.Fatal("expected exact skill sync to reject symlinked retained entries")
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "outside" {
		t.Fatal("exact skill sync modified external file via retained symlink")
	}
	if _, err := os.Stat(filepath.Join(HostSkillsPath(cfg, "quant"), "gas")); err != nil {
		t.Fatalf("failed exact sync should preserve gas skill dir after symlink rejection: %v", err)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
