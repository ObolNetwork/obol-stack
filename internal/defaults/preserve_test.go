package defaults

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

const (
	editedRel = "values/erpc.yaml.gotmpl"
	otherRel  = "base/templates/x402.yaml"
)

func preserveTestCfg(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("OBOL_SKIP_IMAGE_DIGEST", "true")
	t.Setenv("OBOL_DEVELOPMENT", "")
	orig := nowFn
	nowFn = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { nowFn = orig })
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if _, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{}); err != nil {
		t.Fatalf("initial render: %v", err)
	}
	return cfg
}

func defaultsPath(cfg *config.Config, rel string) string {
	return filepath.Join(cfg.ConfigDir, "defaults", filepath.FromSlash(rel))
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// simulateOldDefault makes the manifest claim obol last wrote `content` at
// rel — i.e. the shipped default at rel changes on the next refresh.
func simulateOldDefault(t *testing.T, cfg *config.Config, rel, content string) {
	t.Helper()
	dir := filepath.Join(cfg.ConfigDir, "defaults")
	files, ok := readManifest(dir)
	if !ok {
		t.Fatal("manifest missing")
	}
	files[rel] = sha256Hex([]byte(content))
	if err := writeManifest(dir, files); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshWritesManifestForEveryFile(t *testing.T) {
	cfg := preserveTestCfg(t)
	files, ok := readManifest(filepath.Join(cfg.ConfigDir, "defaults"))
	if !ok {
		t.Fatal("manifest not written")
	}
	for _, rel := range []string{"helmfile.yaml", editedRel, otherRel} {
		if files[rel] != sha256Hex([]byte(mustRead(t, defaultsPath(cfg, rel)))) {
			t.Errorf("manifest hash for %s does not match the written file", rel)
		}
	}
	if _, tracked := files[stampFile]; tracked {
		t.Error("stamp file must not be tracked in the manifest")
	}
}

func TestRefreshOverwritesUneditedFiles(t *testing.T) {
	cfg := preserveTestCfg(t)
	want := mustRead(t, defaultsPath(cfg, otherRel))
	// The previous default differed, and the on-disk copy is exactly that
	// previous default: unedited, so it takes the new one.
	mustWrite(t, defaultsPath(cfg, otherRel), "old default\n")
	simulateOldDefault(t, cfg, otherRel, "old default\n")

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, defaultsPath(cfg, otherRel)); got != want {
		t.Error("unedited file was not overwritten with the new default")
	}
	if len(res.Pending)+len(res.BackedUp)+len(res.KeptEdited) != 0 {
		t.Errorf("unedited refresh should report nothing kept: %+v", res)
	}
}

func TestRefreshKeepsEditedFileWhenDefaultChanged(t *testing.T) {
	cfg := preserveTestCfg(t)
	newDefault := mustRead(t, defaultsPath(cfg, editedRel))
	simulateOldDefault(t, cfg, editedRel, "old default\n")
	mustWrite(t, defaultsPath(cfg, editedRel), "my edit\n")

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, defaultsPath(cfg, editedRel)); got != "my edit\n" {
		t.Fatalf("edited file overwritten: %q", got)
	}
	if len(res.Pending) != 1 || res.Pending[0].Path != defaultsPath(cfg, editedRel) {
		t.Fatalf("expected one pending default for the edited file: %+v", res.Pending)
	}
	pending := res.Pending[0].New
	if !strings.HasPrefix(pending, filepath.Join(cfg.ConfigDir, pendingDirName)) {
		t.Errorf("new default must be written outside the rendered tree: %s", pending)
	}
	if got := mustRead(t, pending); got != newDefault {
		t.Error("pending file does not hold the new default")
	}

	var buf bytes.Buffer
	ReportRefresh(ui.NewForTest(&buf, &buf), res, "obol stack up --reset-defaults")
	out := buf.String()
	for _, want := range []string{defaultsPath(cfg, editedRel), pending, "edited version", "--reset-defaults"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q:\n%s", want, out)
		}
	}

	// A later refresh keeps the edit and the pending file; once the operator
	// copies the new default over, the pending entry is cleared.
	if got := PendingDefaults(cfg); len(got) != 1 {
		t.Fatalf("PendingDefaults = %+v", got)
	}
	mustWrite(t, defaultsPath(cfg, editedRel), newDefault)
	if got := PendingDefaults(cfg); len(got) != 0 {
		t.Fatalf("resolved pending default still listed: %+v", got)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Error("resolved pending file should be removed")
	}
}

func TestRefreshKeepsEditedFileSilentlyWhenDefaultUnchanged(t *testing.T) {
	cfg := preserveTestCfg(t)
	mustWrite(t, defaultsPath(cfg, editedRel), "my edit\n")

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, defaultsPath(cfg, editedRel)); got != "my edit\n" {
		t.Fatalf("edited file overwritten: %q", got)
	}
	if len(res.Pending) != 0 || len(res.KeptEdited) != 1 {
		t.Fatalf("expected the edit kept without a pending default: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, pendingDirName)); !os.IsNotExist(err) {
		t.Error("no pending directory expected when the default did not change")
	}
}

func TestRefreshResetOverwritesAndBacksUp(t *testing.T) {
	cfg := preserveTestCfg(t)
	newDefault := mustRead(t, defaultsPath(cfg, editedRel))
	simulateOldDefault(t, cfg, editedRel, "old default\n")
	mustWrite(t, defaultsPath(cfg, editedRel), "my edit\n")
	if _, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true}); err != nil {
		t.Fatal(err)
	}

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, defaultsPath(cfg, editedRel)); got != newDefault {
		t.Fatal("--reset-defaults did not take the new default")
	}
	if len(res.BackedUp) != 1 {
		t.Fatalf("expected exactly the edited file backed up: %+v", res.BackedUp)
	}
	wantBackup := filepath.Join(cfg.ConfigDir, "defaults.bak.20260102T030405Z", filepath.FromSlash(editedRel))
	if res.BackedUp[0].Backup != wantBackup || mustRead(t, wantBackup) != "my edit\n" {
		t.Errorf("backup = %+v, want the edit at %s", res.BackedUp[0], wantBackup)
	}
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, pendingDirName)); !os.IsNotExist(err) {
		t.Error("reset should clear pending new defaults")
	}

	var buf bytes.Buffer
	ReportRefresh(ui.NewForTest(&buf, &buf), res, "")
	if !strings.Contains(buf.String(), wantBackup) {
		t.Errorf("reset output must print the backup path:\n%s", buf.String())
	}
}

func TestRefreshMigrationWithoutManifestBacksUpDifferingFiles(t *testing.T) {
	cfg := preserveTestCfg(t)
	want := mustRead(t, defaultsPath(cfg, editedRel))
	// Tree written by an obol without the manifest.
	if err := os.Remove(filepath.Join(cfg.ConfigDir, "defaults", manifestFile)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, defaultsPath(cfg, editedRel), "older default or an edit\n")

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Migrated {
		t.Error("expected migration to be reported")
	}
	if got := mustRead(t, defaultsPath(cfg, editedRel)); got != want {
		t.Error("migration must install the new default")
	}
	if len(res.BackedUp) != 1 || mustRead(t, res.BackedUp[0].Backup) != "older default or an edit\n" {
		t.Fatalf("differing file must be backed up before replacement: %+v", res.BackedUp)
	}
	if _, ok := readManifest(filepath.Join(cfg.ConfigDir, "defaults")); !ok {
		t.Error("migration must write the manifest")
	}

	var buf bytes.Buffer
	ReportRefresh(ui.NewForTest(&buf, &buf), res, "obol stack up --reset-defaults")
	if !strings.Contains(buf.String(), res.BackedUp[0].Backup) {
		t.Errorf("migration warning must list the backup:\n%s", buf.String())
	}
}

func TestRefreshRemovedDefaults(t *testing.T) {
	cfg := preserveTestCfg(t)
	dir := filepath.Join(cfg.ConfigDir, "defaults")
	files, _ := readManifest(dir)
	files["retired/unedited.yaml"] = sha256Hex([]byte("shipped\n"))
	files["retired/edited.yaml"] = sha256Hex([]byte("shipped\n"))
	if err := writeManifest(dir, files); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "retired"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "retired", "unedited.yaml"), "shipped\n")
	mustWrite(t, filepath.Join(dir, "retired", "edited.yaml"), "changed\n")

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "retired", "unedited.yaml")); !os.IsNotExist(err) {
		t.Error("unedited file no longer shipped should be removed")
	}
	if got := mustRead(t, filepath.Join(dir, "retired", "edited.yaml")); got != "changed\n" {
		t.Error("edited file no longer shipped must be kept")
	}
	if len(res.KeptRemoved) != 1 {
		t.Errorf("expected the edited retired file reported: %+v", res.KeptRemoved)
	}
}

func TestRecordRewriteKeepsObolEditsUnedited(t *testing.T) {
	cfg := preserveTestCfg(t)
	path := defaultsPath(cfg, "helmfile.yaml")
	before := mustRead(t, path)
	after := before + "# bumped\n"
	mustWrite(t, path, after)
	RecordRewrite(cfg, path, []byte(before), []byte(after))

	res, err := RefreshInfrastructure(cfg, backendK3s, "stack-a", RefreshOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if mustRead(t, path) != before {
		t.Error("an obol-recorded rewrite should be treated as unedited and refreshed")
	}
	if len(res.KeptEdited)+len(res.Pending) != 0 {
		t.Errorf("obol rewrite mistaken for a hand-edit: %+v", res)
	}
}
