package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Confirm asks a yes/no question, returns true for "y"/"yes".
// The default is shown in brackets: [Y/n] or [y/N].
func (u *UI) Confirm(msg string, defaultYes bool) bool {
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
// defaultIdx is 0-based; shown as [default] next to that option.
func (u *UI) Select(msg string, options []string, defaultIdx int) (int, error) {
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
func (u *UI) Input(msg string, defaultVal string) (string, error) {
	if defaultVal != "" {
		fmt.Fprintf(u.stdout, "%s %s: ", msg, dimStyle.Render("["+defaultVal+"]"))
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
func (u *UI) SecretInput(msg string) (string, error) {
	fmt.Fprintf(u.stdout, "%s: ", msg)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(u.stdout) // newline after hidden input
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
