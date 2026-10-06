package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

type lipglossStyle = lipgloss.Style

// noColorEnv is the https://no-color.org convention: when set to any
// non-empty value, CLI output must not contain ANSI color.
const noColorEnv = "NO_COLOR"

func init() {
	if ColorDisabled() {
		disableColor()
	}
}

// ColorDisabled reports whether the user opted out of colored output via
// NO_COLOR (any non-empty value).
func ColorDisabled() bool {
	return os.Getenv(noColorEnv) != ""
}

// disableColor strips foreground colors from every package style. Text
// attributes such as bold are kept: NO_COLOR only governs color.
func disableColor() {
	for _, s := range []*lipglossStyle{
		&infoStyle, &successStyle, &warnStyle, &errorStyle, &dimStyle, &boldStyle,
		&bannerStyle, &taglineStyle, &accentStyle,
	} {
		*s = s.UnsetForeground().UnsetBackground()
	}
}
