package update

import (
	"strings"
	"testing"
)

func TestDetectInstallMethod(t *testing.T) {
	binDir := "/home/alice/.local/bin"

	cases := []struct {
		exe  string
		want InstallMethod
	}{
		{"/opt/homebrew/Caskroom/obol/0.15.0/obol", InstallHomebrew},
		{"/opt/homebrew/bin/obol", InstallHomebrew},
		{"/usr/local/Caskroom/obol/0.15.0/obol", InstallHomebrew},
		{"/usr/local/Cellar/obol/0.15.0/bin/obol", InstallHomebrew},
		{"/home/linuxbrew/.linuxbrew/bin/obol", InstallHomebrew},
		{"/usr/bin/obol", InstallSystem},
		{"/home/alice/.local/bin/obol", InstallScript},
		{"/home/alice/.cache/go-build/ab/obol", InstallDev},
		{"/home/alice/src/obol-stack/.workspace/bin/obol", InstallDev},
		{"/usr/local/bin/obol", InstallDev},
	}

	for _, c := range cases {
		if got := DetectInstallMethod(c.exe, binDir); got != c.want {
			t.Errorf("DetectInstallMethod(%q) = %s, want %s", c.exe, got, c.want)
		}
	}

	// A custom OBOL_BIN_DIR counts as a script install.
	if got := DetectInstallMethod("/opt/obol/bin/obol", "/opt/obol/bin"); got != InstallScript {
		t.Errorf("custom bin dir: got %s", got)
	}
}

func TestCLIUpgradeCommand(t *testing.T) {
	if got := CLIUpgradeCommand(InstallHomebrew, "v0.15.0"); got != "brew upgrade --cask obol" {
		t.Errorf("homebrew: %q", got)
	}

	if got := CLIUpgradeCommand(InstallScript, "v0.15.0"); !strings.Contains(got, "https://stack.obol.org") {
		t.Errorf("script: %q", got)
	}

	if got := CLIUpgradeCommand(InstallSystem, "v0.15.0"); !strings.Contains(got, "/releases/download/v0.15.0/obol_0.15.0_linux_") {
		t.Errorf("system: %q", got)
	}

	if got := CLIUpgradeCommand(InstallDev, "v0.15.0"); !strings.Contains(got, "just build") {
		t.Errorf("dev: %q", got)
	}
}
