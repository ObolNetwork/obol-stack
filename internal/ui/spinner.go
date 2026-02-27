package ui

import (
	"fmt"
	"time"
)

// Braille spinner frames.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// RunWithSpinner executes fn while displaying a spinner with msg.
//
// On success: replaces spinner with "✓ msg (duration)".
// On failure: replaces spinner with "✗ msg", caller handles error display.
// In verbose mode or non-TTY: prints the message and runs fn without animation.
func (u *UI) RunWithSpinner(msg string, fn func() error) error {
	start := time.Now()

	if !u.isTTY || u.verbose {
		u.Info(msg)
		err := fn()
		elapsed := time.Since(start).Round(time.Second)
		if err == nil {
			u.Successf("%s (%s)", msg, elapsed)
		}
		return err
	}

	// Animated spinner on TTY.
	u.mu.Lock()
	done := make(chan struct{})
	frame := 0
	go func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				u.mu.Lock()
				// Move to start of line, clear it, write spinner frame + message.
				fmt.Fprintf(u.stdout, "\r\033[K%s %s",
					infoStyle.Render(spinnerFrames[frame%len(spinnerFrames)]),
					msg)
				frame++
				u.mu.Unlock()
			}
		}
	}()
	u.mu.Unlock()

	err := fn()
	close(done)

	elapsed := time.Since(start).Round(time.Second)

	// Clear spinner line and print final status.
	u.mu.Lock()
	fmt.Fprintf(u.stdout, "\r\033[K")
	u.mu.Unlock()

	if err == nil {
		u.Successf("%s (%s)", msg, elapsed)
	} else {
		u.Errorf("%s", msg)
	}
	return err
}
