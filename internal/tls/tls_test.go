package tls

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMkcertAvailableAndGenerateCerts(t *testing.T) {
	// Find the project workspace bin directory relative to this test file
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	workspaceBin := filepath.Join(projectRoot, ".workspace", "bin")

	// Try common locations for mkcert
	binDirs := []string{
		workspaceBin,
		os.Getenv("HOME") + "/.local/bin",
	}

	var binDir string
	for _, d := range binDirs {
		if MkcertAvailable(d) {
			binDir = d
			break
		}
	}

	if binDir == "" {
		t.Skip("mkcert not available in any known location")
	}

	t.Logf("mkcert found in: %s", binDir)

	tmpDir := t.TempDir()

	if err := GenerateCerts(binDir, tmpDir); err != nil {
		t.Fatalf("GenerateCerts failed: %v", err)
	}

	if !CertsExist(tmpDir) {
		t.Fatal("CertsExist returned false after GenerateCerts")
	}

	// Verify cert files are non-empty
	certData, err := os.ReadFile(CertPath(tmpDir))
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}
	if len(certData) == 0 {
		t.Error("cert file is empty")
	}

	keyData, err := os.ReadFile(KeyPath(tmpDir))
	if err != nil {
		t.Fatalf("failed to read key: %v", err)
	}
	if len(keyData) == 0 {
		t.Error("key file is empty")
	}

	t.Logf("cert: %d bytes, key: %d bytes", len(certData), len(keyData))
}

func TestCertsExist(t *testing.T) {
	tmpDir := t.TempDir()

	// No certs yet
	if CertsExist(tmpDir) {
		t.Error("CertsExist should return false for empty dir")
	}

	// Create the tls dir and cert file only
	tlsDir := CertDir(tmpDir)
	if err := os.MkdirAll(tlsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CertPath(tmpDir), []byte("cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if CertsExist(tmpDir) {
		t.Error("CertsExist should return false with only cert file")
	}

	// Create key file too
	if err := os.WriteFile(KeyPath(tmpDir), []byte("key"), 0644); err != nil {
		t.Fatal(err)
	}
	if !CertsExist(tmpDir) {
		t.Error("CertsExist should return true with both cert and key files")
	}
}

func TestPaths(t *testing.T) {
	dir := "/test/config"
	if got := CertDir(dir); got != "/test/config/tls" {
		t.Errorf("CertDir = %q, want /test/config/tls", got)
	}
	if got := CertPath(dir); got != "/test/config/tls/obol-stack.pem" {
		t.Errorf("CertPath = %q, want /test/config/tls/obol-stack.pem", got)
	}
	if got := KeyPath(dir); got != "/test/config/tls/obol-stack-key.pem" {
		t.Errorf("KeyPath = %q, want /test/config/tls/obol-stack-key.pem", got)
	}
}
