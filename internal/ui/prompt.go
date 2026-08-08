package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// isInteractive returns true if prompts should be shown.
// Returns false in JSON mode, when not a TTY, or when OBOL_NONINTERACTIVE=true.
func (u *UI) isInteractive() bool {
	return !u.IsJSON() && u.IsTTY() && os.Getenv("OBOL_NONINTERACTIVE") != "true"
}

// Confirm asks a yes/no question, returns true for "y"/"yes".
// In non-interactive mode, returns the default without prompting.
func (u *UI) Confirm(msg string, defaultYes bool) bool {
	if !u.isInteractive() {
		return defaultYes
	}

	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}

	fmt.Fprintf(u.stdout, "%s %s ", msg, dimStyle.Render(suffix))

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))

	if line == "" {
		return defaultYes
	}

	return line == "y" || line == "yes"
}

// Select presents a numbered list and returns the selected index.
// In non-interactive mode, returns the default index without prompting.
func (u *UI) Select(msg string, options []string, defaultIdx int) (int, error) {
	if !u.isInteractive() {
		return defaultIdx, nil
	}

	fmt.Fprintln(u.stdout, msg)

	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = accentStyle.Render("→ ")
		}

		fmt.Fprintf(u.stdout, "  %s%s%s %s\n",
			marker,
			accentStyle.Render("["),
			accentStyle.Render(strconv.Itoa(i+1)),
			opt+accentStyle.Render("]"))
	}

	defDisplay := strconv.Itoa(defaultIdx + 1)
	fmt.Fprintf(u.stdout, "\n  %s %s: ", accentStyle.Render("Choice"), dimStyle.Render("["+defDisplay+"]"))

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if line == "" {
		return defaultIdx, nil
	}

	choice, err := strconv.Atoi(line)
	if err != nil || choice < 1 || choice > len(options) {
		return 0, fmt.Errorf("invalid selection: %s", line)
	}

	return choice - 1, nil
}

// Input reads a single line of text input with an optional default.
// In non-interactive mode, returns the default or an error if no default is set.
func (u *UI) Input(msg string, defaultVal string) (string, error) {
	return u.InputWithSuffix(msg, defaultVal, "")
}

// InputWithSuffix reads input like Input but renders an extra dimmed suffix
// between the default brace and the colon. Use this for hints that aren't
// part of the question itself — e.g. "[5] (5 OBOL ceiling):". The suffix
// is shown only when defaultVal is non-empty (so it stays adjacent to the
// "[default]" chip).
func (u *UI) InputWithSuffix(msg, defaultVal, suffix string) (string, error) {
	if !u.isInteractive() {
		if defaultVal != "" {
			return defaultVal, nil
		}
		return "", fmt.Errorf("%s: required (non-interactive mode, provide via flag)", msg)
	}

	if defaultVal != "" {
		if strings.TrimSpace(suffix) != "" {
			fmt.Fprintf(u.stdout, "%s %s %s: ", msg, dimStyle.Render("["+defaultVal+"]"), dimStyle.Render(suffix))
		} else {
			fmt.Fprintf(u.stdout, "%s %s: ", msg, dimStyle.Render("["+defaultVal+"]"))
		}
	} else {
		fmt.Fprintf(u.stdout, "%s: ", msg)
	}

	reader := bufio.NewReader(os.Stdin)

	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal, nil
	}

	return line, nil
}

// SecretInput reads input without echoing (for API keys, passwords).
// In non-interactive mode, returns an error directing the user to use a flag.
func (u *UI) SecretInput(msg string) (string, error) {
	if !u.isInteractive() {
		return "", fmt.Errorf("%s: interactive input unavailable (provide via flag or env var)", msg)
	}

	fmt.Fprintf(u.stdout, "%s: ", msg)

	b, err := term.ReadPassword(int(os.Stdin.Fd()))

	fmt.Fprintln(u.stdout) // newline after hidden input

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(b)), nil
}
