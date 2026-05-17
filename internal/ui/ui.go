// Package ui provides consistent terminal output primitives for the obol CLI.
//
// Pass a *UI through your call chain instead of using fmt directly.
// Output adapts to the environment: colors and spinners in interactive
// terminals, plain text when piped or in CI.
//
// When OutputJSON mode is active, human-readable output (Info, Success, Print,
// etc.) is redirected to stderr so that stdout contains only clean JSON. Use
// the JSON() method to emit structured data.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mattn/go-isatty"
)

// OutputMode controls the format of command output.
type OutputMode int

const (
	// OutputHuman is the default human-readable output mode.
	OutputHuman OutputMode = iota
	// OutputJSON produces machine-readable JSON on stdout.
	OutputJSON
)

// ParseOutputMode converts a string to an OutputMode.
func ParseOutputMode(s string) (OutputMode, error) {
	switch s {
	case "", "human":
		return OutputHuman, nil
	case "json":
		return OutputJSON, nil
	default:
		return OutputHuman, fmt.Errorf("invalid output mode %q (use: human, json)", s)
	}
}

// UI provides consistent terminal output primitives.
type UI struct {
	verbose bool
	quiet   bool
	isTTY   bool
	output  OutputMode
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

// NewWithAllOptions creates a UI instance with full control over all modes.
func NewWithAllOptions(verbose, quiet bool, output OutputMode) *UI {
	u := NewWithOptions(verbose, quiet)
	u.output = output
	if output == OutputJSON {
		// In JSON mode, redirect human output to stderr so stdout is clean JSON.
		u.stdout = os.Stderr
	}
	return u
}

// NewForTest creates a UI instance that writes to the supplied writers.
// Use bytes.Buffer for stdout/stderr to capture output in unit tests.
func NewForTest(stdout, stderr io.Writer) *UI {
	return &UI{
		stdout: stdout,
		stderr: stderr,
	}
}

// IsVerbose returns whether verbose mode is enabled.
func (u *UI) IsVerbose() bool { return u.verbose }

// IsQuiet returns whether quiet mode is enabled.
func (u *UI) IsQuiet() bool { return u.quiet }

// IsTTY returns whether stdout is an interactive terminal.
func (u *UI) IsTTY() bool { return u.isTTY }

// IsJSON returns whether JSON output mode is active.
func (u *UI) IsJSON() bool { return u.output == OutputJSON }

// JSON writes v as indented JSON to the real stdout (os.Stdout).
// This bypasses the stderr redirect so agents always get JSON on stdout.
func (u *UI) JSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
