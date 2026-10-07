package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// BrowserEnv is everything the open-a-browser decision depends on. It is a
// plain struct so the decision (PlanBrowserOpen) is a pure, table-testable
// function; DefaultBrowserEnv fills it from the real process.
type BrowserEnv struct {
	// GOOS is runtime.GOOS (or a test override).
	GOOS string
	// Getenv reads an environment variable.
	Getenv func(string) string
	// LookPath reports whether a binary is on PATH (exec.LookPath shape).
	LookPath func(string) (string, error)
	// ProcVersion is the contents of /proc/version (Linux only; "" elsewhere).
	ProcVersion string
	// Interactive is true when stdout is a TTY, output is human (not JSON)
	// and OBOL_NONINTERACTIVE is not "true".
	Interactive bool
	// Disabled is true when the operator passed the global --no-open flag.
	Disabled bool
}

// BrowserPlan is the outcome of PlanBrowserOpen: either a command to start
// (Open == true) or the reason the browser is left alone.
type BrowserPlan struct {
	Open   bool
	Argv   []string
	Reason string
}

// envSet treats "", "0" and "false" as unset so CI=false / OBOL_NO_BROWSER=0
// behave the way people expect.
func envSet(getenv func(string) string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(key))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// IsWSL reports whether the environment looks like Windows Subsystem for Linux.
func (e BrowserEnv) IsWSL() bool {
	if e.GOOS != "linux" {
		return false
	}
	if strings.TrimSpace(e.Getenv("WSL_DISTRO_NAME")) != "" {
		return true
	}
	return strings.Contains(strings.ToLower(e.ProcVersion), "microsoft")
}

// PlanBrowserOpen decides whether (and how) to open url in a browser. It never
// opens anything itself. Rules, in order:
//
//   - --no-open / OBOL_NO_BROWSER → skip
//   - non-interactive (no TTY, JSON output, OBOL_NONINTERACTIVE=true) → skip
//   - CI → skip
//   - over SSH (SSH_CONNECTION / SSH_TTY) → skip (the browser would open on
//     the remote host, not in front of the operator)
//   - WSL → wslview when on PATH, else explorer.exe
//   - macOS → open
//   - Windows → rundll32 url.dll,FileProtocolHandler
//   - other Unix → xdg-open, only when DISPLAY or WAYLAND_DISPLAY is set
func PlanBrowserOpen(env BrowserEnv, url string) BrowserPlan {
	getenv := env.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	lookPath := env.LookPath
	if lookPath == nil {
		lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	}
	env.Getenv = getenv

	switch {
	case url == "":
		return BrowserPlan{Reason: "no URL"}
	case env.Disabled || envSet(getenv, "OBOL_NO_BROWSER"):
		return BrowserPlan{Reason: "disabled (--no-open / OBOL_NO_BROWSER)"}
	case !env.Interactive:
		return BrowserPlan{Reason: "non-interactive"}
	case envSet(getenv, "CI"):
		return BrowserPlan{Reason: "CI"}
	case getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "":
		return BrowserPlan{Reason: "SSH session"}
	}

	if env.IsWSL() {
		if _, err := lookPath("wslview"); err == nil {
			return BrowserPlan{Open: true, Argv: []string{"wslview", url}}
		}
		return BrowserPlan{Open: true, Argv: []string{"explorer.exe", url}}
	}

	switch env.GOOS {
	case "darwin":
		return BrowserPlan{Open: true, Argv: []string{"open", url}}
	case "windows":
		return BrowserPlan{Open: true, Argv: []string{"rundll32", "url.dll,FileProtocolHandler", url}}
	}

	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return BrowserPlan{Reason: "no display (headless)"}
	}
	return BrowserPlan{Open: true, Argv: []string{"xdg-open", url}}
}

// DefaultBrowserEnv builds a BrowserEnv from the real process and this UI's
// output mode.
func (u *UI) DefaultBrowserEnv() BrowserEnv {
	env := BrowserEnv{
		GOOS:        runtime.GOOS,
		Getenv:      os.Getenv,
		LookPath:    exec.LookPath,
		Interactive: u.isInteractive(),
		Disabled:    u.noBrowser,
	}
	if env.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/version"); err == nil {
			env.ProcVersion = string(data)
		}
	}
	return env
}

// SetNoBrowser disables every automatic browser open for this UI (the global
// --no-open flag).
func (u *UI) SetNoBrowser(v bool) { u.noBrowser = v }

// startBrowser is swapped in tests so nothing is ever launched.
var startBrowser = func(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OpenBrowser opens url in the operator's browser when PlanBrowserOpen allows
// it and reports whether it did. It prints nothing: callers always print the
// URL themselves (see OpenURL for the print-and-maybe-open helper).
func (u *UI) OpenBrowser(url string) bool {
	plan := PlanBrowserOpen(u.DefaultBrowserEnv(), url)
	if !plan.Open {
		return false
	}
	return startBrowser(plan.Argv) == nil
}

// OpenURL always prints "label: url" and, when autoOpen is set and the
// environment allows it, opens the URL in a browser. Reports whether a
// browser was opened. Human output only — in JSON mode it lands on stderr
// like every other UI line, so stdout stays clean.
func (u *UI) OpenURL(label, url string, autoOpen bool) bool {
	if url == "" {
		return false
	}
	u.Printf("  %s %s", dimStyle.Render(label+":"), url)
	if !autoOpen {
		return false
	}
	if u.OpenBrowser(url) {
		u.Dim("  (opened in your browser)")
		return true
	}
	return false
}
