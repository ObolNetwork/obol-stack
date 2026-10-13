package agentskills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// fakeRepo builds an ObolNetwork/skills-shaped checkout with the given opt-in
// skills and points OBOL_SKILLS_SOURCE at it.
func fakeRepo(t *testing.T, skills ...string) {
	t.Helper()

	root := t.TempDir()
	for _, s := range skills {
		dir := filepath.Join(root, "skills", s, "references")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		writeFile(t, filepath.Join(root, "skills", s, "SKILL.md"), "---\nname: "+s+"\n---\n")
		writeFile(t, filepath.Join(dir, "notes.md"), s+" notes\n")
	}

	t.Setenv(SourceEnv, root)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func testCfg(t *testing.T) *config.Config {
	t.Helper()

	return &config.Config{ConfigDir: t.TempDir(), StateDir: t.TempDir()}
}

func TestWrite_MixesBuiltInAndOptIn(t *testing.T) {
	fakeRepo(t, "distributed-validators")
	dst := t.TempDir()

	if err := Write(testCfg(t), dst, []string{"swap", "distributed-validators"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	for _, p := range []string{"swap/SKILL.md", "distributed-validators/SKILL.md", "distributed-validators/references/notes.md"} {
		if !exists(filepath.Join(dst, p)) {
			t.Errorf("missing %s", p)
		}
	}
}

func TestWrite_UnknownSkillWritesNothing(t *testing.T) {
	fakeRepo(t, "distributed-validators")
	dst := t.TempDir()

	err := Write(testCfg(t), dst, []string{"swap", "no-such-skill"})
	if err == nil || !strings.Contains(err.Error(), "no-such-skill") {
		t.Fatalf("Write = %v; want an error naming no-such-skill", err)
	}

	if exists(filepath.Join(dst, "swap")) {
		t.Fatal("built-in skill written although the set failed to resolve")
	}
}

func TestWrite_NoPinnedReleaseExplainsOverride(t *testing.T) {
	if ContentSHA256 != "" {
		t.Skip("release is pinned")
	}

	t.Setenv(SourceEnv, "")

	err := Write(testCfg(t), t.TempDir(), []string{"distributed-validators"})
	if err == nil || !strings.Contains(err.Error(), SourceEnv) {
		t.Fatalf("Write = %v; want a hint about %s", err, SourceEnv)
	}
}

// First sync after upgrading: no manifest yet, so retired skills that older
// obol versions installed are removed; hand-placed skills stay.
func TestSync_RemovesRetiredKeepsHandPlaced(t *testing.T) {
	fakeRepo(t)
	dst := t.TempDir()

	for _, d := range []string{"addresses", "monetize-guide", "my-own-skill"} {
		if err := os.MkdirAll(filepath.Join(dst, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := Sync(testCfg(t), dst, Embedded()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, gone := range []string{"addresses", "monetize-guide"} {
		if exists(filepath.Join(dst, gone)) {
			t.Errorf("retired skill %s still installed", gone)
		}
	}

	if !exists(filepath.Join(dst, "my-own-skill")) {
		t.Error("hand-placed skill removed")
	}

	if !exists(filepath.Join(dst, "sell", "SKILL.md")) {
		t.Error("built-in skill missing")
	}
}

func TestSync_RemovesOptInWhenDropped(t *testing.T) {
	fakeRepo(t, "distributed-validators")
	cfg := testCfg(t)
	dst := t.TempDir()

	if err := Sync(cfg, dst, append(Embedded(), "distributed-validators")); err != nil {
		t.Fatalf("Sync with opt-in: %v", err)
	}

	if !exists(filepath.Join(dst, "distributed-validators", "SKILL.md")) {
		t.Fatal("opt-in skill not installed")
	}

	if err := Sync(cfg, dst, Embedded()); err != nil {
		t.Fatalf("Sync without opt-in: %v", err)
	}

	if exists(filepath.Join(dst, "distributed-validators")) {
		t.Fatal("dropped opt-in skill still installed")
	}
}

// Offline: built-ins still sync, the opt-in copy already there is kept, and
// the caller gets a PartialError to warn with.
func TestSync_OfflineKeepsOptInCopy(t *testing.T) {
	fakeRepo(t, "distributed-validators")
	cfg := testCfg(t)
	dst := t.TempDir()

	want := append(Embedded(), "distributed-validators")

	if err := Sync(cfg, dst, want); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	t.Setenv(SourceEnv, filepath.Join(t.TempDir(), "missing"))

	err := Sync(cfg, dst, want)

	var partial *PartialError
	if !errors.As(err, &partial) || !slices.Equal(partial.Skills, []string{"distributed-validators"}) {
		t.Fatalf("Sync = %v; want a PartialError for distributed-validators", err)
	}

	if !exists(filepath.Join(dst, "distributed-validators", "SKILL.md")) {
		t.Fatal("existing opt-in copy removed while offline")
	}
}

func TestSync_IgnoresUnsafeManifestEntries(t *testing.T) {
	fakeRepo(t)
	dst := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")

	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dst, manifestFile), "../"+filepath.Base(victim)+"\n..\n/etc\n")

	if err := Sync(testCfg(t), dst, Embedded()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !exists(victim) {
		t.Fatal("manifest entry escaped the skills dir")
	}
}

func TestRecordRoundTrip(t *testing.T) {
	cfg := testCfg(t)

	if err := SetAdded(cfg, "obol-agent", []string{"b", "a", "b"}); err != nil {
		t.Fatal(err)
	}

	got, err := Added(cfg, "obol-agent")
	if err != nil || !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Added = %v, %v; want [a b]", got, err)
	}

	set, _ := DefaultAgentSet(cfg, "obol-agent")
	if !slices.Contains(set, "a") || !slices.Contains(set, "sell") {
		t.Fatalf("DefaultAgentSet = %v; want built-ins plus recorded", set)
	}

	if err := SetAdded(cfg, "obol-agent", nil); err != nil {
		t.Fatal(err)
	}

	if exists(RecordPath(cfg, "obol-agent")) {
		t.Fatal("empty record should remove the file")
	}
}

func tarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	_ = tw.Close()
	_ = gz.Close()

	return buf.Bytes()
}

func TestExtractSkillsTree(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "skills")
	data := tarball(t, map[string]string{
		"skills-1.9.0/README.md":                              "not a skill",
		"skills-1.9.0/skills/distributed-validators/SKILL.md":     "x",
		"skills-1.9.0/skills/distributed-validators/gas/GUIDE.md": "y",
	})

	if err := extractSkillsTree(bytes.NewReader(data), dst); err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(dst, "distributed-validators", "gas", "GUIDE.md")) || exists(filepath.Join(dst, "README.md")) {
		t.Fatal("extracted the wrong files")
	}

	d1, err := treeDigest(dst)
	if err != nil {
		t.Fatal(err)
	}

	d2, _ := treeDigest(dst)
	if d1 != d2 || len(d1) != 64 {
		t.Fatalf("treeDigest not stable: %s vs %s", d1, d2)
	}
}

func TestExtractSkillsTree_RejectsEscape(t *testing.T) {
	data := tarball(t, map[string]string{"skills-1.9.0/skills/../../evil": "x"})

	if err := extractSkillsTree(bytes.NewReader(data), filepath.Join(t.TempDir(), "skills")); err == nil {
		t.Fatal("expected an error for a path escaping the destination")
	}
}

// TestPinnedRelease downloads the pinned ObolNetwork/skills tag and checks it
// against ContentSHA256. Network-only: run with OBOL_SKILLS_VERIFY_PIN=1, and
// use the printed digest when bumping Release.
func TestPinnedRelease(t *testing.T) {
	if os.Getenv("OBOL_SKILLS_VERIFY_PIN") != "1" {
		t.Skip("set OBOL_SKILLS_VERIFY_PIN=1 to download and verify the pinned release")
	}

	tag := Release
	if v := os.Getenv("OBOL_SKILLS_VERIFY_TAG"); v != "" {
		tag = v // e.g. dry-run the download against an older tag
	}

	cache := filepath.Join(t.TempDir(), tag)
	if err := fetchRelease(cache, tag); err != nil {
		t.Fatal(err)
	}

	digest, err := treeDigest(filepath.Join(cache, "skills"))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("ObolNetwork/skills %s content digest: %s", tag, digest)

	if tag == Release && ContentSHA256 != "" && digest != ContentSHA256 {
		t.Fatalf("digest %s != pinned ContentSHA256 %s", digest, ContentSHA256)
	}
}
