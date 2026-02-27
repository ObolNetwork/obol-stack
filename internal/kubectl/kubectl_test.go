package kubectl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestEnsureCluster_Missing(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	err := EnsureCluster(cfg)
	if err == nil {
		t.Fatal("expected error when kubeconfig missing")
	}
}

func TestEnsureCluster_Exists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig.yaml"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ConfigDir: dir}
	if err := EnsureCluster(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPaths(t *testing.T) {
	cfg := &config.Config{
		BinDir:    "/usr/local/bin",
		ConfigDir: "/home/user/.config/obol",
	}
	bin, kc := Paths(cfg)
	if bin != "/usr/local/bin/kubectl" {
		t.Errorf("binary = %q, want /usr/local/bin/kubectl", bin)
	}
	if kc != "/home/user/.config/obol/kubeconfig.yaml" {
		t.Errorf("kubeconfig = %q, want /home/user/.config/obol/kubeconfig.yaml", kc)
	}
}

func TestOutput_BinaryNotFound(t *testing.T) {
	_, err := Output("/nonexistent/kubectl", "/tmp/kc.yaml", "version")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRunSilent_BinaryNotFound(t *testing.T) {
	err := RunSilent("/nonexistent/kubectl", "/tmp/kc.yaml", "version")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}
