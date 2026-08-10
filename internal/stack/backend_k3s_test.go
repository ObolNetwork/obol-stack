package stack

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func writeK3sTestExecutable(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

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
			if err := os.WriteFile(pidPath, []byte(tt.content), 0o600); err != nil {
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
	if err := os.WriteFile(pidPath, []byte("12345"), 0o600); err != nil {
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

func TestK3sKillallPath(t *testing.T) {
	t.Run("configured bin directory", func(t *testing.T) {
		binDir := t.TempDir()
		want := filepath.Join(binDir, k3sKillall)
		writeK3sTestExecutable(t, want, "#!/bin/sh\nexit 0\n")

		got, err := (&K3sBackend{}).killallPath(&config.Config{BinDir: binDir})
		if err != nil {
			t.Fatalf("killallPath() error: %v", err)
		}

		if got != want {
			t.Fatalf("killallPath() = %q, want %q", got, want)
		}
	})

	t.Run("resolved k3s symlink directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink permissions differ on Windows")
		}

		root := t.TempDir()
		binDir := filepath.Join(root, "bin")
		targetDir := filepath.Join(root, "installation")
		k3sTarget := filepath.Join(targetDir, "k3s")
		want := filepath.Join(targetDir, k3sKillall)

		writeK3sTestExecutable(t, k3sTarget, "#!/bin/sh\nexit 0\n")
		writeK3sTestExecutable(t, want, "#!/bin/sh\nexit 0\n")

		want, err := filepath.EvalSymlinks(want)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", want, err)
		}

		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", binDir, err)
		}

		if err := os.Symlink(k3sTarget, filepath.Join(binDir, "k3s")); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}

		got, err := (&K3sBackend{}).killallPath(&config.Config{BinDir: binDir})
		if err != nil {
			t.Fatalf("killallPath() error: %v", err)
		}

		if got != want {
			t.Fatalf("killallPath() = %q, want %q", got, want)
		}
	})

	t.Run("configured helper must be executable", func(t *testing.T) {
		binDir := t.TempDir()
		path := filepath.Join(binDir, k3sKillall)

		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}

		if got, ok := firstExecutableFile([]string{path}); ok {
			t.Fatalf("firstExecutableFile() = %q, true; want false", got)
		}
	})
}

func TestK3sCleanupRuntimeUsesConfiguredDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("K3s backend is Linux-only")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	marker := filepath.Join(root, "data-dir.txt")
	writeK3sTestExecutable(t, filepath.Join(binDir, k3sKillall), `#!/bin/sh
printf '%s' "$K3S_DATA_DIR" > "$K3S_TEST_MARKER"
`)
	t.Setenv("K3S_TEST_MARKER", marker)

	cfg := &config.Config{BinDir: binDir, DataDir: filepath.Join(root, "data")}

	var stdout, stderr bytes.Buffer
	if err := (&K3sBackend{}).cleanupRuntime(cfg, ui.NewForTest(&stdout, &stderr)); err != nil {
		t.Fatalf("cleanupRuntime() error: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", marker, err)
	}

	want := filepath.Join(root, "data", "k3s")
	if string(got) != want {
		t.Fatalf("K3S_DATA_DIR = %q, want %q", got, want)
	}
}

func TestK3sDownCleansOrphansWithoutPidFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("K3s backend is Linux-only")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	marker := filepath.Join(root, "cleanup-ran")
	writeK3sTestExecutable(t, filepath.Join(binDir, k3sKillall), `#!/bin/sh
touch "$K3S_TEST_MARKER"
`)
	t.Setenv("K3S_TEST_MARKER", marker)

	cfg := &config.Config{
		BinDir:    binDir,
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}

	var stdout, stderr bytes.Buffer
	if err := (&K3sBackend{}).Down(cfg, ui.NewForTest(&stdout, &stderr), "test-stack"); err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup helper did not run: %v", err)
	}
}

func TestK3sDownReturnsCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("K3s backend is Linux-only")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	writeK3sTestExecutable(t, filepath.Join(binDir, k3sKillall), "#!/bin/sh\nexit 23\n")

	cfg := &config.Config{
		BinDir:    binDir,
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cfg.ConfigDir, err)
	}

	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)
	if err := os.WriteFile(pidPath, []byte("invalid-pid"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", pidPath, err)
	}

	var stdout, stderr bytes.Buffer

	err := (&K3sBackend{}).Down(cfg, ui.NewForTest(&stdout, &stderr), "test-stack")
	if err == nil {
		t.Fatal("Down() succeeded when cleanup helper failed")
	}

	if !strings.Contains(err.Error(), "failed to clean up k3s runtime") {
		t.Fatalf("Down() error = %q", err)
	}

	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("PID evidence was removed after cleanup failure: %v", err)
	}
}

func TestK3sDestroyPreservesPersistentData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("K3s backend is Linux-only")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	writeK3sTestExecutable(t, filepath.Join(binDir, k3sKillall), "#!/bin/sh\nexit 0\n")

	cfg := &config.Config{
		BinDir:    binDir,
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}

	statePath := filepath.Join(cfg.DataDir, "k3s", "server", "state")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(statePath), err)
	}

	if err := os.WriteFile(statePath, []byte("persistent"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", statePath, err)
	}

	var stdout, stderr bytes.Buffer
	if err := (&K3sBackend{}).Destroy(cfg, ui.NewForTest(&stdout, &stderr), "test-stack"); err != nil {
		t.Fatalf("Destroy() error: %v", err)
	}

	if got, err := os.ReadFile(statePath); err != nil {
		t.Fatalf("persistent K3s data was removed: %v", err)
	} else if string(got) != "persistent" {
		t.Fatalf("persistent K3s data = %q, want %q", got, "persistent")
	}
}
