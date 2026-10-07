package ui

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func pathWith(bins ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(bins, name) {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestPlanBrowserOpen(t *testing.T) {
	const url = "http://obol.stack"
	tests := []struct {
		name     string
		env      BrowserEnv
		wantOpen bool
		wantArgv []string
		reason   string
	}{
		{
			name:     "macOS interactive",
			env:      BrowserEnv{GOOS: "darwin", Interactive: true, Getenv: envMap(nil)},
			wantOpen: true,
			wantArgv: []string{"open", url},
		},
		{
			name:     "windows",
			env:      BrowserEnv{GOOS: "windows", Interactive: true, Getenv: envMap(nil)},
			wantOpen: true,
			wantArgv: []string{"rundll32", "url.dll,FileProtocolHandler", url},
		},
		{
			name:     "linux desktop X11",
			env:      BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(map[string]string{"DISPLAY": ":0"})},
			wantOpen: true,
			wantArgv: []string{"xdg-open", url},
		},
		{
			name:     "linux desktop wayland",
			env:      BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})},
			wantOpen: true,
			wantArgv: []string{"xdg-open", url},
		},
		{
			name:   "headless linux",
			env:    BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(nil)},
			reason: "headless",
		},
		{
			name:     "WSL via env with wslview",
			env:      BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}), LookPath: pathWith("wslview")},
			wantOpen: true,
			wantArgv: []string{"wslview", url},
		},
		{
			name:     "WSL via /proc/version without wslview",
			env:      BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(nil), ProcVersion: "Linux version 5.15.153.1-microsoft-standard-WSL2"},
			wantOpen: true,
			wantArgv: []string{"explorer.exe", url},
		},
		{
			name:   "SSH_CONNECTION",
			env:    BrowserEnv{GOOS: "darwin", Interactive: true, Getenv: envMap(map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"})},
			reason: "SSH",
		},
		{
			name:   "SSH_TTY on linux desktop",
			env:    BrowserEnv{GOOS: "linux", Interactive: true, Getenv: envMap(map[string]string{"SSH_TTY": "/dev/pts/1", "DISPLAY": ":0"})},
			reason: "SSH",
		},
		{
			name:   "CI",
			env:    BrowserEnv{GOOS: "darwin", Interactive: true, Getenv: envMap(map[string]string{"CI": "true"})},
			reason: "CI",
		},
		{
			name:     "CI=false is not CI",
			env:      BrowserEnv{GOOS: "darwin", Interactive: true, Getenv: envMap(map[string]string{"CI": "false"})},
			wantOpen: true,
			wantArgv: []string{"open", url},
		},
		{
			name:   "JSON / non-TTY",
			env:    BrowserEnv{GOOS: "darwin", Interactive: false, Getenv: envMap(nil)},
			reason: "non-interactive",
		},
		{
			name:   "OBOL_NO_BROWSER",
			env:    BrowserEnv{GOOS: "darwin", Interactive: true, Getenv: envMap(map[string]string{"OBOL_NO_BROWSER": "1"})},
			reason: "disabled",
		},
		{
			name:   "--no-open",
			env:    BrowserEnv{GOOS: "darwin", Interactive: true, Disabled: true, Getenv: envMap(nil)},
			reason: "disabled",
		},
		{
			name:   "nil getenv is safe",
			env:    BrowserEnv{GOOS: "linux", Interactive: true},
			reason: "headless",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanBrowserOpen(tt.env, url)
			if got.Open != tt.wantOpen {
				t.Fatalf("Open = %v, want %v (reason %q)", got.Open, tt.wantOpen, got.Reason)
			}
			if tt.wantOpen && !slices.Equal(got.Argv, tt.wantArgv) {
				t.Errorf("Argv = %v, want %v", got.Argv, tt.wantArgv)
			}
			if !tt.wantOpen && !strings.Contains(got.Reason, tt.reason) {
				t.Errorf("Reason = %q, want it to mention %q", got.Reason, tt.reason)
			}
		})
	}
}

func TestOpenURL_AlwaysPrintsAndNeverOpensWhenNotInteractive(t *testing.T) {
	called := false
	orig := startBrowser
	startBrowser = func([]string) error { called = true; return nil }
	t.Cleanup(func() { startBrowser = orig })

	var out bytes.Buffer
	u := NewForTest(&out, &out) // not a TTY → non-interactive
	if u.OpenURL("Dashboard", "http://obol.stack", true) {
		t.Fatal("OpenURL reported opened for a non-interactive UI")
	}
	if called {
		t.Fatal("browser launched for a non-interactive UI")
	}
	if !strings.Contains(out.String(), "http://obol.stack") {
		t.Fatalf("URL not printed: %q", out.String())
	}
}
