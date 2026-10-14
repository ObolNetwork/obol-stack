package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSecretTightensExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteSecret(path, []byte("new-synthetic-content")); err != nil {
		t.Fatalf("WriteSecret: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-synthetic-content" {
		t.Fatal("content not replaced")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteSecretMissingTargetAndParents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a", "b", "token")

	if err := WriteSecret(path, []byte("x")); err != nil {
		t.Fatalf("WriteSecret: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	for _, d := range []string{filepath.Join(root, "a"), filepath.Join(root, "a", "b")} {
		di, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm()&0o077 != 0 {
			t.Fatalf("parent %s mode = %o, want no group/other bits", d, di.Mode().Perm())
		}
	}
	assertNoTempFiles(t, filepath.Dir(path))
}

func TestWriteSecretFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	// Target is an existing non-empty directory, so the rename fails.
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecret(target, []byte("x")); err == nil {
		t.Fatal("expected error writing over a directory")
	}
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
