package agentskills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// manifestFile, written into an agent's skills dir, lists the skills obol
// installed there last time. Sync removes only those (and Retired) when they
// leave the set, so skills an operator dropped in by hand are never touched.
const manifestFile = ".obol-managed"

// Retired lists skills that earlier obol versions embedded and installed
// into every agent. Agents synced before the manifest existed still have
// them; Sync removes these on the first sync that lacks a manifest.
var Retired = []string{
	"addresses", "bridging", "building-blocks", "concepts", "gas", "indexing",
	"l2s", "standards", "wallets", "why", "distributed-validators", "monetize-guide",
	"autoresearch", "autoresearch-coordinator", "autoresearch-worker",
}

// Embedded returns the skills built into this obol binary, sorted.
func Embedded() []string {
	names, _ := embed.GetEmbeddedSkillNames()
	sort.Strings(names)

	return names
}

// IsEmbedded reports whether name is built into this binary.
func IsEmbedded(name string) bool {
	return slices.Contains(Embedded(), name)
}

// Available returns the opt-in skills in the pinned ObolNetwork/skills
// release (or OBOL_SKILLS_SOURCE), sorted. It may download the release.
func Available(cfg *config.Config) ([]string, error) {
	dir, err := repoSkillsDir(cfg)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string

	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(dir, e.Name(), "SKILL.md")) {
			names = append(names, e.Name())
		}
	}

	sort.Strings(names)

	return names, nil
}

// Write installs the named skills into dst, overwriting their files.
// Embedded names come from the binary; any other name is resolved from the
// pinned ObolNetwork/skills release, fetched on demand. Unknown names fail.
func Write(cfg *config.Config, dst string, names []string) error {
	var embedded, repo []string

	for _, n := range names {
		if IsEmbedded(n) {
			embedded = append(embedded, n)
		} else {
			repo = append(repo, n)
		}
	}

	// Resolve every name before writing anything, so a typo or an unreachable
	// release never leaves dst half-written.
	var src string

	if len(repo) > 0 {
		dir, err := repoSkillsDir(cfg)
		if err != nil {
			return fmt.Errorf("skills %s are not built into obol: %w", strings.Join(repo, ", "), err)
		}

		for _, n := range repo {
			if !fileExists(filepath.Join(dir, n, "SKILL.md")) {
				return fmt.Errorf("skill %q is neither built into obol nor in ObolNetwork/skills %s (see 'obol agent skills list --available')", n, Release)
			}
		}

		src = dir
	}

	if err := embed.WriteSkillSubset(dst, embedded); err != nil {
		return err
	}

	for _, n := range repo {
		to := filepath.Join(dst, n)
		if err := os.RemoveAll(to); err != nil {
			return err
		}

		if err := copyTree(filepath.Join(src, n), to); err != nil {
			return fmt.Errorf("install skill %s: %w", n, err)
		}
	}

	return nil
}

// PartialError reports opt-in skills that could not be (re)installed, e.g.
// because GitHub was unreachable. Built-in skills were synced and existing
// copies of the failed skills were kept, so callers should warn, not fail.
type PartialError struct {
	Skills []string
	Err    error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("kept existing copies of %s: %v", strings.Join(e.Skills, ", "), e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// Sync makes dst hold exactly names among the skills obol manages: it writes
// names, removes skills obol installed previously (per the manifest, or
// Retired when there is none yet) that are no longer wanted, and records the
// new set. Directories obol never installed are left alone.
//
// If opt-in skills can't be fetched, the built-in ones are still synced, the
// opt-in ones keep whatever copy dst already has, and a *PartialError is
// returned.
func Sync(cfg *config.Config, dst string, names []string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	previous, err := readManifest(dst)
	if errors.Is(err, fs.ErrNotExist) {
		previous = Retired
	} else if err != nil {
		return err
	}

	var partial *PartialError

	if err := Write(cfg, dst, names); err != nil {
		var builtin, optIn []string

		for _, n := range names {
			if IsEmbedded(n) {
				builtin = append(builtin, n)
			} else {
				optIn = append(optIn, n)
			}
		}

		if len(optIn) == 0 {
			return err
		}

		if err := embed.WriteSkillSubset(dst, builtin); err != nil {
			return err
		}

		partial = &PartialError{Skills: optIn, Err: err}
	}

	for _, old := range previous {
		if slices.Contains(names, old) || !validName(old) {
			continue
		}

		if err := os.RemoveAll(filepath.Join(dst, old)); err != nil {
			return fmt.Errorf("remove skill %s: %w", old, err)
		}
	}

	if err := writeManifest(dst, names); err != nil {
		return err
	}

	if partial != nil {
		return partial
	}

	return nil
}

func readManifest(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, err
	}

	var names []string

	for line := range strings.SplitSeq(string(data), "\n") {
		if n := strings.TrimSpace(line); n != "" && !strings.HasPrefix(n, "#") {
			names = append(names, n)
		}
	}

	return names, nil
}

func writeManifest(dir string, names []string) error {
	sorted := slices.Clone(names)
	sort.Strings(sorted)

	body := "# Skills installed by obol; it removes these when they leave the set.\n" +
		"# Other directories here are yours and are never touched.\n" +
		strings.Join(sorted, "\n") + "\n"

	return os.WriteFile(filepath.Join(dir, manifestFile), []byte(body), 0o644)
}

// validName guards removals driven by the manifest file, which lives in an
// agent-writable directory: a name can never reach outside dst.
func validName(n string) bool {
	if n == "" || n == "." || n == ".." {
		return false
	}

	for _, r := range n {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}

	return true
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			return os.WriteFile(target, data, info.Mode().Perm()|0o644)
		default:
			return nil // symlinks and specials are not copied
		}
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
