package stackbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/defaults"
)

const (
	editedDefault   = "values/erpc.yaml.gotmpl"
	defaultsManFile = ".obol-defaults-manifest.json"
)

// defaultsStack is a config/data dir pair with a rendered defaults tree, as
// left by `obol stack init` (no cluster, no kubeconfig).
func defaultsStack(t *testing.T, stackID string) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data")}
	for _, d := range []string{cfg.ConfigDir, cfg.DataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, ".stack-id"), []byte(stackID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := defaults.RefreshInfrastructure(cfg, "k3s", stackID, defaults.RefreshOptions{}); err != nil {
		t.Fatalf("render defaults: %v", err)
	}
	return cfg
}

func defaultsFile(cfg *config.Config, rel string) string {
	return filepath.Join(cfg.ConfigDir, "defaults", filepath.FromSlash(rel))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func defaultsBackups(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(cfg.ConfigDir, "defaults.bak.*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func setDefaultsTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")
	t.Setenv("OBOL_DEVELOPMENT", "")
}

// handEditedExport is an exported stack with one hand-edited default
// (edited) and a pending new default (pending).
type handEditedExport struct {
	src                      *config.Config
	archive, edited, pending string
}

// exportHandEditedStack renders a stack, hand-edits one default, leaves a
// pending new default, and exports it.
func exportHandEditedStack(t *testing.T) handEditedExport {
	t.Helper()
	src := defaultsStack(t, "stack-a")
	edited := readFile(t, defaultsFile(src, editedDefault)) + "\n# operator hand-edit\n"
	writeFile(t, defaultsFile(src, editedDefault), edited)
	pending := "# newer shipped default\n"
	writeFile(t, filepath.Join(src.ConfigDir, "defaults.obol-new", filepath.FromSlash(editedDefault)), pending)

	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Export(src, ExportOptions{Output: archive, HasPassFlag: true}, testUI()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return handEditedExport{src: src, archive: archive, edited: edited, pending: pending}
}

func TestExportImport_DefaultsHandEditsSurvive(t *testing.T) {
	setDefaultsTestEnv(t)
	exp := exportHandEditedStack(t)
	src, archive, edited, pending := exp.src, exp.archive, exp.edited, exp.pending

	// Restore over a fresh `stack init` (pristine defaults, other stack ID).
	dst := defaultsStack(t, "fresh-id")
	if err := Import(dst, ImportOptions{Input: archive, Force: true, SkipCluster: true}, testUI()); err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := readFile(t, defaultsFile(dst, editedDefault)); got != edited {
		t.Errorf("hand-edited default not restored:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dst.ConfigDir, "defaults.obol-new", filepath.FromSlash(editedDefault))); got != pending {
		t.Errorf("pending new default = %q, want %q", got, pending)
	}
	if got, want := readFile(t, defaultsFile(dst, defaultsManFile)), readFile(t, defaultsFile(src, defaultsManFile)); got != want {
		t.Error("defaults manifest not restored verbatim")
	}
	// The replaced tree was pristine: nothing worth a backup.
	if b := defaultsBackups(t, dst); len(b) != 0 {
		t.Errorf("unexpected defaults backups for a pristine local tree: %v", b)
	}

	// The next refresh (stack up) must recognise the edit and keep it.
	res, err := defaults.RefreshInfrastructure(dst, "k3s", "stack-a", defaults.RefreshOptions{Force: true})
	if err != nil {
		t.Fatalf("refresh after import: %v", err)
	}
	if res.Migrated || len(res.BackedUp) != 0 {
		t.Errorf("refresh treated the restored tree as unmanaged: %+v", res)
	}
	// The restored pending copy keeps it reported as pending (not just kept).
	if len(res.Pending) != 1 || res.Pending[0].Path != defaultsFile(dst, editedDefault) {
		t.Errorf("edited default not kept as pending by refresh: %+v", res)
	}
	if got := readFile(t, defaultsFile(dst, editedDefault)); got != edited {
		t.Error("refresh after import overwrote the hand-edit")
	}
}

func TestImport_SetsAsideEditedLocalDefaults(t *testing.T) {
	setDefaultsTestEnv(t)
	exp := exportHandEditedStack(t)
	archive, edited := exp.archive, exp.edited

	dst := defaultsStack(t, "fresh-id")
	localEdit := "# local edit that the import replaces\n"
	writeFile(t, defaultsFile(dst, "base/templates/x402.yaml"), localEdit)
	// A stale pending file of the replaced tree must not survive.
	writeFile(t, filepath.Join(dst.ConfigDir, "defaults.obol-new", "stale.yaml"), "x")

	if err := Import(dst, ImportOptions{Input: archive, Force: true, SkipCluster: true}, testUI()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readFile(t, defaultsFile(dst, editedDefault)); got != edited {
		t.Error("archive's defaults not restored")
	}
	if got := readFile(t, defaultsFile(dst, "base/templates/x402.yaml")); got == localEdit {
		t.Error("local edit leaked into the restored tree")
	}
	backups := defaultsBackups(t, dst)
	if len(backups) != 1 {
		t.Fatalf("want one defaults backup, got %v", backups)
	}
	if got := readFile(t, filepath.Join(backups[0], "base", "templates", "x402.yaml")); got != localEdit {
		t.Errorf("local edit not preserved in %s", backups[0])
	}
	if _, err := os.Stat(filepath.Join(dst.ConfigDir, "defaults.obol-new", "stale.yaml")); !os.IsNotExist(err) {
		t.Error("stale pending default of the replaced tree survived the import")
	}
}

func TestImport_DefaultsRespectForceGate(t *testing.T) {
	setDefaultsTestEnv(t)
	archive := exportHandEditedStack(t).archive

	dst := defaultsStack(t, "fresh-id")
	localEdit := "# local edit\n"
	writeFile(t, defaultsFile(dst, editedDefault), localEdit)

	err := Import(dst, ImportOptions{Input: archive, SkipCluster: true}, testUI())
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("import over an existing stack without --force: err = %v", err)
	}
	if got := readFile(t, defaultsFile(dst, editedDefault)); got != localEdit {
		t.Error("refused import still touched the defaults tree")
	}
	if b := defaultsBackups(t, dst); len(b) != 0 {
		t.Errorf("refused import created backups: %v", b)
	}
}

// Archives written before defaults/ was exported carry no defaults: the local
// tree is left alone (stack up re-renders it).
func TestImport_LegacyArchiveKeepsLocalDefaults(t *testing.T) {
	setDefaultsTestEnv(t)
	archive := filepath.Join(t.TempDir(), "legacy.tar.gz")
	w, err := newArchiveWriter(archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(Manifest{Version: ManifestVersion, CreatedAt: "then", StackID: "old-id"})
	if err := w.addBytes(ManifestFileName, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.addBytes("config/.stack-id", []byte("old-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	dst := defaultsStack(t, "fresh-id")
	before := readFile(t, defaultsFile(dst, defaultsManFile))
	if err := Import(dst, ImportOptions{Input: archive, Force: true, SkipCluster: true}, testUI()); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := readFile(t, defaultsFile(dst, defaultsManFile)); got != before {
		t.Error("legacy archive import changed the local defaults tree")
	}
}

// A path re-pointed by the import is obol's own content: an otherwise
// unedited default must stay unedited in the manifest.
func TestRewriteRestoredPaths_KeepsDefaultsManifestInStep(t *testing.T) {
	setDefaultsTestEnv(t)
	cfg := defaultsStack(t, "stack-a")
	m := &Manifest{ConfigDir: "/Users/old/.config/obol", DataDir: "/Users/old/.local/share/obol"}

	// Simulate a shipped default that embeds the old host path.
	target := defaultsFile(cfg, editedDefault)
	withPath := readFile(t, target) + "\n# data: " + m.DataDir + "\n"
	writeFile(t, target, withPath)
	var man struct {
		Version int               `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(readFile(t, defaultsFile(cfg, defaultsManFile))), &man); err != nil {
		t.Fatal(err)
	}
	man.Files[editedDefault] = sha256Hex(withPath)
	data, _ := json.Marshal(man)
	writeFile(t, defaultsFile(cfg, defaultsManFile), string(data))

	if _, err := rewriteRestoredPaths(cfg, m); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); !strings.Contains(got, cfg.DataDir) {
		t.Fatalf("path not re-pointed:\n%s", got)
	}
	res, err := defaults.RefreshInfrastructure(cfg, "k3s", "stack-a", defaults.RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeptEdited)+len(res.Pending) != 0 {
		t.Errorf("re-pointed default mistaken for a hand-edit: %+v", res)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
