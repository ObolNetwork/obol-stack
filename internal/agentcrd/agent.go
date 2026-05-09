// Package agentcrd contains host-side helpers for managing the obol.org/Agent
// CRD: building a spec from CLI flags, seeding the per-agent skills dir +
// soul.md on the host (which becomes the data PVC inside the cluster), and
// thin wrappers around kubectl apply/get/delete. The in-cluster reconciler
// in internal/serviceoffercontroller is the source of truth for the
// resulting K8s primitives; this package is just the host-side seam.
package agentcrd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// nameRE matches the same shape K8s allows for resource names. Mirrors the
// CRD-side validation (skill names use `^[a-z0-9][a-z0-9-]*$`) plus length
// caps so users get a useful error before hitting the apiserver.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Namespace returns the namespace for an agent of the given name. Single
// source of truth: keep callers from spreading "agent-" prefixes through
// the codebase.
func Namespace(name string) string {
	return "agent-" + name
}

// HostHomePath is where the agent's .hermes data lives on the host. The
// cluster mounts this into the Hermes pod via hostPath; writing
// soul.md/skills here puts them inside the pod automatically.
func HostHomePath(cfg *config.Config, name string) string {
	desc := agentruntime.Describe(agentruntime.Hermes)
	return filepath.Join(cfg.DataDir, Namespace(name), desc.DataPVCName, desc.HomeDir)
}

// HostSkillsPath is the per-agent skills dir. OBOL_SKILLS_DIR points here
// from inside the pod.
func HostSkillsPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), "obol-skills")
}

// HostSoulPath is where the seeded soul.md lives.
func HostSoulPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), "soul.md")
}

// SeedOptions controls how host-side seed data is written.
type SeedOptions struct {
	// OverwriteSoul forces a soul.md rewrite even if one already exists.
	// Default false: agent-owned after first reconcile.
	OverwriteSoul bool
	// ExactSkills removes any previously seeded skill dirs not present in the
	// requested set before copying the embedded skill subset.
	ExactSkills bool
}

// SeedHostFiles writes the chosen skill subset and seeds soul.md on the host
// data path. soul.md is only written when missing (or when OverwriteSoul is
// true).
//
// Returns whether soul.md was written this call so callers can report the
// difference between "fresh agent" and "existing agent, skills resynced".
func SeedHostFiles(cfg *config.Config, name string, skills []string, objective string, opts SeedOptions) (soulWritten bool, err error) {
	if opts.ExactSkills {
		if err := syncHostSkillsExact(HostSkillsPath(cfg, name), skills); err != nil {
			return false, fmt.Errorf("sync skills: %w", err)
		}
	} else {
		if err := embed.WriteSkillSubset(HostSkillsPath(cfg, name), skills); err != nil {
			return false, fmt.Errorf("write skills: %w", err)
		}
	}
	return WriteSoul(cfg, name, objective, opts.OverwriteSoul)
}

// WriteSoul renders and writes soul.md for the named agent. When overwrite is
// false, an existing soul.md is preserved and the return value is false.
func WriteSoul(cfg *config.Config, name, objective string, overwrite bool) (bool, error) {
	soulPath := HostSoulPath(cfg, name)
	if _, statErr := os.Lstat(soulPath); statErr == nil {
		if !overwrite {
			return false, nil
		}
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("stat soul.md: %w", statErr)
	}

	rendered, err := agentruntime.RenderSoul(objective)
	if err != nil {
		return false, fmt.Errorf("render soul: %w", err)
	}
	soulDir := filepath.Dir(soulPath)
	if err := os.MkdirAll(soulDir, 0o755); err != nil {
		return false, fmt.Errorf("create home dir: %w", err)
	}
	tmp, err := os.CreateTemp(soulDir, ".soul-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp soul.md: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("chmod temp soul.md: %w", err)
	}
	if _, err := tmp.WriteString(rendered); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temp soul.md: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp soul.md: %w", err)
	}
	if err := os.Rename(tmpPath, soulPath); err != nil {
		return false, fmt.Errorf("install soul.md: %w", err)
	}
	return true, nil
}

func syncHostSkillsExact(dst string, names []string) error {
	if dst == "" {
		return fmt.Errorf("syncHostSkillsExact: dst is empty")
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", parent, err)
	}

	tmpRoot, err := os.MkdirTemp(parent, ".obol-skills-sync-*")
	if err != nil {
		return fmt.Errorf("create temp skills dir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)

	staged := filepath.Join(tmpRoot, "obol-skills")
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to exact-sync symlinked skills dir %s", dst)
		}
		if err := copyDir(dst, staged); err != nil {
			return fmt.Errorf("stage current skills dir: %w", err)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(staged, 0o755); err != nil {
			return fmt.Errorf("create staged skills dir: %w", err)
		}
	} else {
		return fmt.Errorf("stat current skills dir: %w", err)
	}

	if err := removeStaleSkillDirs(staged, names); err != nil {
		return err
	}
	if len(names) > 0 {
		if err := embed.WriteSkillSubset(staged, names); err != nil {
			return err
		}
	}

	backup := dst + ".bak"
	_ = os.RemoveAll(backup)
	movedExisting := false
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to exact-sync symlinked skills dir %s", dst)
		}
		if err := os.Rename(dst, backup); err != nil {
			return fmt.Errorf("backup current skills dir: %w", err)
		}
		movedExisting = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat current skills dir: %w", err)
	}

	if err := os.Rename(staged, dst); err != nil {
		if movedExisting {
			_ = os.RemoveAll(dst)
			_ = os.Rename(backup, dst)
		}
		return fmt.Errorf("activate staged skills dir: %w", err)
	}
	if movedExisting {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove skills backup: %w", err)
		}
	}
	return nil
}

func removeStaleSkillDirs(dst string, names []string) error {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		return fmt.Errorf("read skills dir %s: %w", dst, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := want[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dst, entry.Name())); err != nil {
			return fmt.Errorf("remove stale skill %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	if info, err := os.Lstat(src); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to stage symlinked path %s", src)
	} else if !info.IsDir() {
		return fmt.Errorf("expected directory at %s", src)
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to stage symlink %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := dst
		if rel != "." {
			out = filepath.Join(dst, rel)
		}
		if info.IsDir() {
			return os.MkdirAll(out, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode())
	})
}

// ValidateName reports whether the agent name is valid for both K8s resource
// naming and the obol.org/Agent CRD pattern.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name is required")
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("agent name %q must match %s (lowercase letters, digits, dashes; first char alnum; max 63 chars)", name, nameRE.String())
	}
	return nil
}

// ParseSkills splits the CLI-style comma list and validates that every name
// matches the CRD's skill-name pattern. Empty strings between commas are
// dropped; nothing is fancy on purpose.
func ParseSkills(csv string) ([]string, error) {
	if strings.TrimSpace(csv) == "" {
		return nil, nil
	}
	skillRE := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	var out []string
	for _, raw := range strings.Split(csv, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if !skillRE.MatchString(name) {
			return nil, fmt.Errorf("invalid skill name %q (lowercase letters, digits, dashes only; must start alnum)", name)
		}
		out = append(out, name)
	}
	return out, nil
}

// BuildAgent assembles the unstructured Agent manifest for kubectl apply.
// The reconciler is responsible for filling status; we never write it from
// the CLI side.
func BuildAgent(name string, opts AgentOptions) map[string]any {
	spec := map[string]any{
		"runtime": monetizeapi.AgentRuntimeHermes,
	}
	if opts.Model != "" {
		spec["model"] = opts.Model
	}
	if len(opts.Skills) > 0 {
		// Render as []any so json.Marshal produces a YAML array, not a
		// typed-string slice that the apiserver validates oddly.
		raw := make([]any, len(opts.Skills))
		for i, s := range opts.Skills {
			raw[i] = s
		}
		spec["skills"] = raw
	}
	if strings.TrimSpace(opts.Objective) != "" {
		spec["objective"] = strings.TrimSpace(opts.Objective)
	}
	if opts.CreateWallet {
		spec["wallet"] = map[string]any{"create": true}
	}

	return map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "Agent",
		"metadata": map[string]any{
			"name":      name,
			"namespace": Namespace(name),
		},
		"spec": spec,
	}
}

// AgentOptions is the host-side projection of AgentSpec used by the CLI.
type AgentOptions struct {
	Model        string
	Skills       []string
	Objective    string
	CreateWallet bool
}
