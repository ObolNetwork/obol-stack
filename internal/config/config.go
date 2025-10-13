package config

import (
	"os"
	"path/filepath"
)

// Config holds all obol configuration paths
type Config struct {
	ConfigDir string
	BinDir    string
	StateDir  string
}

// Load returns the configuration with XDG-compliant defaults
func Load() *Config {
	return &Config{
		ConfigDir: getConfigDir(),
		BinDir:    getBinDir(),
		StateDir:  getStateDir(),
	}
}

// getConfigDir returns OBOL_CONFIG_DIR or XDG_CONFIG_HOME/obol
// In development mode (OBOL_DEVELOPMENT=true), uses .workspace/config
func getConfigDir() string {
	if dir := os.Getenv("OBOL_CONFIG_DIR"); dir != "" {
		return dir
	}

	// Development mode: use .workspace directory in project root
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		cwd, _ := os.Getwd()
		return filepath.Join(cwd, ".workspace", "config")
	}

	// XDG_CONFIG_HOME defaults to ~/.config
	xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfigHome == "" {
		home, _ := os.UserHomeDir()
		xdgConfigHome = filepath.Join(home, ".config")
	}

	return filepath.Join(xdgConfigHome, "obol")
}

// getBinDir returns OBOL_BIN_DIR or OBOL_CONFIG_DIR/bin
// In development mode (OBOL_DEVELOPMENT=true), uses .workspace/bin
func getBinDir() string {
	if dir := os.Getenv("OBOL_BIN_DIR"); dir != "" {
		return dir
	}

	// Development mode: use .workspace directory in project root
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		cwd, _ := os.Getwd()
		return filepath.Join(cwd, ".workspace", "bin")
	}

	return filepath.Join(getConfigDir(), "bin")
}

// getStateDir returns OBOL_STATE_DIR or XDG_DATA_HOME/obol
// In development mode (OBOL_DEVELOPMENT=true), uses .workspace/state
func getStateDir() string {
	if dir := os.Getenv("OBOL_STATE_DIR"); dir != "" {
		return dir
	}

	// Development mode: use .workspace directory in project root
	if os.Getenv("OBOL_DEVELOPMENT") == "true" {
		cwd, _ := os.Getwd()
		return filepath.Join(cwd, ".workspace", "state")
	}

	// XDG_DATA_HOME defaults to ~/.local/share
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		home, _ := os.UserHomeDir()
		xdgDataHome = filepath.Join(home, ".local", "share")
	}

	return filepath.Join(xdgDataHome, "obol")
}

// GetDataDir returns the data directory for use in k3d volumes
// This is the same as StateDir but exposed for external tools
func (c *Config) GetDataDir() string {
	return c.StateDir
}
