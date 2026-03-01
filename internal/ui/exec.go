package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// ExecConfig configures how a subprocess is run.
type ExecConfig struct {
	// Name is the display name shown in the spinner (e.g., "Deploying with helmfile").
	Name string

	// Cmd is the command to run.
	Cmd *exec.Cmd

	// Interactive runs the command with stdin/stdout/stderr connected directly
	// to the terminal. Use this for commands that may prompt for input (e.g. sudo).
	// Disables spinner and output capture.
	Interactive bool
}

// Exec runs a subprocess with output capture and spinner.
//
// Default mode (TTY, not verbose):
//   - Shows spinner with config.Name
//   - Captures stdout+stderr to buffer
//   - On success: "✓ Name (Xs)"
//   - On failure: "✗ Name" + dumps captured output to stderr
//
// Verbose mode:
//   - Shows "==> Name"
//   - Streams stdout+stderr live, each line indented with dim "│" prefix
//
// Non-TTY (pipe/CI):
//   - Shows "Name..."
//   - Streams live (no spinner)
func (u *UI) Exec(cfg ExecConfig) error {
	if cfg.Interactive {
		return u.execInteractive(cfg)
	}
	if u.verbose {
		return u.execVerbose(cfg)
	}
	return u.execCaptured(cfg)
}

// ExecOutput runs a subprocess, captures stdout, and returns it.
// Stderr is captured and shown on error.
func (u *UI) ExecOutput(cfg ExecConfig) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cfg.Cmd.Stdout = &stdout
	cfg.Cmd.Stderr = &stderr

	err := u.RunWithSpinner(cfg.Name, func() error {
		return cfg.Cmd.Run()
	})
	if err != nil {
		combined := stderr.String() + stdout.String()
		if combined != "" {
			u.dumpCapturedOutput(combined)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (u *UI) execInteractive(cfg ExecConfig) error {
	u.Infof("%s ...", cfg.Name)
	if cfg.Cmd.Stdin == nil {
		cfg.Cmd.Stdin = os.Stdin
	}
	if cfg.Cmd.Stdout == nil {
		cfg.Cmd.Stdout = os.Stdout
	}
	if cfg.Cmd.Stderr == nil {
		cfg.Cmd.Stderr = os.Stderr
	}
	err := cfg.Cmd.Run()
	if err == nil {
		u.Successf("%s", cfg.Name)
	} else {
		u.Errorf("%s", cfg.Name)
	}
	return err
}

func (u *UI) execCaptured(cfg ExecConfig) error {
	var buf bytes.Buffer
	cfg.Cmd.Stdout = &buf
	cfg.Cmd.Stderr = &buf

	err := u.RunWithSpinner(cfg.Name, func() error {
		return cfg.Cmd.Run()
	})
	if err != nil && buf.Len() > 0 {
		u.dumpCapturedOutput(buf.String())
	}
	return err
}

func (u *UI) execVerbose(cfg ExecConfig) error {
	u.Info(cfg.Name)

	// Create pipes to prefix each line with dim "│".
	stdoutPipe, stdoutW := io.Pipe()
	stderrPipe, stderrW := io.Pipe()
	cfg.Cmd.Stdout = stdoutW
	cfg.Cmd.Stderr = stderrW

	var wg sync.WaitGroup
	wg.Add(2)

	prefix := dimStyle.Render("  │ ")

	streamLines := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			fmt.Fprintf(u.stdout, "%s%s\n", prefix, scanner.Text())
		}
	}

	go streamLines(stdoutPipe)
	go streamLines(stderrPipe)

	err := cfg.Cmd.Run()
	stdoutW.Close()
	stderrW.Close()
	wg.Wait()

	if err == nil {
		u.Successf("%s", cfg.Name)
	} else {
		u.Errorf("%s", cfg.Name)
	}
	return err
}

func (u *UI) dumpCapturedOutput(output string) {
	separator := dimStyle.Render("  ───────────────────────")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, dimStyle.Render("  Output:"))
	fmt.Fprintln(os.Stderr, separator)

	scanner := bufio.NewScanner(bytes.NewReader([]byte(output)))
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "  %s\n", scanner.Text())
	}

	fmt.Fprintln(os.Stderr, separator)
	fmt.Fprintln(os.Stderr)
}
