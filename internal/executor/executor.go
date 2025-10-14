package executor

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/obol/obol-stack/internal/logging"
)

// Executor wraps subprocess execution with automatic output logging
type Executor struct {
	logger *logging.Logger
	stdout io.Writer
	stderr io.Writer
}

// New creates a new Executor with the given logger
// All subprocess output will be automatically logged
func New(logger *logging.Logger) *Executor {
	e := &Executor{
		logger: logger,
	}

	// Create logging writers that capture subprocess output
	if logger != nil {
		e.stdout = &logWriter{
			writer: os.Stdout,
			logger: logger,
			level:  "info",
		}
		e.stderr = &logWriter{
			writer: os.Stderr,
			logger: logger,
			level:  "error",
		}
	} else {
		e.stdout = os.Stdout
		e.stderr = os.Stderr
	}

	return e
}

// Command creates a new command that automatically logs all subprocess output
// Use this exactly like exec.Command - logging is handled transparently
func (e *Executor) Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	// Log command initiation
	if e.logger != nil {
		e.logger.Info("Executing command", "cmd", name, "args", args)
	}

	// Set up logging for stdout/stderr
	// For commands using Output(), they will override Stdout, so only set Stderr
	cmd.Stderr = e.stderr

	return cmd
}

// CommandWithLogging creates a command that logs both stdout and stderr
// Use this for commands that call Run() (not Output())
func (e *Executor) CommandWithLogging(name string, args ...string) *exec.Cmd {
	cmd := e.Command(name, args...)
	cmd.Stdout = e.stdout
	return cmd
}

// logWriter wraps an io.Writer and logs subprocess output
type logWriter struct {
	writer io.Writer
	logger *logging.Logger
	level  string
	buffer []byte
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	// Write to the underlying writer (os.Stdout/Stderr)
	n, err = w.writer.Write(p)
	if err != nil {
		return n, err
	}

	// Also log line by line
	w.buffer = append(w.buffer, p...)

	// Process complete lines
	for {
		idx := strings.IndexByte(string(w.buffer), '\n')
		if idx == -1 {
			break
		}

		line := string(w.buffer[:idx])
		w.buffer = w.buffer[idx+1:]

		if line != "" {
			switch w.level {
			case "error":
				w.logger.Error("subprocess", "line", line)
			default:
				w.logger.Info("subprocess", "line", line)
			}
		}
	}

	return n, nil
}
