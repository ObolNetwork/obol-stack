// Package agentcrd contains host-side helpers for managing the obol.org/Agent
// CRD: building a spec from CLI flags, seeding the per-agent skills dir +
// SOUL.md on the host (which becomes the data PVC inside the cluster), and
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
// cluster mounts this into the Hermes pod via the data PVC; writing
// SOUL.md/skills here puts them inside the pod automatically.
func HostHomePath(cfg *config.Config, name string) string {
	desc := agentruntime.Describe(agentruntime.Hermes)
	return filepath.Join(cfg.DataDir, Namespace(name), desc.DataPVCName, desc.HomeDir)
}

// HostSkillsPath is the per-agent skills dir. OBOL_SKILLS_DIR points here
// from inside the pod.
func HostSkillsPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), "obol-skills")
}

// HostSoulPath is where the seeded Hermes identity file lives. Hermes reads
// uppercase SOUL.md from HERMES_HOME, so keep this path aligned with upstream
// Hermes profile semantics.
func HostSoulPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), "SOUL.md")
}

// HostNoBundledSkillsMarkerPath returns the location of the `.no-bundled-skills`
// marker file inside the agent's Hermes profile. When this file exists, Hermes'
// installer, `hermes update`, and skill syncs skip seeding bundled skills.
//
// Sub-agents only ever need the narrow, operator-chosen skill subset we layer
// in via OBOL_SKILLS_DIR; Hermes' ~80 bundled skills (apple-notes, spotify,
// github-pr-workflow, gif-search, Pokemon-player, …) just bloat the system
// prompt for an EVM-focused paid service. The marker is the official
// upstream-supported opt-out; see Hermes docs/user-guide/features/skills.
func HostNoBundledSkillsMarkerPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), ".no-bundled-skills")
}

// HostLegacySoulPath is the pre-profile seed path used before Hermes profile
// casing was aligned. It is read during migration only.
func HostLegacySoulPath(cfg *config.Config, name string) string {
	return filepath.Join(HostHomePath(cfg, name), "soul.md")
}

// SeedOptions controls how host-side seed data is written.
type SeedOptions struct {
	// OverwriteSoul forces a SOUL.md rewrite even if one already exists.
	// Default false: agent-owned after first reconcile.
	OverwriteSoul bool
	// ExactSkills removes any previously seeded skill dirs not present in the
	// requested set before copying the embedded skill subset.
	ExactSkills bool
}

// SeedHostFiles writes the chosen skill subset and seeds SOUL.md on the host
// data path. SOUL.md is only written when missing (or when OverwriteSoul is
// true).
//
// CONTRACT — sub-agents-for-sale ONLY. This helper also writes the
// `.no-bundled-skills` marker (see writeNoBundledSkillsMarker), which makes
// Hermes skip its ~80 bundled skills and run with just the operator-chosen
// subset. That is correct for a narrow, EVM-focused paid service but WRONG for
// the stack-managed master agent, which keeps the full bundled set. The master
// (internal/hermes) seeds its own home via a separate path (it does not call
// this) and must NEVER be routed through SeedHostFiles, or it would silently
// lose its bundled skills. The reusable seed primitives this is built from
// (WriteSoul, embed.WriteSkillSubset) deliberately do NOT write the marker, so
// the objective-only update path — and any future master-side reuse — stays
// safe; locked by TestMarkerOnlyWrittenBySeedHostFiles.
//
// Returns whether SOUL.md was written this call so callers can report the
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
	if err := writeNoBundledSkillsMarker(cfg, name); err != nil {
		return false, fmt.Errorf("write no-bundled-skills marker: %w", err)
	}
	return WriteSoul(cfg, name, objective, opts.OverwriteSoul)
}

// writeNoBundledSkillsMarker drops a `.no-bundled-skills` file into the agent's
// Hermes profile dir so the runtime skips seeding its ~80 bundled skills.
// Idempotent: an existing marker is left as-is. The file is intentionally empty;
// Hermes treats presence-as-flag.
func writeNoBundledSkillsMarker(cfg *config.Config, name string) error {
	path := HostNoBundledSkillsMarkerPath(cfg, name)
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat marker: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create home dir: %w", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

// WriteSoul renders and writes SOUL.md for the named agent. When overwrite is
// false, an existing SOUL.md is preserved and the return value is false.
func WriteSoul(cfg *config.Config, name, objective string, overwrite bool) (bool, error) {
	soulPath := HostSoulPath(cfg, name)
	if _, statErr := os.Lstat(soulPath); statErr == nil {
		if !overwrite && pathHasExactBase(soulPath) {
			return false, nil
		}
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("stat SOUL.md: %w", statErr)
	}

	if !overwrite {
		copied, err := copyLegacySoulIfPresent(cfg, name, soulPath)
		if err != nil {
			return false, err
		}
		if copied {
			return true, nil
		}
	}

	rendered, err := agentruntime.RenderSoul(objective)
	if err != nil {
		return false, fmt.Errorf("render soul: %w", err)
	}
	if err := writeSoulFileAtomically(soulPath, []byte(rendered)); err != nil {
		return false, err
	}
	return true, nil
}

func pathHasExactBase(path string) bool {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return true
	}
	base := filepath.Base(path)
	for _, entry := range entries {
		if entry.Name() == base {
			return true
		}
	}
	return false
}

func copyLegacySoulIfPresent(cfg *config.Config, name, soulPath string) (bool, error) {
	legacyPath := HostLegacySoulPath(cfg, name)
	if legacyPath == soulPath {
		return false, nil
	}
	info, err := os.Lstat(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat legacy soul.md: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	body, err := os.ReadFile(legacyPath)
	if err != nil {
		return false, fmt.Errorf("read legacy soul.md: %w", err)
	}
	if err := writeSoulFileAtomically(soulPath, body); err != nil {
		return false, err
	}
	return true, nil
}

func writeSoulFileAtomically(path string, body []byte) error {
	soulDir := filepath.Dir(path)
	if err := os.MkdirAll(soulDir, 0o755); err != nil {
		return fmt.Errorf("create home dir: %w", err)
	}
	tmp, err := os.CreateTemp(soulDir, ".soul-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp SOUL.md: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp SOUL.md: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp SOUL.md: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp SOUL.md: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install SOUL.md: %w", err)
	}
	return nil
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
