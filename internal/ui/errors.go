package ui

import "fmt"

// FormatError renders a structured error with an optional hint.
//
//	✗ something went wrong
//	  Hint: check your configuration
func (u *UI) FormatError(err error, hint string) {
	u.Error(err.Error())

	if hint != "" {
		fmt.Fprintf(u.stderr, "  %s\n", dimStyle.Render(hint))
	}
}

// FormatActionableError renders an error with a concrete next-step command.
//
//	✗ Stack not running
//	  Run: obol stack up
func (u *UI) FormatActionableError(err error, action string) {
	u.Error(err.Error())

	if action != "" {
		fmt.Fprintf(u.stderr, "  Run: %s\n", boldStyle.Render(action))
	}
}
