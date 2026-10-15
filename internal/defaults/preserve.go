package defaults

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Hand-edits to the rendered defaults tree ($CONFIG/defaults) are supported:
// not every knob has a CLI command. obol records the sha256 of every file it
// writes there (manifestFile) and, on refresh, only overwrites files whose
// on-disk content still matches what it last wrote.
//
// The new default for a kept file and the backups taken before overwriting
// an edited file live in sibling directories, NOT next to the file: Helm
// renders every file under a chart's templates/ directory regardless of
// extension, so a `<file>.obol-new` beside base/templates/x402.yaml would be
// applied as a second copy of the same resources.
const (
	manifestFile    = ".obol-defaults-manifest.json"
	pendingDirName  = "defaults.obol-new"
	backupDirPrefix = "defaults.bak."
)

// nowFn is swappable in tests (backup directory timestamps).
var nowFn = time.Now

// RefreshOptions controls RefreshInfrastructure.
type RefreshOptions struct {
	// Force re-renders even when the stamp (embedded digest, backend, stack
	// ID, binary identity) is unchanged.
	Force bool
	// Reset takes the new defaults for every file, copying hand-edited files
	// to a timestamped backup directory first.
	Reset bool
}

// FileBackup is an edited file that was copied aside before being replaced.
type FileBackup struct {
	Path   string
	Backup string
}

// PendingDefault is a hand-edited file that was kept; New holds the newer
// default obol would have written.
type PendingDefault struct {
	Path string
	New  string
}

// RefreshResult reports what a refresh did with hand-edited files.
type RefreshResult struct {
	Refreshed bool
	// Pending: edited files kept while the shipped default changed.
	Pending []PendingDefault
	// KeptEdited: edited files kept; their shipped default did not change.
	KeptEdited []string
	// KeptRemoved: edited files obol no longer ships, left in place.
	KeptRemoved []string
	// BackedUp: edited files replaced after a backup (Reset, or the first
	// refresh after upgrading from a version without the manifest).
	BackedUp []FileBackup
	// Migrated is true when no manifest existed for an existing tree, so
	// edits could not be told apart from older defaults.
	Migrated bool
}

type defaultsManifest struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readManifest(defaultsDir string) (map[string]string, bool) {
	data, err := os.ReadFile(filepath.Join(defaultsDir, manifestFile))
	if err != nil {
		return nil, false
	}
	var m defaultsManifest
	if err := json.Unmarshal(data, &m); err != nil || m.Files == nil {
		return nil, false
	}
	return m.Files, true
}

func writeManifest(defaultsDir string, files map[string]string) error {
	data, err := json.MarshalIndent(defaultsManifest{Version: 1, Files: files}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(defaultsDir, manifestFile), append(data, '\n'), 0o600)
}

// writeFileMkdir writes base/rel (rel in slash form), creating directories
// as needed. The write is scoped to base: rel cannot leave it.
func writeFileMkdir(base, rel string, data []byte) error {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()

	rel = filepath.FromSlash(rel)
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return root.WriteFile(rel, data, 0o600)
}

// RefreshInfrastructure re-renders the embedded infrastructure defaults when
// the stamp changed (or opts.Force), preserving hand-edited files:
//
//   - unedited (on-disk matches what obol last wrote) -> overwritten
//   - edited, shipped default changed -> kept; the new default is written
//     to $CONFIG/defaults.obol-new/<path> and reported in Pending
//   - edited, shipped default unchanged -> kept (KeptEdited)
//   - no longer shipped -> removed if unedited, kept and reported if edited
//   - opts.Reset -> everything overwritten; edited files are copied to
//     $CONFIG/defaults.bak.<timestamp>/<path> first
//   - existing tree without a manifest (written by an older obol) ->
//     every file differing from the new default is backed up, then replaced
func RefreshInfrastructure(cfg *config.Config, backendName, stackID string, opts RefreshOptions) (RefreshResult, error) {
	var res RefreshResult
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")

	stamp, err := infrastructureStamp(backendName, stackID)
	if err != nil {
		return res, err
	}
	if !opts.Force && !opts.Reset {
		currentStamp, _ := os.ReadFile(filepath.Join(defaultsDir, stampFile))
		_, helmfileErr := os.Stat(filepath.Join(defaultsDir, "helmfile.yaml"))
		if string(currentStamp) == stamp && helmfileErr == nil {
			return res, nil
		}
	}

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return res, err
	}
	staging, err := os.MkdirTemp(cfg.ConfigDir, ".defaults-render-")
	if err != nil {
		return res, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	if err := renderInfrastructure(cfg, staging, backendName, stackID); err != nil {
		return res, err
	}

	newFiles, err := readTree(staging)
	if err != nil {
		return res, fmt.Errorf("read rendered defaults: %w", err)
	}

	oldManifest, haveManifest := readManifest(defaultsDir)
	pendingDir := filepath.Join(cfg.ConfigDir, pendingDirName)
	backupDir := filepath.Join(cfg.ConfigDir, backupDirPrefix+nowFn().UTC().Format("20060102T150405Z"))
	backup := func(rel string, data []byte) error {
		dst := filepath.Join(backupDir, filepath.FromSlash(rel))
		if err := writeFileMkdir(backupDir, rel, data); err != nil {
			return fmt.Errorf("back up %s: %w", rel, err)
		}
		res.BackedUp = append(res.BackedUp, FileBackup{Path: filepath.Join(defaultsDir, filepath.FromSlash(rel)), Backup: dst})
		return nil
	}

	rels := make([]string, 0, len(newFiles))
	for rel := range newFiles {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	newManifest := make(map[string]string, len(newFiles))
	keepPending := map[string]bool{}
	for _, rel := range rels {
		newData := newFiles[rel]
		newHash := sha256Hex(newData)
		target := filepath.Join(defaultsDir, filepath.FromSlash(rel))
		newManifest[rel] = newHash

		disk, readErr := os.ReadFile(target)
		if readErr != nil {
			if !errors.Is(readErr, fs.ErrNotExist) {
				return res, fmt.Errorf("read %s: %w", target, readErr)
			}
			if err := writeFileMkdir(defaultsDir, rel, newData); err != nil {
				return res, err
			}
			continue
		}
		diskHash := sha256Hex(disk)
		if diskHash == newHash {
			continue
		}

		oldHash, tracked := oldManifest[rel]
		edited := !haveManifest || !tracked || diskHash != oldHash
		switch {
		case !edited:
			// Unedited: take the new default.
		case opts.Reset:
			if err := backup(rel, disk); err != nil {
				return res, err
			}
		case !haveManifest:
			// First refresh after upgrading from an obol without the
			// manifest: no record of what it wrote, so an older default
			// and a hand-edit look the same. Keep a copy, then replace.
			res.Migrated = true
			if err := backup(rel, disk); err != nil {
				return res, err
			}
		default:
			pendingPath := filepath.Join(pendingDir, filepath.FromSlash(rel))
			_, pendErr := os.Stat(pendingPath)
			if newHash != oldHash || pendErr == nil {
				if err := writeFileMkdir(pendingDir, rel, newData); err != nil {
					return res, fmt.Errorf("write new default for %s: %w", rel, err)
				}
				keepPending[rel] = true
				res.Pending = append(res.Pending, PendingDefault{Path: target, New: pendingPath})
			} else {
				res.KeptEdited = append(res.KeptEdited, target)
			}
			continue
		}
		if err := writeFileMkdir(defaultsDir, rel, newData); err != nil {
			return res, err
		}
	}

	// Files obol shipped before but no longer does.
	if haveManifest {
		var gone []string
		for rel := range oldManifest {
			if _, ok := newFiles[rel]; !ok {
				gone = append(gone, rel)
			}
		}
		sort.Strings(gone)
		for _, rel := range gone {
			target := filepath.Join(defaultsDir, filepath.FromSlash(rel))
			disk, err := os.ReadFile(target)
			if err != nil {
				continue
			}
			if sha256Hex(disk) != oldManifest[rel] {
				if !opts.Reset {
					res.KeptRemoved = append(res.KeptRemoved, target)
					continue
				}
				if err := backup(rel, disk); err != nil {
					return res, err
				}
			}
			if err := os.Remove(target); err != nil {
				return res, fmt.Errorf("remove %s: %w", target, err)
			}
		}
	}

	if err := prunePending(pendingDir, keepPending); err != nil {
		return res, err
	}
	if err := writeManifest(defaultsDir, newManifest); err != nil {
		return res, fmt.Errorf("write defaults manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(defaultsDir, stampFile), []byte(stamp), 0o600); err != nil {
		return res, err
	}
	res.Refreshed = true
	return res, nil
}

// prunePending removes pending new-default files that are no longer pending
// (the edit was resolved or reset) and any directories left empty.
func prunePending(pendingDir string, keep map[string]bool) error {
	root, err := os.OpenRoot(pendingDir)
	if err != nil {
		return nil // no pending dir: nothing to prune
	}
	defer root.Close()

	var dirs []string
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if rel != "." {
				dirs = append(dirs, rel)
			}
			return nil
		}
		if !keep[rel] {
			return root.Remove(rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("prune %s: %w", pendingDir, err)
	}
	// Deepest first so parents empty out before they are tried.
	for _, dir := range slices.Backward(dirs) {
		_ = root.Remove(dir) // fails (and is ignored) when not empty
	}
	_ = os.Remove(pendingDir) // likewise
	return nil
}

// readTree reads every regular file under dir, keyed by slash path.
func readTree(dir string) (map[string][]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	files := map[string][]byte{}
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := fs.ReadFile(root.FS(), rel)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
	return files, err
}

// PendingDefaults lists edited defaults files that still have a newer
// default waiting in $CONFIG/defaults.obol-new. Entries the operator
// resolved by copying the new default over are cleaned up.
func PendingDefaults(cfg *config.Config) []PendingDefault {
	pendingDir := filepath.Join(cfg.ConfigDir, pendingDirName)
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	pending, err := readTree(pendingDir)
	if err != nil {
		return nil
	}
	var out []PendingDefault
	keep := map[string]bool{}
	for _, rel := range slices.Sorted(maps.Keys(pending)) {
		target := filepath.Join(defaultsDir, filepath.FromSlash(rel))
		if disk, err := os.ReadFile(target); err == nil && bytes.Equal(pending[rel], disk) {
			continue // resolved: pruned below
		}
		keep[rel] = true
		out = append(out, PendingDefault{Path: target, New: filepath.Join(pendingDir, filepath.FromSlash(rel))})
	}
	_ = prunePending(pendingDir, keep)
	return out
}

// RecordRewrite updates the manifest after obol itself rewrote a file in
// the defaults tree (e.g. `obol upgrade` bumping chart versions), so the
// change is not mistaken for a hand-edit. A file the operator had already
// edited (before differs from what obol last wrote) stays marked as edited.
func RecordRewrite(cfg *config.Config, path string, before, after []byte) {
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	rel, err := filepath.Rel(defaultsDir, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	rel = filepath.ToSlash(rel)
	files, ok := readManifest(defaultsDir)
	if !ok || files[rel] != sha256Hex(before) {
		return
	}
	files[rel] = sha256Hex(after)
	_ = writeManifest(defaultsDir, files)
}

// IsDefaultsPath reports whether a ConfigDir-relative path belongs to the
// defaults tree or its pending new-default sibling (the parts a stack
// backup restores as one unit).
func IsDefaultsPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, root := range []string{"defaults", pendingDirName} {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

// handEdited returns the defaults-relative paths whose content differs from
// what obol last wrote. Without a manifest (a tree written by an older obol)
// edits cannot be told apart from defaults, so every file counts.
func handEdited(defaultsDir string) ([]string, error) {
	files, haveManifest := readManifest(defaultsDir)
	// Root-scoped so a symlink swapped in mid-walk can't redirect a read
	// outside the defaults tree.
	root, err := os.OpenRoot(defaultsDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var edited []string
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if rel == manifestFile || rel == stampFile {
			return nil
		}
		// obol never writes symlinks, so one is a hand-edit (and may point
		// outside the tree, where the root won't follow it).
		if d.Type()&fs.ModeSymlink != 0 {
			edited = append(edited, rel)
			return nil
		}
		data, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		if !haveManifest || files[rel] != sha256Hex(data) {
			edited = append(edited, rel)
		}
		return nil
	})
	return edited, err
}

// ClearForRestore makes way for a defaults tree restored from a stack
// backup, so the archive's files and manifest are not mixed with the local
// tree they replace. The local pending new defaults are removed (they belong
// to the replaced tree). A local tree without hand-edits is removed; one with
// edits is moved to $CONFIG/defaults.bak.<timestamp> and that path returned.
func ClearForRestore(cfg *config.Config) (string, error) {
	if err := os.RemoveAll(filepath.Join(cfg.ConfigDir, pendingDirName)); err != nil {
		return "", err
	}
	defaultsDir := filepath.Join(cfg.ConfigDir, "defaults")
	if _, err := os.Stat(defaultsDir); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	edited, err := handEdited(defaultsDir)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", defaultsDir, err)
	}
	if len(edited) == 0 {
		return "", os.RemoveAll(defaultsDir)
	}
	backupDir := filepath.Join(cfg.ConfigDir, backupDirPrefix+nowFn().UTC().Format("20060102T150405Z"))
	if _, err := os.Stat(backupDir); err == nil {
		backupDir += "-pre-import"
	}
	if err := os.Rename(defaultsDir, backupDir); err != nil {
		return "", fmt.Errorf("set aside %s: %w", defaultsDir, err)
	}
	return backupDir, nil
}

// ReportRefresh prints the outcome of a refresh. resetCmd is the command
// that takes the new defaults (e.g. "obol stack up --reset-defaults").
func ReportRefresh(u *ui.UI, res RefreshResult, resetCmd string) {
	if !res.Refreshed {
		return
	}
	if len(res.BackedUp) > 0 {
		if res.Migrated {
			u.Warnf("Replaced %d defaults file(s) that differed from the new defaults; copies were saved first:", len(res.BackedUp))
			u.Dim("  (this obol version tracks hand-edits to the defaults tree; the previous version did not, so an")
			u.Dim("  older default and a hand-edit could not be told apart — re-apply any edits from the copies)")
		} else {
			u.Warnf("Took the new defaults; %d edited file(s) were backed up first:", len(res.BackedUp))
		}
		for _, b := range res.BackedUp {
			u.Printf("  %s", b.Path)
			u.Dim("    backup: " + b.Backup)
		}
	}
	if len(res.Pending) > 0 {
		u.Warnf("Kept %d hand-edited defaults file(s); the stack is running with your edited version, not the new default:", len(res.Pending))
		for _, p := range res.Pending {
			u.Printf("  %s", p.Path)
			u.Dim("    new default: " + p.New)
		}
		u.Printf("  Compare with: diff <file> <new default>. To take the new defaults (edited files are backed up first): %s", resetCmd)
		u.Printf("  To keep your version and stop this warning, delete the new-default copy.")
	}
	if len(res.KeptRemoved) > 0 {
		u.Warnf("Kept %d hand-edited file(s) that obol no longer ships (delete them if no longer needed):", len(res.KeptRemoved))
		for _, p := range res.KeptRemoved {
			u.Printf("  %s", p)
		}
	}
	if len(res.KeptEdited) > 0 {
		u.Dim(fmt.Sprintf("Kept %d hand-edited defaults file(s) (no new default for them)", len(res.KeptEdited)))
	}
	if len(res.BackedUp)+len(res.Pending)+len(res.KeptRemoved)+len(res.KeptEdited) == 0 {
		u.Dim("Refreshed default infrastructure templates from embedded assets")
	}
}
