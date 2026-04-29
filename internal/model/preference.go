package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// preferenceFileName is the basename under cfg.ConfigDir where the user's
// explicit `obol model prefer` choice is persisted. A separate file (rather
// than a marker inside the LiteLLM ConfigMap) means the preference survives
// every autoConfigureLLM / defaults-refresh path that rewrites the ConfigMap
// — without that, rank-based selection would silently override the user's
// explicit pick on every stack restart.
const preferenceFileName = "preferred-model"

// preferencePath returns the absolute path where the preferred-model marker
// is stored. Empty cfg.ConfigDir returns "" so callers can treat that as
// "no preference state available" without fabricating a path.
func preferencePath(cfg *config.Config) string {
	if cfg == nil || cfg.ConfigDir == "" {
		return ""
	}
	return filepath.Join(cfg.ConfigDir, preferenceFileName)
}

// WritePreference records the user's explicit model preference. The value is
// trimmed; an empty value clears the preference (matching ClearPreference).
func WritePreference(cfg *config.Config, name string) error {
	path := preferencePath(cfg)
	if path == "" {
		return errors.New("config dir not set")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ClearPreference(cfg)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir preference dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
		return fmt.Errorf("write preference: %w", err)
	}
	return nil
}

// ReadPreference returns the user's persisted preferred model name, or "" if
// no preference has been set. All errors (missing file, unreadable, garbled)
// collapse to "" — callers treat absence and corruption identically because
// the right behavior in either case is to fall back to capability ranking.
func ReadPreference(cfg *config.Config) string {
	path := preferencePath(cfg)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ClearPreference removes the preference file. Missing file is not an error.
func ClearPreference(cfg *config.Config) error {
	path := preferencePath(cfg)
	if path == "" {
		return errors.New("config dir not set")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove preference: %w", err)
	}
	return nil
}
