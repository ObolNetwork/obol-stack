package stackbackup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

type testEntry struct {
	name     string
	mode     int64
	typeflag byte
	body     []byte
}

// writeTestArchive writes a tar.gz with the given entries in order.
func writeTestArchive(t *testing.T, entries []testEntry) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "test.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typeflag, Size: int64(len(e.body))}
		if e.typeflag == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(e.body) > 0 && e.typeflag == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return archive
}

func manifestEntry(t *testing.T, m Manifest) testEntry {
	t.Helper()
	if m.Version == 0 {
		m.Version = ManifestVersion
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return testEntry{name: ManifestFileName, mode: 0o600, typeflag: tar.TypeReg, body: data}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s: mode %o, want %o", filepath.Base(path), got, want)
	}
}

// TestExtractClampsModes: archive-declared group/world bits are dropped;
// files restore 0600 (0700 when the archive marks them executable) and new
// directories 0700, including when an existing file is overwritten.
func TestExtractClampsModes(t *testing.T) {
	archive := writeTestArchive(t, []testEntry{
		{name: "open-dir/", mode: 0o777, typeflag: tar.TypeDir},
		{name: "open-dir/file.txt", mode: 0o666, typeflag: tar.TypeReg, body: []byte("a")},
		{name: "open-dir/run.sh", mode: 0o777, typeflag: tar.TypeReg, body: []byte("b")},
		{name: "implicit/parent/file.txt", mode: 0o644, typeflag: tar.TypeReg, body: []byte("c")},
		{name: "existing.txt", mode: 0o666, typeflag: tar.TypeReg, body: []byte("new")},
	})

	dest := t.TempDir()
	existing := filepath.Join(dest, "existing.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := walkArchive(archive, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		return extractEntry(tr, hdr, dest, clean)
	}); err != nil {
		t.Fatal(err)
	}

	assertPerm(t, filepath.Join(dest, "open-dir"), 0o700)
	assertPerm(t, filepath.Join(dest, "open-dir", "file.txt"), 0o600)
	assertPerm(t, filepath.Join(dest, "open-dir", "run.sh"), 0o700)
	assertPerm(t, filepath.Join(dest, "implicit"), 0o700)
	assertPerm(t, filepath.Join(dest, "implicit", "parent"), 0o700)
	assertPerm(t, filepath.Join(dest, "implicit", "parent", "file.txt"), 0o600)
	assertPerm(t, existing, 0o600)
	if got, _ := os.ReadFile(existing); string(got) != "new" {
		t.Fatal("existing file not overwritten with archive content")
	}
}

// TestReadArchiveManifestUsesRatioGuard: the manifest scan reads through the
// same decompression-ratio limit as extraction.
func TestReadArchiveManifestUsesRatioGuard(t *testing.T) {
	origFloor, origRatio := bombFloorBytes, maxCompressionRatio
	bombFloorBytes, maxCompressionRatio = 1024, 2
	defer func() { bombFloorBytes, maxCompressionRatio = origFloor, origRatio }()

	// A highly compressible entry ahead of the manifest forces the scan to
	// inflate it before reaching manifest.json.
	archive := writeTestArchive(t, []testEntry{
		{name: "zeros", mode: 0o600, typeflag: tar.TypeReg, body: make([]byte, 1<<20)},
		manifestEntry(t, Manifest{CreatedAt: "now"}),
	})

	_, err := readArchiveManifest(archive)
	if err == nil || !strings.Contains(err.Error(), "decompression ratio") {
		t.Fatalf("readArchiveManifest = %v, want ratio-limit error", err)
	}
}

func TestReadArchiveManifestRejectsNonGzip(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain.tar.gz")
	if err := os.WriteFile(plain, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchiveManifest(plain); err == nil || !strings.Contains(err.Error(), "not a gzip archive") {
		t.Fatalf("readArchiveManifest = %v, want gzip error", err)
	}
}

func TestArchiveWriterTightensExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.tar.gz")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := newArchiveWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, path, 0o600)
}

const testAgentNS = "hermes-obol-agent"

// importArchiveWithAgentData builds an archive carrying one keystore for
// testAgentNS (synthetic, non-key content).
func importArchiveWithAgentData(t *testing.T) string {
	t.Helper()
	return writeTestArchive(t, []testEntry{
		manifestEntry(t, Manifest{
			CreatedAt:  "now",
			Components: []Component{{Name: "data", Included: true, Notes: []string{testAgentNS}}},
		}),
		{name: "data/" + testAgentNS + "/remote-signer-keystores/k1.json", mode: 0o600, typeflag: tar.TypeReg, body: []byte(`{"synthetic":"archive"}`)},
	})
}

func importTestConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data")}
	for _, d := range []string{cfg.ConfigDir, cfg.DataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// seedKeystores leaves two existing keystores in DataDir, as after a
// config-only purge.
func seedKeystores(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := filepath.Join(cfg.DataDir, testAgentNS, "remote-signer-keystores")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"k1.json", "k2.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"synthetic":"existing"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "k1.json")
}

func TestImportRefusesExistingDataWithoutForce(t *testing.T) {
	cfg := importTestConfig(t)
	k1 := seedKeystores(t, cfg)
	archive := importArchiveWithAgentData(t)

	err := Import(cfg, ImportOptions{Input: archive, SkipCluster: true}, testUI())
	if err == nil {
		t.Fatal("import over existing agent data without --force must fail")
	}
	for _, want := range []string{"2 keystore(s)", "1 agent data dir(s)", testAgentNS, cfg.DataDir, "--force", "obol agent wallet backup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if got, _ := os.ReadFile(k1); string(got) != `{"synthetic":"existing"}` {
		t.Fatal("existing keystore was modified")
	}
}

func TestImportOverwritesExistingDataWithForce(t *testing.T) {
	cfg := importTestConfig(t)
	k1 := seedKeystores(t, cfg)
	archive := importArchiveWithAgentData(t)

	if err := Import(cfg, ImportOptions{Input: archive, SkipCluster: true, Force: true}, testUI()); err != nil {
		t.Fatalf("import with --force: %v", err)
	}
	if got, _ := os.ReadFile(k1); string(got) != `{"synthetic":"archive"}` {
		t.Fatal("keystore not restored from archive")
	}
}

func TestImportIntoEmptyDataDir(t *testing.T) {
	cfg := importTestConfig(t)
	// An empty namespace dir (e.g. pre-created by the runtime) is no conflict.
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, testAgentNS), 0o700); err != nil {
		t.Fatal(err)
	}
	archive := importArchiveWithAgentData(t)

	if err := Import(cfg, ImportOptions{Input: archive, SkipCluster: true}, testUI()); err != nil {
		t.Fatalf("import into empty data dir: %v", err)
	}
	k1 := filepath.Join(cfg.DataDir, testAgentNS, "remote-signer-keystores", "k1.json")
	if got, _ := os.ReadFile(k1); string(got) != `{"synthetic":"archive"}` {
		t.Fatal("keystore not restored")
	}
	assertPerm(t, k1, 0o600)
}

func TestImportIgnoresDataNotInArchive(t *testing.T) {
	cfg := importTestConfig(t)
	// Data of a namespace the archive does not carry is not overwritten.
	other := filepath.Join(cfg.DataDir, "agent-other", "state.db")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Import(cfg, ImportOptions{Input: importArchiveWithAgentData(t), SkipCluster: true}, testUI()); err != nil {
		t.Fatalf("import: %v", err)
	}
}

func TestFindDataConflictsLegacyManifest(t *testing.T) {
	cfg := importTestConfig(t)
	seedKeystores(t, cfg)
	// No data component recorded: every agent namespace present counts.
	c := findDataConflicts(cfg, &Manifest{Version: ManifestVersion})
	if len(c.dirs) != 1 || c.dirs[0] != testAgentNS || c.keystores != 2 {
		t.Fatalf("conflicts = %+v", c)
	}
	// Data component present but excluded: archive restores no data.
	c = findDataConflicts(cfg, &Manifest{Components: []Component{{Name: "data", Included: false}}})
	if len(c.dirs) != 0 {
		t.Fatalf("conflicts = %+v, want none", c)
	}
}
