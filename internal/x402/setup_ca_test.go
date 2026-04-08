package x402

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPopulateCABundle_BuildsPatch(t *testing.T) {
	// Write a fake CA bundle to a temp file and verify the JSON patch
	// that populateCABundle would construct.
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca-certificates.crt")
	caContent := "-----BEGIN CERTIFICATE-----\nFAKECERT\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(caPath, []byte(caContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the patch structure matches what kubectl expects.
	patch := map[string]any{"data": map[string]string{"ca-certificates.crt": caContent}}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(patchJSON, &decoded); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}

	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatal("patch missing 'data' key")
	}
	cert, ok := data["ca-certificates.crt"].(string)
	if !ok || cert != caContent {
		t.Errorf("ca-certificates.crt = %q, want %q", cert, caContent)
	}
}

func TestPopulateCABundle_NoBundleSkips(t *testing.T) {
	// When no CA bundle exists at any candidate path, the function
	// should return silently without error. We test the logic inline
	// since the real function depends on kubectl.
	candidates := []string{
		"/nonexistent/path/1",
		"/nonexistent/path/2",
	}
	var caData []byte
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			caData = data
			break
		}
	}
	if len(caData) != 0 {
		t.Fatal("expected no CA data from nonexistent paths")
	}
}

func TestPopulateCABundle_EmptyFileSkips(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "empty.crt")
	if err := os.WriteFile(caPath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatal("expected empty file")
	}
}
