package skillpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// skillFS builds a minimal valid skill tree as a MapFS. mtime/sys
// fields are parameterized so tests can prove they don't leak into the
// hash.
func skillFS(modTime time.Time) fstest.MapFS {
	return fstest.MapFS{
		"SKILL.md":          &fstest.MapFile{Data: []byte("# my-skill\n"), Mode: 0o644, ModTime: modTime},
		"scripts/run.py":    &fstest.MapFile{Data: []byte("print('hi')\n"), Mode: 0o755, ModTime: modTime},
		"references/ref.md": &fstest.MapFile{Data: []byte("ref\n"), Mode: 0o600, ModTime: modTime},
	}
}

func TestMaxBundleBytes_MatchesMonetizeAPI(t *testing.T) {
	if MaxBundleBytes != monetizeapi.MaxSkillBundleBytes {
		t.Fatalf("skillpkg.MaxBundleBytes = %d, monetizeapi.MaxSkillBundleBytes = %d — these caps must agree",
			MaxBundleBytes, monetizeapi.MaxSkillBundleBytes)
	}
}

func TestPack_Deterministic(t *testing.T) {
	fsys := skillFS(time.Unix(1700000000, 0))

	gz1, hash1, err := Pack(fsys)
	if err != nil {
		t.Fatalf("first pack: %v", err)
	}
	gz2, hash2, err := Pack(fsys)
	if err != nil {
		t.Fatalf("second pack: %v", err)
	}

	if !bytes.Equal(gz1, gz2) {
		t.Error("two packs of the same FS produced different bytes")
	}
	if hash1 != hash2 {
		t.Errorf("two packs of the same FS produced different hashes: %s vs %s", hash1, hash2)
	}
	if len(hash1) != 64 || strings.ToLower(hash1) != hash1 {
		t.Errorf("hash %q is not 64-char lowercase hex", hash1)
	}
}

// TestPack_MetadataIndependence proves on-disk metadata (mtimes, sys
// info, source modes that normalize to the same class) does not change
// the artifact hash.
func TestPack_MetadataIndependence(t *testing.T) {
	tests := []struct {
		name string
		a, b fstest.MapFS
	}{
		{
			name: "different mtimes",
			a:    skillFS(time.Unix(0, 0)),
			b:    skillFS(time.Now()),
		},
		{
			name: "different owner-ish sys info",
			a: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o644, Sys: &struct{ UID int }{1000}},
			},
			b: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o644, Sys: &struct{ UID int }{0}},
			},
		},
		{
			name: "modes within the same normalization class",
			a: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o644},
				"run.sh":   &fstest.MapFile{Data: []byte("y"), Mode: 0o755},
			},
			b: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o600},
				"run.sh":   &fstest.MapFile{Data: []byte("y"), Mode: 0o700},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gzA, hashA, err := Pack(tt.a)
			if err != nil {
				t.Fatalf("pack a: %v", err)
			}
			gzB, hashB, err := Pack(tt.b)
			if err != nil {
				t.Fatalf("pack b: %v", err)
			}
			if hashA != hashB {
				t.Errorf("hashes differ: %s vs %s", hashA, hashB)
			}
			if !bytes.Equal(gzA, gzB) {
				t.Error("bytes differ for metadata-only variation")
			}
		})
	}
}

// TestPack_CreationOrderIndependence writes the same content into two
// real directories in opposite creation order (and with different
// mtimes) and proves the hashes match. This is the on-disk analog of
// the MapFS determinism tests.
func TestPack_CreationOrderIndependence(t *testing.T) {
	files := map[string]string{
		"SKILL.md":       "# skill\n",
		"scripts/a.py":   "a\n",
		"scripts/b.py":   "b\n",
		"references.txt": "r\n",
	}

	writeAll := func(t *testing.T, order []string) string {
		t.Helper()
		dir := t.TempDir()
		for _, rel := range order {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(files[rel]), 0o644); err != nil {
				t.Fatal(err)
			}
			// Scatter mtimes so this also covers epoch normalization on
			// a real filesystem.
			mt := time.Now().Add(-time.Duration(len(rel)) * time.Hour)
			if err := os.Chtimes(p, mt, mt); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	dirA := writeAll(t, []string{"SKILL.md", "scripts/a.py", "scripts/b.py", "references.txt"})
	dirB := writeAll(t, []string{"references.txt", "scripts/b.py", "scripts/a.py", "SKILL.md"})

	_, hashA, err := Pack(os.DirFS(dirA))
	if err != nil {
		t.Fatalf("pack a: %v", err)
	}
	_, hashB, err := Pack(os.DirFS(dirB))
	if err != nil {
		t.Fatalf("pack b: %v", err)
	}
	if hashA != hashB {
		t.Errorf("creation order changed the hash: %s vs %s", hashA, hashB)
	}
}

func TestPack_Errors(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		wantSub string
	}{
		{
			name: "symlink rejected",
			fsys: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o644},
				"link":     &fstest.MapFile{Mode: 0o644 | os.ModeSymlink},
			},
			wantSub: "symlinks and special files",
		},
		{
			name: "missing SKILL.md",
			fsys: fstest.MapFS{
				"scripts/run.py": &fstest.MapFile{Data: []byte("x"), Mode: 0o644},
			},
			wantSub: "must contain SKILL.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Pack(tt.fsys)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestPack_RejectsOversizeAfterGzip(t *testing.T) {
	// Incompressible (random) payload comfortably above the cap so the
	// post-gzip size still exceeds MaxBundleBytes.
	big := make([]byte, MaxBundleBytes+200000)
	rnd := rand.New(rand.NewSource(42)) //nolint:gosec // determinism wanted, not security
	rnd.Read(big)

	fsys := fstest.MapFS{
		"SKILL.md": &fstest.MapFile{Data: []byte("x"), Mode: 0o644},
		"blob.bin": &fstest.MapFile{Data: big, Mode: 0o644},
	}

	_, _, err := Pack(fsys)
	if err == nil {
		t.Fatal("expected oversize error, got nil")
	}
	if !strings.Contains(err.Error(), "900000-byte") {
		t.Errorf("oversize error should name the cap, got: %v", err)
	}
}

func TestPack_SkipsPythonArtifacts(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md":        &fstest.MapFile{Data: []byte("x"), Mode: 0o644},
		"scripts/run.py":  &fstest.MapFile{Data: []byte("y"), Mode: 0o644},
		"scripts/run.pyc": &fstest.MapFile{Data: []byte("z"), Mode: 0o644},
		"scripts/__pycache__/run.cpython-312.pyc": &fstest.MapFile{Data: []byte("z"), Mode: 0o644},
	}

	gz, _, err := Pack(fsys)
	if err != nil {
		t.Fatal(err)
	}

	names := tarEntryNames(t, gz)
	for _, n := range names {
		if strings.Contains(n, "pyc") || strings.Contains(n, "__pycache__") {
			t.Errorf("python artifact leaked into bundle: %s", n)
		}
	}
}

// TestPack_NormalizesHeaders cracks the artifact open and verifies the
// determinism-relevant tar header fields entry by entry.
func TestPack_NormalizesHeaders(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md":       &fstest.MapFile{Data: []byte("doc"), Mode: 0o600, ModTime: time.Now()},
		"scripts/run.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\n"), Mode: 0o700, ModTime: time.Now()},
	}

	gz, _, err := Pack(fsys)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	if zr.Header.Name != "" {
		t.Errorf("gzip header name = %q, want empty", zr.Header.Name)
	}
	if zr.Header.OS != 255 {
		t.Errorf("gzip header OS = %d, want 255", zr.Header.OS)
	}

	wantModes := map[string]int64{
		"SKILL.md":       0o644,
		"scripts/":       0o755,
		"scripts/run.sh": 0o755, // exec bit on source promotes to 0755
	}
	tr := tar.NewReader(zr)
	var got []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hdr.Name)
		if want, ok := wantModes[hdr.Name]; ok && hdr.Mode != want {
			t.Errorf("%s mode = %o, want %o", hdr.Name, hdr.Mode, want)
		}
		if !hdr.ModTime.Equal(time.Unix(0, 0)) {
			t.Errorf("%s mtime = %v, want epoch", hdr.Name, hdr.ModTime)
		}
		if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
			t.Errorf("%s ownership not cleared: uid=%d gid=%d uname=%q gname=%q", hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
		}
	}

	want := []string{"SKILL.md", "scripts/", "scripts/run.sh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entry order = %v, want %v", got, want)
	}
}

func TestScanSecrets(t *testing.T) {
	tests := []struct {
		name      string
		fsys      fstest.MapFS
		wantCount int
		wantSub   string
	}{
		{
			name: "clean skill",
			fsys: fstest.MapFS{
				"SKILL.md":       &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				"scripts/run.py": &fstest.MapFile{Data: []byte("print(1)"), Mode: 0o644},
			},
			wantCount: 0,
		},
		{
			name: "dotenv file",
			fsys: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				".env":     &fstest.MapFile{Data: []byte("API_KEY=x"), Mode: 0o644},
			},
			wantCount: 1,
			wantSub:   "environment file",
		},
		{
			name: "dotenv variant",
			fsys: fstest.MapFS{
				"SKILL.md":    &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				".env.locals": &fstest.MapFile{Data: []byte("API_KEY=x"), Mode: 0o644},
			},
			wantCount: 1,
			wantSub:   "environment file",
		},
		{
			name: "ssh key name",
			fsys: fstest.MapFS{
				"SKILL.md":    &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				"keys/id_rsa": &fstest.MapFile{Data: []byte("whatever"), Mode: 0o600},
			},
			wantCount: 1,
			wantSub:   "SSH key",
		},
		{
			name: "pem marker in content",
			fsys: fstest.MapFS{
				"SKILL.md":  &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				"creds.txt": &fstest.MapFile{Data: []byte("-----BEGIN EC PRIVATE KEY-----\nabc\n"), Mode: 0o644},
			},
			wantCount: 1,
			wantSub:   "PRIVATE KEY",
		},
		{
			name: "key file with pem content warns for both",
			fsys: fstest.MapFS{
				"SKILL.md": &fstest.MapFile{Data: []byte("doc"), Mode: 0o644},
				"id_rsa":   &fstest.MapFile{Data: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), Mode: 0o600},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := ScanSecrets(tt.fsys)
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != tt.wantCount {
				t.Fatalf("got %d warnings %v, want %d", len(warnings), warnings, tt.wantCount)
			}
			if tt.wantSub != "" && !strings.Contains(strings.Join(warnings, "\n"), tt.wantSub) {
				t.Errorf("warnings %v do not mention %q", warnings, tt.wantSub)
			}
		})
	}
}

// tarEntryNames decompresses and lists tar entry names.
func tarEntryNames(t *testing.T, gz []byte) []string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}
