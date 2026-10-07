package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Source says where a resolved tool came from.
type Source string

const (
	SourceEnv     Source = "env"     // OBOL_<TOOL> override
	SourceManaged Source = "managed" // obol bin dir (installed by obol or obolup.sh)
	SourcePath    Source = "path"    // compatible binary on $PATH
	SourceMissing Source = "missing" // nothing usable found
)

// Resolution is the outcome of resolving one binary tool.
type Resolution struct {
	Name   string
	Path   string // binary to exec; for SourceMissing, the managed path it would be installed to
	Source Source
	// Version is filled when the resolver had to probe it ($PATH candidates).
	Version string
	// Rejected/RejectedVersion describe a $PATH binary skipped as incompatible.
	Rejected        string
	RejectedVersion string
}

// Found reports whether a usable binary was resolved.
func (r Resolution) Found() bool { return r.Source != SourceMissing }

// MissingHint is the user-facing remedy for a missing tool.
const MissingHint = "run 'obol upgrade' to install missing tools"

// MissingError formats the standard "tool not found" error.
func MissingError(name string) error {
	return fmt.Errorf("%s not found (looked in %s, the obol bin dir and $PATH); %s", name, EnvVar(name), MissingHint)
}

// Path resolves a binary tool and returns the path to exec. It never fails:
// when nothing usable exists it returns <binDir>/<name>, so callers keep
// their existing "not found"/exec error paths. Use Resolve for details.
func Path(binDir, name string) string {
	return Resolve(binDir, name).Path
}

// Resolve resolves a binary tool in order:
//
//  1. OBOL_<TOOL> env override (used as-is, no version check);
//  2. <binDir>/<name> if it is an executable file (obol-managed);
//  3. the first <name> on $PATH whose version is compatible with the
//     manifest pin (see the compat policies in manifest.yaml).
func Resolve(binDir, name string) Resolution {
	managed := filepath.Join(binDir, name)

	if v := strings.TrimSpace(os.Getenv(EnvVar(name))); v != "" {
		p := v
		if !strings.ContainsRune(v, os.PathSeparator) {
			if lp, err := exec.LookPath(v); err == nil {
				p = lp
			}
		}

		return Resolution{Name: name, Path: p, Source: SourceEnv}
	}

	if isExecutable(managed) {
		return Resolution{Name: name, Path: managed, Source: SourceManaged}
	}

	res := Resolution{Name: name, Path: managed, Source: SourceMissing}
	tool, known := Default().Get(name)
	binDirClean := cleanAbs(binDir)

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || cleanAbs(dir) == binDirClean {
			continue
		}

		cand := filepath.Join(dir, name)
		if !isExecutable(cand) {
			continue
		}

		if !known || tool.Compat == CompatAny {
			return Resolution{Name: name, Path: cand, Source: SourcePath}
		}

		ver, err := ProbeVersion(tool, cand)
		if err == nil && Compatible(tool.Compat, tool.Version, ver) {
			return Resolution{Name: name, Path: cand, Source: SourcePath, Version: ver}
		}

		if res.Rejected == "" {
			res.Rejected, res.RejectedVersion = cand, ver
		}
	}

	return res
}

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}

	return filepath.Clean(p)
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p) // follows symlinks: a dangling link is "missing"
	if err != nil || fi.IsDir() {
		return false
	}

	return fi.Mode()&0o111 != 0
}

// --- version probing -------------------------------------------------------

var genericVersionRE = regexp.MustCompile(`v?([0-9]+\.[0-9]+\.[0-9]+)`)

type probeKey struct {
	path  string
	size  int64
	mtime time.Time
}

var probeCache sync.Map // probeKey -> string

// ProbeVersion runs the tool's version command and extracts x.y.z.
// Results are cached per (path, size, mtime) for the life of the process.
func ProbeVersion(t Tool, path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	key := probeKey{path: path, size: fi.Size(), mtime: fi.ModTime()}
	if v, ok := probeCache.Load(key); ok {
		return v.(string), nil
	}

	args := t.VersionArgs
	if len(args) == 0 {
		args = []string{"version"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	// Never let a version probe talk to a cluster or prompt.
	cmd.Env = append(os.Environ(), "KUBECONFIG="+os.DevNull)

	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("%s %s: %w", path, strings.Join(args, " "), err)
	}

	v, err := ParseVersion(t.VersionRegex, string(out))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}

	probeCache.Store(key, v)

	return v, nil
}

// ParseVersion extracts the first x.y.z from out using pattern (whose first
// capture group is the version) or a generic semver pattern.
func ParseVersion(pattern, out string) (string, error) {
	re := genericVersionRE
	if pattern != "" {
		var err error
		if re, err = regexp.Compile(pattern); err != nil {
			return "", fmt.Errorf("bad version regex %q: %w", pattern, err)
		}
	}

	m := re.FindStringSubmatch(out)
	if len(m) < 2 {
		return "", fmt.Errorf("no version found in %q", strings.TrimSpace(firstLine(out)))
	}

	return m[1], nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}

	return s
}

// semver parses "v1.2.3[-pre][+meta]" into [major, minor, patch].
func semver(v string) ([3]int, bool) {
	var out [3]int

	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}

	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return out, false
	}

	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}

		out[i] = n
	}

	return out, true
}

// CompareVersions returns -1, 0 or 1. Unparseable versions sort lowest.
func CompareVersions(a, b string) int {
	av, aok := semver(a)
	bv, bok := semver(b)

	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}

	for i := range 3 {
		if av[i] < bv[i] {
			return -1
		}

		if av[i] > bv[i] {
			return 1
		}
	}

	return 0
}

// Compatible reports whether version `have` satisfies policy for pin `want`.
//
//	skew1: same major, |minor(have) - minor(want)| <= 1   (kubectl skew policy)
//	minor: same major, minor(have) >= minor(want)          (helm 4.x, k3d 5.x, helmfile 1.x)
//	any:   always
func Compatible(policy, want, have string) bool {
	if policy == CompatAny {
		return true
	}

	w, wok := semver(want)
	h, hok := semver(have)

	if !wok || !hok || w[0] != h[0] {
		return false
	}

	switch policy {
	case CompatSkew1:
		d := h[1] - w[1]
		return d >= -1 && d <= 1
	case CompatMinor:
		return h[1] >= w[1]
	default:
		return false
	}
}

// CompatRange describes the versions policy accepts for pin want, for
// user-facing notes (e.g. "v4.x >= 4.3" when a $PATH helm 3 is skipped).
// Returns "" for CompatAny or an unparseable pin.
func CompatRange(policy, want string) string {
	w, ok := semver(want)
	if !ok {
		return ""
	}

	switch policy {
	case CompatSkew1:
		return fmt.Sprintf("v%d.%d (±1 minor)", w[0], w[1])
	case CompatMinor:
		return fmt.Sprintf("v%d.x >= %d.%d", w[0], w[0], w[1])
	default:
		return ""
	}
}
