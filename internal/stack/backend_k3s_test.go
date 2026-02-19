package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestK3sReadPid(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantPid     int
		wantErr     bool
		errContains string
	}{
		{name: "valid pid", content: "12345", wantPid: 12345},
		{name: "with trailing newline", content: "12345\n", wantPid: 12345},
		{name: "with whitespace", content: " 12345 ", wantPid: 12345},
		{name: "pid 1", content: "1", wantPid: 1},
		{name: "large pid", content: "4194304", wantPid: 4194304},
		{name: "not a number", content: "not-a-number", wantErr: true, errContains: "invalid PID"},
		{name: "empty content", content: "", wantErr: true, errContains: "invalid PID"},
		{name: "float", content: "123.45", wantErr: true, errContains: "invalid PID"},
		{name: "negative", content: "-1", wantErr: true, errContains: "invalid PID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := &config.Config{ConfigDir: tmpDir}

			pidPath := filepath.Join(tmpDir, k3sPidFile)
			if err := os.WriteFile(pidPath, []byte(tt.content), 0600); err != nil {
				t.Fatalf("WriteFile error: %v", err)
			}

			b := &K3sBackend{}
			pid, err := b.readPid(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readPid() = %d, nil error; want error containing %q", pid, tt.errContains)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("readPid() error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("readPid() unexpected error: %v", err)
			}
			if pid != tt.wantPid {
				t.Errorf("readPid() = %d, want %d", pid, tt.wantPid)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		b := &K3sBackend{}
		_, err := b.readPid(cfg)
		if err == nil {
			t.Fatal("readPid() with no file should return error")
		}
	})
}

func TestK3sRemovePidFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	pidPath := filepath.Join(tmpDir, k3sPidFile)
	if err := os.WriteFile(pidPath, []byte("12345"), 0600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	b := &K3sBackend{}
	b.removePidFile(cfg)

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("PID file should have been removed")
	}
}

func TestK3sRemovePidFileNoop(t *testing.T) {
	// Removing a non-existent PID file should not panic or error
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	b := &K3sBackend{}
	b.removePidFile(cfg) // should not panic
}
