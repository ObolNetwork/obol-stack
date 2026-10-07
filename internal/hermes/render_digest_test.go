package hermes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenderedFilesDigest(t *testing.T) {
	dir := t.TempDir()
	empty := renderedFilesDigest(dir)

	if err := os.WriteFile(filepath.Join(dir, valuesFileName), []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	one := renderedFilesDigest(dir)
	if one == empty {
		t.Fatal("digest ignored values file")
	}

	if renderedFilesDigest(dir) != one {
		t.Fatal("digest not stable")
	}

	if err := os.WriteFile(filepath.Join(dir, helmfileFileName), []byte("releases: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if renderedFilesDigest(dir) == one {
		t.Fatal("digest ignored helmfile")
	}
}
