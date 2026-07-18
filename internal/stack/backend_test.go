package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Compile-time interface compliance checks
var (
	_ Backend = (*K3dBackend)(nil)
	_ Backend = (*K3sBackend)(nil)
)

func TestNewBackend(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantName    string
		wantErr     bool
		errContains string
	}{
		{name: "k3d backend", input: "k3d", wantName: "k3d"},
		{name: "k3s backend", input: "k3s", wantName: "k3s"},
		{name: "unknown backend", input: "docker", wantErr: true, errContains: "unknown backend"},
		{name: "empty string", input: "", wantErr: true, errContains: "unknown backend"},
		{name: "case sensitive", input: "K3D", wantErr: true, errContains: "unknown backend"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := NewBackend(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewBackend(%q) = nil error, want error containing %q", tt.input, tt.errContains)
				}

				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("NewBackend(%q) error = %q, want containing %q", tt.input, err.Error(), tt.errContains)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewBackend(%q) unexpected error: %v", tt.input, err)
			}

			if backend.Name() != tt.wantName {
				t.Errorf("NewBackend(%q).Name() = %q, want %q", tt.input, backend.Name(), tt.wantName)
			}
		})
	}
}

func TestK3dBackendName(t *testing.T) {
	b := &K3dBackend{}
	if got := b.Name(); got != BackendK3d {
		t.Errorf("K3dBackend.Name() = %q, want %q", got, BackendK3d)
	}
}

func TestK3sBackendName(t *testing.T) {
	b := &K3sBackend{}
	if got := b.Name(); got != BackendK3s {
		t.Errorf("K3sBackend.Name() = %q, want %q", got, BackendK3s)
	}
}

func TestK3dBackendDataDir(t *testing.T) {
	// k3d DataDir must always return "/data" regardless of cfg.DataDir,
	// because k3d mounts the host data dir to /data inside the container.
	tests := []struct {
		name    string
		dataDir string
	}{
		{name: "absolute path", dataDir: "/home/user/.local/share/obol"},
		{name: "relative path", dataDir: ".workspace/data"},
		{name: "empty string", dataDir: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &K3dBackend{}

			cfg := &config.Config{DataDir: tt.dataDir}
			if got := b.DataDir(cfg); got != "/data" {
				t.Errorf("K3dBackend.DataDir() = %q, want %q (must always be /data for Docker mount)", got, "/data")
			}
		})
	}
}

func TestK3sBackendDataDir(t *testing.T) {
	// k3s DataDir must return an absolute version of cfg.DataDir,
	// because k3s runs directly on the host.
	b := &K3sBackend{}

	t.Run("absolute path passthrough", func(t *testing.T) {
		cfg := &config.Config{DataDir: "/home/user/.local/share/obol"}

		got := b.DataDir(cfg)
		if got != "/home/user/.local/share/obol" {
			t.Errorf("K3sBackend.DataDir() = %q, want %q", got, "/home/user/.local/share/obol")
		}
	})

	t.Run("relative path resolved to absolute", func(t *testing.T) {
		cfg := &config.Config{DataDir: "relative/path"}

		got := b.DataDir(cfg)
		if !filepath.IsAbs(got) {
			t.Errorf("K3sBackend.DataDir() = %q, want absolute path", got)
		}

		if !strings.HasSuffix(got, "relative/path") {
			t.Errorf("K3sBackend.DataDir() = %q, want suffix %q", got, "relative/path")
		}
	})
}

func TestSaveAndLoadBackend(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		wantName string
	}{
		{name: "save k3s load k3s", backend: "k3s", wantName: "k3s"},
		{name: "save k3d load k3d", backend: "k3d", wantName: "k3d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := &config.Config{ConfigDir: tmpDir}

			if err := SaveBackend(cfg, tt.backend); err != nil {
				t.Fatalf("SaveBackend() error: %v", err)
			}

			backend, err := LoadBackend(cfg)
			if err != nil {
				t.Fatalf("LoadBackend() error: %v", err)
			}

			if backend.Name() != tt.wantName {
				t.Errorf("LoadBackend().Name() = %q, want %q", backend.Name(), tt.wantName)
			}
		})
	}
}

func TestLoadBackendFallsBackToK3d(t *testing.T) {
	// When no .stack-backend file exists, LoadBackend must return k3d
	// for backward compatibility with existing stacks.
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	backend, err := LoadBackend(cfg)
	if err != nil {
		t.Fatalf("LoadBackend() error: %v", err)
	}

	if backend.Name() != BackendK3d {
		t.Errorf("LoadBackend() with no file = %q, want %q (backward compat)", backend.Name(), BackendK3d)
	}
}

func TestLoadBackendWithWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	// Write file with trailing newline and whitespace
	path := filepath.Join(tmpDir, stackBackendFile)
	if err := os.WriteFile(path, []byte("k3s\n  "), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	backend, err := LoadBackend(cfg)
	if err != nil {
		t.Fatalf("LoadBackend() error: %v", err)
	}

	if backend.Name() != BackendK3s {
		t.Errorf("LoadBackend() = %q, want %q", backend.Name(), BackendK3s)
	}
}

func TestLoadBackendInvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{ConfigDir: tmpDir}

	path := filepath.Join(tmpDir, stackBackendFile)
	if err := os.WriteFile(path, []byte("docker-swarm"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := LoadBackend(cfg)
	if err == nil {
		t.Fatal("LoadBackend() with invalid backend name should return error")
	}

	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("LoadBackend() error = %q, want containing %q", err.Error(), "unknown backend")
	}
}

func TestK3dBackendInit(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
	}

	b := &K3dBackend{}

	u := ui.New(false)
	if err := b.Init(cfg, u, "test-stack", false); err != nil {
		t.Fatalf("K3dBackend.Init() error: %v", err)
	}

	// Verify config file was written
	configPath := filepath.Join(tmpDir, k3dConfigFile)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read generated config: %v", err)
	}

	content := string(data)

	// Verify placeholders were replaced
	if strings.Contains(content, "{{STACK_ID}}") {
		t.Error("Config still contains {{STACK_ID}} placeholder")
	}

	if strings.Contains(content, "{{DATA_DIR}}") {
		t.Error("Config still contains {{DATA_DIR}} placeholder")
	}

	if strings.Contains(content, "{{CONFIG_DIR}}") {
		t.Error("Config still contains {{CONFIG_DIR}} placeholder")
	}

	// Verify actual values are present
	if !strings.Contains(content, "test-stack") {
		t.Error("Config does not contain stack ID 'test-stack'")
	}

	// Verify paths are absolute
	if !strings.Contains(content, tmpDir) {
		t.Errorf("Config does not contain absolute data dir path %q", tmpDir)
	}
}

func TestK3sBackendInit(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ConfigDir: tmpDir,
		DataDir:   filepath.Join(tmpDir, "data"),
	}

	b := &K3sBackend{}

	u := ui.New(false)
	if err := b.Init(cfg, u, "my-cluster", false); err != nil {
		t.Fatalf("K3sBackend.Init() error: %v", err)
	}

	// Verify config file was written
	configPath := filepath.Join(tmpDir, k3sConfigFile)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read generated config: %v", err)
	}

	content := string(data)

	// Verify placeholders were replaced
	if strings.Contains(content, "{{STACK_ID}}") {
		t.Error("Config still contains {{STACK_ID}} placeholder")
	}

	if strings.Contains(content, "{{DATA_DIR}}") {
		t.Error("Config still contains {{DATA_DIR}} placeholder")
	}

	// Verify actual values are present
	if !strings.Contains(content, "my-cluster") {
		t.Error("Config does not contain stack ID 'my-cluster'")
	}

	// Verify data-dir uses absolute path
	absDataDir, _ := filepath.Abs(filepath.Join(tmpDir, "data"))

	expectedDataDir := absDataDir + "/k3s"
	if !strings.Contains(content, expectedDataDir) {
		t.Errorf("Config does not contain absolute data-dir %q", expectedDataDir)
	}
}

func TestDetectExistingBackend(t *testing.T) {
	t.Run("returns saved backend name", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		if err := SaveBackend(cfg, "k3s"); err != nil {
			t.Fatalf("SaveBackend() error: %v", err)
		}

		got := DetectExistingBackend(cfg)
		if got != "k3s" {
			t.Errorf("DetectExistingBackend() = %q, want %q", got, "k3s")
		}
	})

	t.Run("returns empty when no file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		got := DetectExistingBackend(cfg)
		if got != "" {
			t.Errorf("DetectExistingBackend() = %q, want empty string", got)
		}
	})

	t.Run("returns empty for corrupted file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		path := filepath.Join(tmpDir, stackBackendFile)
		os.WriteFile(path, []byte("docker-swarm"), 0o644)

		got := DetectExistingBackend(cfg)
		if got != "" {
			t.Errorf("DetectExistingBackend() = %q, want empty string for invalid backend", got)
		}
	})
}

func TestCleanupStaleBackendConfigs(t *testing.T) {
	t.Run("cleans k3d files when switching from k3d", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		// Create k3d config file
		k3dPath := filepath.Join(tmpDir, k3dConfigFile)
		os.WriteFile(k3dPath, []byte("k3d config"), 0o644)

		cleanupStaleBackendConfigs(cfg, BackendK3d)

		if _, err := os.Stat(k3dPath); !os.IsNotExist(err) {
			t.Error("k3d.yaml should be removed after cleanup")
		}
	})

	t.Run("cleans k3s files when switching from k3s", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		// Create k3s files
		for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
			os.WriteFile(filepath.Join(tmpDir, f), []byte("data"), 0o644)
		}

		cleanupStaleBackendConfigs(cfg, BackendK3s)

		for _, f := range []string{k3sConfigFile, k3sPidFile, k3sLogFile} {
			if _, err := os.Stat(filepath.Join(tmpDir, f)); !os.IsNotExist(err) {
				t.Errorf("%s should be removed after cleanup", f)
			}
		}
	})

	t.Run("no-op for same backend", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		// Create k3s config but clean up k3d (should not touch k3s files)
		k3sPath := filepath.Join(tmpDir, k3sConfigFile)
		os.WriteFile(k3sPath, []byte("k3s config"), 0o644)

		cleanupStaleBackendConfigs(cfg, BackendK3d)

		if _, err := os.Stat(k3sPath); os.IsNotExist(err) {
			t.Error("k3s-config.yaml should NOT be removed when cleaning k3d")
		}
	})
}

func TestOllamaHostForBackend(t *testing.T) {
	t.Run("k3s returns localhost", func(t *testing.T) {
		got := ollamaHostForBackend(BackendK3s)
		if got != "127.0.0.1" {
			t.Errorf("ollamaHostForBackend(k3s) = %q, want %q", got, "127.0.0.1")
		}
	})

	t.Run("k3d returns docker host", func(t *testing.T) {
		got := ollamaHostForBackend(BackendK3d)
		// On macOS: host.docker.internal, on Linux: host.k3d.internal
		if got != "host.docker.internal" && got != "host.k3d.internal" {
			t.Errorf("ollamaHostForBackend(k3d) = %q, want docker host", got)
		}
	})
}

func TestGetStackID(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "simple id", content: "happy-panda", want: "happy-panda"},
		{name: "with trailing newline", content: "happy-panda\n", want: "happy-panda"},
		{name: "with whitespace", content: "  happy-panda  \n", want: "happy-panda"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := &config.Config{ConfigDir: tmpDir}

			path := filepath.Join(tmpDir, stackIDFile)
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("WriteFile error: %v", err)
			}

			got := getStackID(cfg)
			if got != tt.want {
				t.Errorf("getStackID() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("missing file returns empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{ConfigDir: tmpDir}

		got := getStackID(cfg)
		if got != "" {
			t.Errorf("getStackID() with no file = %q, want empty string", got)
		}
	})
}
