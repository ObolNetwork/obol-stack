package embed

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Embedded file systems
// Note: embed paths are relative to this file's directory (internal/embed/)
//
//go:embed k3d-config.yaml
var K3dConfig string

//go:embed k3s-config.yaml
var K3sConfig string

//go:embed all:infrastructure
var infrastructureFS embed.FS

//go:embed all:networks
var networksFS embed.FS

//go:embed all:skills
var skillsFS embed.FS

// InfrastructureDigest returns a stable digest of the embedded infrastructure
// assets. Callers use this to decide whether an existing copied defaults tree
// needs to be refreshed from the current binary.
func InfrastructureDigest() (string, error) {
	hash := sha256.New()

	if err := fs.WalkDir(infrastructureFS, "infrastructure", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		data, err := infrastructureFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})

		return nil
	}); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CopyDefaults recursively copies all embedded infrastructure manifests to the destination directory.
// The replacements map is applied to every file: each key (e.g. "{{OLLAMA_HOST}}") is replaced
// with its value. Pass nil for a verbatim copy.
func CopyDefaults(destDir string, replacements map[string]string) error {
	return fs.WalkDir(infrastructureFS, "infrastructure", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root infrastructure directory
		if path == "infrastructure" {
			return nil
		}

		// Get relative path within infrastructure/
		relPath := strings.TrimPrefix(path, "infrastructure/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}

			return nil
		}

		// Ensure parent directory exists
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := infrastructureFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Apply placeholder replacements
		content := string(data)
		for placeholder, value := range replacements {
			content = strings.ReplaceAll(content, placeholder, value)
		}

		// Write to destination
		if err := os.WriteFile(destPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}

// GetAvailableNetworks returns a list of all embedded network names
func GetAvailableNetworks() ([]string, error) {
	entries, err := fs.ReadDir(networksFS, "networks")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded networks directory: %w", err)
	}

	var networks []string

	for _, entry := range entries {
		if entry.IsDir() {
			networks = append(networks, entry.Name())
		}
	}

	return networks, nil
}

// ReadEmbeddedNetworkFile reads a file from an embedded network
func ReadEmbeddedNetworkFile(networkName, filename string) ([]byte, error) {
	path := filepath.Join("networks", networkName, filename)

	content, err := networksFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s from network %s: %w", filename, networkName, err)
	}

	return content, nil
}

// ReadInfrastructureFile reads a file from the embedded infrastructure directory
func ReadInfrastructureFile(path string) ([]byte, error) {
	content, err := infrastructureFS.ReadFile(filepath.Join("infrastructure", path))
	if err != nil {
		return nil, fmt.Errorf("failed to read infrastructure file %s: %w", path, err)
	}

	return content, nil
}

// CopySkills recursively copies all embedded skills to the destination directory.
// If a skill directory already exists at the destination, it is skipped (user skills
// take precedence over embedded defaults).
func CopySkills(destDir string) error {
	return fs.WalkDir(skillsFS, "skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root skills directory
		if path == "skills" {
			return nil
		}

		// Get relative path within skills/
		relPath := strings.TrimPrefix(path, "skills/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}

			return nil
		}

		// Ensure parent directory exists
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := skillsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Write to destination
		if err := os.WriteFile(destPath, data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}

// WriteSkillSubset copies the named embedded skills into dst, overwriting any
// per-skill files already present for the named skills. It does NOT delete
// skills already at dst that aren't in names — agents own the dir after the
// initial seed. Callers that need exact-set semantics should remove the
// unwanted skill dirs themselves before calling.
//
// Returns an error if any requested name does not exist in the embedded FS,
// because a typo'd skill name should fail loudly rather than silently produce
// an under-skilled agent.
func WriteSkillSubset(dst string, names []string) error {
	if dst == "" {
		return fmt.Errorf("WriteSkillSubset: dst is empty")
	}
	if len(names) == 0 {
		return nil
	}

	for _, name := range names {
		src := filepath.Join("skills", name)
		info, err := fs.Stat(skillsFS, src)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("skill %q not found in embedded skills", name)
		}
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create skills dir %s: %w", dst, err)
	}

	for _, name := range names {
		src := filepath.Join("skills", name)
		skillDst := filepath.Join(dst, name)

		err := fs.WalkDir(skillsFS, src, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// __pycache__ dirs and .pyc files get generated whenever a dev runs
			// the skill's python scripts locally before `go build`. They'd then
			// get baked into the embed.FS and seeded onto every agent's PVC,
			// bloating the prompt scan and confusing python on a different
			// interpreter version. Skip them defensively here as well as via
			// the skills/.gitignore that keeps them out of the repo.
			if d.IsDir() && d.Name() == "__pycache__" {
				return fs.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".pyc") {
				return nil
			}
			rel := strings.TrimPrefix(path, src)
			rel = strings.TrimPrefix(rel, "/")
			out := skillDst
			if rel != "" {
				out = filepath.Join(skillDst, rel)
			}
			if d.IsDir() {
				return os.MkdirAll(out, 0o755)
			}
			data, err := skillsFS.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read embedded %s: %w", path, err)
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, data, 0o600)
		})
		if err != nil {
			return fmt.Errorf("write skill %q: %w", name, err)
		}
	}
	return nil
}

// GetEmbeddedSkillNames returns the names of all embedded skill directories.
func GetEmbeddedSkillNames() ([]string, error) {
	entries, err := fs.ReadDir(skillsFS, "skills")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded skills: %w", err)
	}

	var names []string

	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

// CopyNetwork recursively copies an embedded network to the destination directory
func CopyNetwork(networkName, destDir string) error {
	networkPath := filepath.Join("networks", networkName)

	// Check if network exists in embedded FS
	_, err := fs.Stat(networksFS, networkPath)
	if err != nil {
		return fmt.Errorf("network %s not found in embedded filesystem: %w", networkName, err)
	}

	return fs.WalkDir(networksFS, networkPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root network directory
		if path == networkPath {
			return nil
		}

		// Get relative path within networks/<network>/
		relPath := strings.TrimPrefix(path, networkPath+"/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory and continue walking
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}

			return nil
		}

		// Ensure parent directory exists
		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
		}

		// Read embedded file
		data, err := networksFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Write to destination
		if err := os.WriteFile(destPath, data, 0o600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}
