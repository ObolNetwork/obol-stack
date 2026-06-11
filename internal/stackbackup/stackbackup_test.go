package stackbackup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectDataNamespaces(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{
		"hermes-obol-agent", "openclaw-pleasing-blowfish", "agent-demo-quant",
		"ethereum-mainnet", "aztec-testnet", "llm", "centaur",
	} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file with a matching prefix must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "hermes-notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := selectDataNamespaces(dir)
	want := map[string]bool{
		"hermes-obol-agent":          true,
		"openclaw-pleasing-blowfish": true,
		"agent-demo-quant":           true,
	}
	if len(got) != len(want) {
		t.Fatalf("selectDataNamespaces = %v, want keys %v", got, want)
	}
	for _, ns := range got {
		if !want[ns] {
			t.Errorf("unexpected namespace selected: %s (chain data must be excluded)", ns)
		}
	}
}

func TestSkipConfigEntry(t *testing.T) {
	cases := map[string]bool{
		"kubeconfig.yaml":                  true,
		"kubeconfig.yaml.bak":              true,
		"defaults":                         true,
		filepath.Join("defaults", "x.yml"): true,
		".stack-id":                        false,
		"k3d.yaml":                         false,
		filepath.Join("applications", "hermes", "obol-agent", "values-hermes.yaml"): false,
		filepath.Join("sell-http", "llm__ollama-gated.yaml"):                        false,
	}
	for rel, want := range cases {
		if got := skipConfigEntry(rel); got != want {
			t.Errorf("skipConfigEntry(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestDecodeManifestVersionCheck(t *testing.T) {
	good, _ := json.Marshal(Manifest{Version: ManifestVersion})
	if _, err := decodeManifest(good); err != nil {
		t.Fatalf("current version rejected: %v", err)
	}
	bad, _ := json.Marshal(Manifest{Version: ManifestVersion + 1})
	if _, err := decodeManifest(bad); err == nil {
		t.Fatal("future archive version accepted — import must refuse unknown formats")
	}
}

func TestStripK8sJSON(t *testing.T) {
	in := []byte(`{
		"apiVersion": "v1", "kind": "List",
		"items": [{
			"apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
			"metadata": {
				"name": "x", "namespace": "llm",
				"uid": "abc", "resourceVersion": "123", "generation": 2,
				"creationTimestamp": "2026-01-01T00:00:00Z",
				"managedFields": [{"manager": "kubectl"}],
				"finalizers": ["obol.org/cleanup"],
				"ownerReferences": [{"uid": "p"}],
				"annotations": {
					"kubectl.kubernetes.io/last-applied-configuration": "{}",
					"keep-me": "yes"
				}
			},
			"spec": {"type": "http"},
			"status": {"conditions": []}
		}]
	}`)
	out, err := StripK8sJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{"uid", "resourceVersion", "managedFields", "creationTimestamp", "ownerReferences", "finalizers", "last-applied", `"status"`} {
		if strings.Contains(s, gone) {
			t.Errorf("stripped output still contains %q", gone)
		}
	}
	for _, kept := range []string{`"spec"`, `"keep-me"`, `"namespace": "llm"`} {
		if !strings.Contains(s, kept) {
			t.Errorf("stripped output lost %q", kept)
		}
	}
}

func TestStripK8sJSONEmptyList(t *testing.T) {
	out, err := StripK8sJSON([]byte(`{"apiVersion":"v1","kind":"List","items":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatalf("empty list should strip to nil, got %s", out)
	}
}

func TestNamespacesFromList(t *testing.T) {
	data := []byte(`{"items":[
		{"metadata":{"namespace":"agent-a"}},
		{"metadata":{"namespace":"agent-b"}},
		{"metadata":{"namespace":"agent-a"}}
	]}`)
	got := namespacesFromList(data)
	if len(got) != 2 || got[0] != "agent-a" || got[1] != "agent-b" {
		t.Fatalf("namespacesFromList = %v", got)
	}
	single := []byte(`{"metadata":{"namespace":"llm"}}`)
	if got := namespacesFromList(single); len(got) != 1 || got[0] != "llm" {
		t.Fatalf("single-object namespaces = %v", got)
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	sub := filepath.Join(src, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "skipme"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "test.tar.gz")
	w, err := newArchiveWriter(archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(Manifest{Version: ManifestVersion, CreatedAt: "now"})
	if err := w.addBytes(ManifestFileName, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	warns, err := w.addTree(src, "data/ns1", func(rel string) bool { return rel == "skipme" })
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Manifest readable without full extraction.
	m, err := readArchiveManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	if m.CreatedAt != "now" {
		t.Fatalf("manifest round trip: %+v", m)
	}

	// Full extraction restores content, symlinks, and honors the skip.
	dest := t.TempDir()
	err = walkArchive(archive, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		if clean == ManifestFileName {
			return nil
		}
		return extractEntry(tr, hdr, dest, clean)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "data/ns1/nested/b.txt"))
	if err != nil || string(got) != "world" {
		t.Fatalf("nested file: %q, %v", got, err)
	}
	target, err := os.Readlink(filepath.Join(dest, "data/ns1/link"))
	if err != nil || target != "a.txt" {
		t.Fatalf("symlink: %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "data/ns1/skipme")); !os.IsNotExist(err) {
		t.Fatal("skip filter ignored during archiving")
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	err = walkArchive(archive, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		t.Fatalf("callback reached for traversal entry %q", clean)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "escapes extraction root") {
		t.Fatalf("path traversal not rejected: %v", err)
	}
}

// TestExtractRejectsSymlinkEscape covers the symlink variant of the traversal
// escape: a clean entry NAME whose symlink TARGET points outside the root.
// Without target validation, a follow-up entry written through the link would
// land at an arbitrary path. Both a "../"-walking and an absolute target must
// be refused, and no link may be created.
func TestExtractRejectsSymlinkEscape(t *testing.T) {
	for _, target := range []string{"../../../../etc/cron.d", "/etc/passwd"} {
		archive := filepath.Join(t.TempDir(), "evil-symlink.tar.gz")
		f, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "link", Linkname: target, Mode: 0o777, Typeflag: tar.TypeSymlink}); err != nil {
			t.Fatal(err)
		}
		tw.Close()
		gz.Close()
		f.Close()

		dest := t.TempDir()
		err = walkArchive(archive, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
			return extractEntry(tr, hdr, dest, clean)
		})
		if err == nil || !strings.Contains(err.Error(), "escapes extraction root") {
			t.Fatalf("symlink escape to %q not rejected: %v", target, err)
		}
		if _, err := os.Lstat(filepath.Join(dest, "link")); !os.IsNotExist(err) {
			t.Fatalf("escaping symlink to %q must not be created: %v", target, err)
		}
	}
}

// TestExtractRejectsDecompressionBomb verifies the ratio guard trips on an
// archive that inflates far beyond its compressed size. Thresholds are lowered
// so the test stays small and fast.
func TestExtractRejectsDecompressionBomb(t *testing.T) {
	origFloor, origRatio := bombFloorBytes, maxCompressionRatio
	bombFloorBytes, maxCompressionRatio = 1024, 2
	defer func() { bombFloorBytes, maxCompressionRatio = origFloor, origRatio }()

	archive := filepath.Join(t.TempDir(), "bomb.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	zeros := make([]byte, 1<<20) // 1 MiB of zeros gzips to ~1 KiB -> ~1000:1
	if err := tw.WriteHeader(&tar.Header{Name: "big", Mode: 0o600, Size: int64(len(zeros)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(zeros); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	dest := t.TempDir()
	err = walkArchive(archive, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		return extractEntry(tr, hdr, dest, clean)
	})
	if err == nil || !strings.Contains(err.Error(), "decompression bomb") {
		t.Fatalf("decompression bomb not rejected: %v", err)
	}
}

func TestReadStackID(t *testing.T) {
	dir := t.TempDir()
	if got := readStackID(dir); got != "" {
		t.Fatalf("missing .stack-id should yield empty, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".stack-id"), []byte("big-teal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readStackID(dir); got != "big-teal" {
		t.Fatalf("readStackID = %q", got)
	}
}
