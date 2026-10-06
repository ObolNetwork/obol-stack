package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallMethod is how the running obol binary was installed. The CLI never
// replaces its own binary; this only selects which upgrade command to print.
type InstallMethod string

const (
	InstallHomebrew InstallMethod = "homebrew" // brew cask / formula
	InstallSystem   InstallMethod = "system"   // deb/rpm package (/usr/bin)
	InstallScript   InstallMethod = "script"   // obolup.sh into the obol bin dir
	InstallDev      InstallMethod = "dev"      // go run / source build / anything else
)

// DetectInstallMethod classifies an (already symlink-resolved) executable
// path. binDir is the obol bin dir (OBOL_BIN_DIR / ~/.local/bin).
func DetectInstallMethod(exe, binDir string) InstallMethod {
	exe = filepath.ToSlash(filepath.Clean(exe))

	for _, marker := range []string{"/opt/homebrew/", "/usr/local/Cellar/", "/usr/local/Caskroom/", "/home/linuxbrew/", "/Caskroom/", "/Cellar/"} {
		if strings.Contains(exe, marker) {
			return InstallHomebrew
		}
	}

	dir := filepath.ToSlash(filepath.Dir(exe))
	if dir == "/usr/bin" {
		return InstallSystem
	}

	if binDir != "" {
		bd := filepath.ToSlash(filepath.Clean(binDir))
		if resolved, err := filepath.EvalSymlinks(binDir); err == nil {
			if filepath.ToSlash(filepath.Clean(resolved)) == dir {
				return InstallScript
			}
		}

		if dir == bd {
			return InstallScript
		}
	}

	if home, err := os.UserHomeDir(); err == nil && dir == filepath.ToSlash(filepath.Join(home, ".local", "bin")) {
		return InstallScript
	}

	return InstallDev
}

// CurrentInstallMethod detects the method for the running binary.
func CurrentInstallMethod(binDir string) InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return InstallDev
	}

	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	return DetectInstallMethod(exe, binDir)
}

// CLIUpgradeCommand returns the command a user should run to move to tag
// (e.g. "v0.15.0") for the given install method.
func CLIUpgradeCommand(m InstallMethod, tag string) string {
	switch m {
	case InstallHomebrew:
		return "brew upgrade --cask obol"
	case InstallSystem:
		// deb/rpm are attached to GitHub releases (no apt/dnf repo yet), so
		// install the new package file directly.
		ver := strings.TrimPrefix(tag, "v")
		base := fmt.Sprintf("https://github.com/ObolNetwork/obol-stack/releases/download/%s/obol_%s_linux_%s", tag, ver, runtime.GOARCH)

		if _, err := exec.LookPath("dnf"); err == nil {
			return "sudo dnf install " + base + ".rpm"
		}

		if _, err := exec.LookPath("rpm"); err == nil {
			if _, err := exec.LookPath("dpkg"); err != nil {
				return "sudo rpm -U " + base + ".rpm"
			}
		}

		return fmt.Sprintf("curl -fsSLO %s.deb && sudo apt install ./obol_%s_linux_%s.deb", base, ver, runtime.GOARCH)
	case InstallScript:
		return "bash <(curl -fsSL https://stack.obol.org)"
	default:
		return "git pull && just build   (development build; or install a release: bash <(curl -fsSL https://stack.obol.org))"
	}
}
