// Package ui provides consistent terminal output primitives for the obol CLI.
//
// Pass a *UI through your call chain instead of using fmt directly.
// Output adapts to the environment: colors and spinners in interactive
// terminals, plain text when piped or in CI.
package ui

import (
	"io"
	"os"
	"sync"

	"github.com/mattn/go-isatty"
)

// UI provides consistent terminal output primitives.
type UI struct {
	verbose bool
	quiet   bool
	isTTY   bool
	stdout  io.Writer
	stderr  io.Writer
	mu      sync.Mutex
}

// New creates a UI instance. When verbose is true, subprocess output is
// streamed live instead of captured behind a spinner.
func New(verbose bool) *UI {
	return &UI{
		verbose: verbose,
		isTTY:   isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()),
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}
}

// NewWithOptions creates a UI instance with full control over verbose and quiet modes.
// Quiet mode suppresses all output except errors.
func NewWithOptions(verbose, quiet bool) *UI {
	u := New(verbose)
	u.quiet = quiet
	return u
}

// IsVerbose returns whether verbose mode is enabled.
func (u *UI) IsVerbose() bool { return u.verbose }

// IsQuiet returns whether quiet mode is enabled.
func (u *UI) IsQuiet() bool { return u.quiet }

// IsTTY returns whether stdout is an interactive terminal.
func (u *UI) IsTTY() bool { return u.isTTY }
