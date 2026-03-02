package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Styles using Obol brand colors (see brand.go for hex values).
// Lipgloss auto-degrades to 256/16 colors on older terminals.
var (
	infoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolGreen)).Bold(true)
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolDarkGreen)).Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolAmber)).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolRed)).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolMuted))
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

// Info prints: ==> message (blue arrow, matching obolup.sh log_info).
func (u *UI) Info(msg string) {
	if u.quiet {
		return
	}
	fmt.Fprintf(u.stdout, "%s %s\n", infoStyle.Render("==>"), msg)
}

// Infof prints a formatted info message.
func (u *UI) Infof(format string, args ...any) {
	u.Info(fmt.Sprintf(format, args...))
}

// Success prints: ✓ message (green check, matching obolup.sh log_success).
func (u *UI) Success(msg string) {
	if u.quiet {
		return
	}
	fmt.Fprintf(u.stdout, "  %s %s\n", successStyle.Render("✓"), msg)
}

// Successf prints a formatted success message.
func (u *UI) Successf(format string, args ...any) {
	u.Success(fmt.Sprintf(format, args...))
}

// Warn prints: ! message (yellow bang, matching obolup.sh log_warn).
// Not suppressed by quiet mode.
func (u *UI) Warn(msg string) {
	fmt.Fprintf(u.stderr, "  %s %s\n", warnStyle.Render("!"), msg)
}

// Warnf prints a formatted warning message.
func (u *UI) Warnf(format string, args ...any) {
	u.Warn(fmt.Sprintf(format, args...))
}

// Error prints: ✗ message (red x, matching obolup.sh log_error).
// Not suppressed by quiet mode.
func (u *UI) Error(msg string) {
	fmt.Fprintf(u.stderr, "%s %s\n", errorStyle.Render("✗"), msg)
}

// Errorf prints a formatted error message.
func (u *UI) Errorf(format string, args ...any) {
	u.Error(fmt.Sprintf(format, args...))
}

// Print writes a plain message to stdout (no prefix, no color).
func (u *UI) Print(msg string) {
	if u.quiet {
		return
	}
	fmt.Fprintln(u.stdout, msg)
}

// Printf writes a formatted message to stdout.
func (u *UI) Printf(format string, args ...any) {
	if u.quiet {
		return
	}
	fmt.Fprintf(u.stdout, format+"\n", args...)
}

// Detail prints an indented key-value pair: "  key: value".
func (u *UI) Detail(key, value string) {
	if u.quiet {
		return
	}
	fmt.Fprintf(u.stdout, "  %s: %s\n", dimStyle.Render(key), value)
}

// Dim prints dimmed/gray text for secondary information.
func (u *UI) Dim(msg string) {
	if u.quiet {
		return
	}
	fmt.Fprintln(u.stdout, dimStyle.Render(msg))
}

// Bold prints bold text.
func (u *UI) Bold(msg string) {
	if u.quiet {
		return
	}
	fmt.Fprintln(u.stdout, boldStyle.Render(msg))
}

// Blank prints an empty line.
func (u *UI) Blank() {
	if u.quiet {
		return
	}
	fmt.Fprintln(u.stdout)
}
