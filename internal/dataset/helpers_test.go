package dataset

import (
	"os"
	"path/filepath"
	"testing"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func TestReadBundle(t *testing.T) {
	dir := t.TempDir()
	artifact := []byte(`{"messages":[]}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "sft.jsonl"), artifact, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"hash":"`+hashA+`","files":["chatml.jsonl","sft.jsonl"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mh, path, fh, size, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if mh != hashA {
		t.Errorf("manifestHash = %q, want %q", mh, hashA)
	}
	if filepath.Base(path) != "sft.jsonl" {
		t.Errorf("picked %q, want sft.jsonl (instruction-format preference)", filepath.Base(path))
	}
	if fh != sha256hex(artifact) {
		t.Errorf("fileHash = %q, want %q", fh, sha256hex(artifact))
	}
	if size != int64(len(artifact)) {
		t.Errorf("size = %d, want %d", size, len(artifact))
	}
}

func TestReadBundle_Errors(t *testing.T) {
	t.Run("missing manifest", func(t *testing.T) {
		if _, _, _, _, err := ReadBundle(t.TempDir()); err == nil {
			t.Error("missing manifest should error")
		}
	})
	t.Run("bad hash length", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"hash":"abcd","files":["a.jsonl"]}`), 0o644)
		if _, _, _, _, err := ReadBundle(dir); err == nil {
			t.Error("short hash should error")
		}
	})
	t.Run("no jsonl artifact", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"hash":"`+hashA+`","files":["readme.txt"]}`), 0o644)
		if _, _, _, _, err := ReadBundle(dir); err == nil {
			t.Error("no jsonl should error")
		}
	})
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "ds.key")

	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
	// Second load returns the same key (stable owner identity).
	k2, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	a1 := ethcrypto.PubkeyToAddress(k1.PublicKey)
	a2 := ethcrypto.PubkeyToAddress(k2.PublicKey)
	if a1 != a2 {
		t.Errorf("reloaded key changed address: %s != %s", a1.Hex(), a2.Hex())
	}

	// Corrupt file -> error.
	bad := filepath.Join(t.TempDir(), "bad.key")
	_ = os.WriteFile(bad, []byte("not-a-key"), 0o600)
	if _, err := LoadOrCreateKey(bad); err == nil {
		t.Error("corrupt key file should error")
	}
}
