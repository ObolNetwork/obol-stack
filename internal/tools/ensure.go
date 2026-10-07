package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Tool states reported by Status.
const (
	StateOK       = "ok"
	StateOutdated = "outdated"
	StateNewer    = "newer"
	StateMissing  = "missing"
	StateUnknown  = "unknown" // present but version could not be read
)

// Status is the check-only view of one tool, for `obol update`.
type Status struct {
	Name      string `json:"name"`
	Wanted    string `json:"wanted"`
	Installed string `json:"installed,omitempty"`
	Path      string `json:"path,omitempty"`
	Source    Source `json:"source"`
	State     string `json:"state"`
	Required  bool   `json:"required"`
	Note      string `json:"note,omitempty"`
}

// NeedsAction reports whether `obol upgrade` would change this tool.
// Only obol-managed copies (or missing tools) are ever touched.
func (s Status) NeedsAction() bool {
	if s.State == StateMissing {
		return true
	}

	return s.Source == SourceManaged && (s.State == StateOutdated || s.State == StateUnknown)
}

// Status reports every manifest tool.
func (in *Installer) Status() []Status {
	out := make([]Status, 0, len(in.Manifest.Tools))
	for _, t := range in.Manifest.Tools {
		out = append(out, in.status(t))
	}

	return out
}

func (in *Installer) status(t Tool) Status {
	s := Status{Name: t.Name, Wanted: t.Version, Required: t.Required}

	if t.IsPlugin() {
		dir, err := in.HelmPluginsDir()
		if err != nil {
			s.Source, s.State, s.Note = SourceMissing, StateMissing, "needs helm"
			return s
		}

		found := FindPlugins(dir, t.PluginName)
		if len(found) == 0 {
			s.Source, s.State, s.Path = SourceMissing, StateMissing, filepath.Join(dir, t.Name)
			return s
		}

		s.Source, s.Path, s.Installed = SourceManaged, found[0].Dir, found[0].Version
		s.State = compareState(s.Installed, t.Version)

		return s
	}

	r := Resolve(in.BinDir, t.Name)
	s.Source, s.Path, s.Installed = r.Source, r.Path, r.Version

	if !r.Found() {
		s.State = StateMissing
		if r.Rejected != "" {
			s.Note = fmt.Sprintf("ignored incompatible %s (v%s)", r.Rejected, r.RejectedVersion)
			if want := CompatRange(t.Compat, t.Version); want != "" {
				s.Note += "; obol needs " + want
			}
		}

		return s
	}

	if s.Installed == "" {
		v, err := ProbeVersion(t, r.Path)
		if err != nil {
			s.State = StateUnknown
			return s
		}

		s.Installed = v
	}

	s.State = compareState(s.Installed, t.Version)
	if r.Source != SourceManaged && s.State != StateOK {
		s.Note = "not managed by obol"
	}

	return s
}

func compareState(have, want string) string {
	switch c := CompareVersions(have, want); {
	case have == "":
		return StateUnknown
	case c < 0:
		return StateOutdated
	case c > 0:
		return StateNewer
	default:
		return StateOK
	}
}

// Result records what Ensure did for one tool.
type Result struct {
	Name      string
	From      string // previous managed version, "" if newly installed
	To        string
	Installed bool
	Err       error
}

// EnsureOptions controls Ensure.
type EnsureOptions struct {
	// Names limits the tools considered (default: all manifest tools).
	Names []string
	// Upgrade also replaces outdated obol-managed copies. Without it only
	// missing tools are installed. Tools resolved from $PATH or an OBOL_<TOOL>
	// override are never modified, and newer managed copies are never
	// downgraded.
	Upgrade bool
	// OnInstall is called before each download, OnDone after each attempt.
	OnInstall func(s Status)
	OnDone    func(r Result)
}

// Ensure installs missing (and with Upgrade, outdated managed) tools.
// It keeps going after a failure and returns the joined errors.
func (in *Installer) Ensure(ctx context.Context, opts EnsureOptions) ([]Result, error) {
	names := opts.Names
	if len(names) == 0 {
		names = in.Manifest.Names()
	}

	var (
		results []Result
		errs    []error
	)

	for _, name := range names {
		t, ok := in.Manifest.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("unknown tool %q", name))
			continue
		}

		s := in.status(t)

		install := s.State == StateMissing || (opts.Upgrade && s.NeedsAction())
		if !install {
			continue
		}

		if opts.OnInstall != nil {
			opts.OnInstall(s)
		}

		res := Result{Name: name, From: s.Installed, To: t.Version}
		if err := in.Install(ctx, name); err != nil {
			res.Err = err
			errs = append(errs, err)
		} else {
			res.Installed = true
		}

		if opts.OnDone != nil {
			opts.OnDone(res)
		}

		results = append(results, res)
	}

	return results, errors.Join(errs...)
}

// EnsureUI runs Ensure and prints progress. Used by `obol stack init/up`
// (missing required tools only) and `obol upgrade` (all tools, Upgrade).
func EnsureUI(ctx context.Context, u *ui.UI, binDir string, opts EnsureOptions) error {
	in := NewInstaller(binDir)
	headerShown := false

	opts.OnInstall = func(s Status) {
		if !headerShown {
			u.Infof("Installing tools into %s", binDir)
			headerShown = true
		}

		if s.State == StateMissing {
			u.Printf("  • %s v%s ...", s.Name, s.Wanted)
		} else {
			u.Printf("  • %s v%s → v%s ...", s.Name, s.Installed, s.Wanted)
		}
	}
	opts.OnDone = func(r Result) {
		if r.Installed {
			u.Successf("%s v%s installed (sha256 verified)", r.Name, r.To)
		} else {
			u.Warnf("%s v%s: %v", r.Name, r.To, r.Err)
		}
	}

	results, err := in.Ensure(ctx, opts)

	if err != nil {
		return fmt.Errorf("tool install failed: %w\nCheck your network connection and retry, or install the tool yourself and point %s at it", err, envHint(results))
	}

	return nil
}

func envHint(results []Result) string {
	var vars []string

	for _, r := range results {
		if r.Err != nil {
			vars = append(vars, EnvVar(r.Name))
		}
	}

	if len(vars) == 0 {
		return "OBOL_<TOOL>"
	}

	return strings.Join(vars, "/")
}
