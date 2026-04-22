package kubectl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig.yaml"), []byte("test"), 0o644); err != nil {
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

func TestWrapClusterDown(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		stderr  string
		wrapped bool
	}{
		{
			name:    "connection refused",
			err:     errors.New("exit status 1"),
			stderr:  `dial tcp 127.0.0.1:50839: connect: connection refused`,
			wrapped: true,
		},
		{
			name:    "unable to connect",
			err:     errors.New("exit status 1"),
			stderr:  `Unable to connect to the server: dial tcp 127.0.0.1:6443`,
			wrapped: true,
		},
		{
			name:    "normal error",
			err:     errors.New("resource not found"),
			stderr:  "Error from server (NotFound)",
			wrapped: false,
		},
		{
			name:    "nil error",
			err:     nil,
			stderr:  "",
			wrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapClusterDown(tt.err, tt.stderr)
			if tt.wrapped {
				if got == nil || !strings.Contains(got.Error(), "cluster appears to be stopped") {
					t.Errorf("expected cluster-down wrapper, got: %v", got)
				}
			} else if tt.err == nil {
				if got != nil {
					t.Errorf("expected nil, got: %v", got)
				}
			} else {
				if got == nil || strings.Contains(got.Error(), "cluster appears to be stopped") {
					t.Errorf("should not wrap normal errors, got: %v", got)
				}
			}
		})
	}
}

func TestFormatClusterDownError(t *testing.T) {
	// Contextual message for a known command.
	msg := FormatClusterDownError(ErrClusterDown, []string{"obol", "sell", "list"})
	if !strings.Contains(msg, "before listing services for sale") {
		t.Errorf("expected contextual hint, got: %s", msg)
	}

	// Fallback for an unknown subcommand.
	msg = FormatClusterDownError(ErrClusterDown, []string{"obol", "frobnicate"})
	if msg != ErrClusterDown.Error() {
		t.Errorf("expected fallback message, got: %s", msg)
	}

	// Non-cluster-down error returns empty.
	msg = FormatClusterDownError(errors.New("something else"), []string{"obol", "sell", "list"})
	if msg != "" {
		t.Errorf("expected empty for non-cluster-down error, got: %s", msg)
	}
}
