package executor

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
)

// Executor wraps subprocess execution with automatic output logging via slog
type Executor struct {
	logger *slog.Logger
}

// New creates a new Executor that logs subprocess output via slog
// The logger should be configured to handle subprocess output appropriately
func New(logger *slog.Logger) *Executor {
	return &Executor{
		logger: logger,
	}
}

// cmdLogger accumulates subprocess output and logs it when complete
type cmdLogger struct {
	stdout *outputAccumulator
	stderr *outputAccumulator
	logger *slog.Logger
	cmd    string
	args   []string
}

func newCmdLogger(logger *slog.Logger, cmd string, args []string) *cmdLogger {
	return &cmdLogger{
		stdout: &outputAccumulator{display: os.Stdout, buffer: &bytes.Buffer{}},
		stderr: &outputAccumulator{display: os.Stderr, buffer: &bytes.Buffer{}},
		logger: logger,
		cmd:    cmd,
		args:   args,
	}
}

func (c *cmdLogger) logComplete() {
	if c.logger == nil {
		return
	}

	// Log the complete subprocess execution as a single entry
	stdoutStr := c.stdout.buffer.String()
	stderrStr := c.stderr.buffer.String()

	if stdoutStr != "" || stderrStr != "" {
		c.logger.Info("subprocess execution",
			slog.Bool("subprocess", true),
			slog.String("command", c.cmd),
			slog.Any("args", c.args),
			slog.String("output", stdoutStr+stderrStr),
		)
	}
}

// Command creates a new command for use with Output()
// Only stderr is logged/displayed, stdout is captured by Output()
func (e *Executor) Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	if e.logger != nil {
		// Capture stderr for display with indentation
		cmd.Stderr = &outputAccumulator{
			display: os.Stderr,
			buffer:  &bytes.Buffer{},
		}
	} else {
		cmd.Stderr = os.Stderr
	}

	return cmd
}

// CommandWithOutput creates a command that displays and logs subprocess output
// Use this for commands that call Run() (not Output())
// Output is displayed in real-time with indentation, then logged as a single entry when complete
func (e *Executor) CommandWithOutput(name string, args ...string) CmdRunner {
	cmd := exec.Command(name, args...)

	if e.logger != nil {
		// Create command logger to accumulate output
		cmdLog := newCmdLogger(e.logger, name, args)

		cmd.Stdout = cmdLog.stdout
		cmd.Stderr = cmdLog.stderr

		// Return a wrapped command that logs when complete
		return &loggingCmd{
			Cmd:    cmd,
			logger: cmdLog,
		}
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return &basicCmd{Cmd: cmd}
}

// CmdRunner is an interface that exec.Cmd-like commands must implement
type CmdRunner interface {
	Run() error
	Start() error
	Wait() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
	SetEnv(env []string)
	SetStdin(stdin io.Reader)
}

// basicCmd wraps exec.Cmd without logging
type basicCmd struct {
	*exec.Cmd
}

func (c *basicCmd) Run() error                              { return c.Cmd.Run() }
func (c *basicCmd) Start() error                            { return c.Cmd.Start() }
func (c *basicCmd) Wait() error                             { return c.Cmd.Wait() }
func (c *basicCmd) Output() ([]byte, error)                 { return c.Cmd.Output() }
func (c *basicCmd) CombinedOutput() ([]byte, error)         { return c.Cmd.CombinedOutput() }
func (c *basicCmd) StdinPipe() (io.WriteCloser, error)      { return c.Cmd.StdinPipe() }
func (c *basicCmd) StdoutPipe() (io.ReadCloser, error)      { return c.Cmd.StdoutPipe() }
func (c *basicCmd) StderrPipe() (io.ReadCloser, error)      { return c.Cmd.StderrPipe() }
func (c *basicCmd) SetEnv(env []string)                     { c.Cmd.Env = env }
func (c *basicCmd) SetStdin(stdin io.Reader)                { c.Cmd.Stdin = stdin }

// loggingCmd wraps exec.Cmd to log output when command completes
type loggingCmd struct {
	*exec.Cmd
	logger *cmdLogger
}

func (c *loggingCmd) Run() error {
	err := c.Cmd.Run()
	c.logger.logComplete()
	return err
}

func (c *loggingCmd) Start() error {
	return c.Cmd.Start()
}

func (c *loggingCmd) Wait() error {
	err := c.Cmd.Wait()
	c.logger.logComplete()
	return err
}

func (c *loggingCmd) Output() ([]byte, error)                 { return c.Cmd.Output() }
func (c *loggingCmd) CombinedOutput() ([]byte, error)         { return c.Cmd.CombinedOutput() }
func (c *loggingCmd) StdinPipe() (io.WriteCloser, error)      { return c.Cmd.StdinPipe() }
func (c *loggingCmd) StdoutPipe() (io.ReadCloser, error)      { return c.Cmd.StdoutPipe() }
func (c *loggingCmd) StderrPipe() (io.ReadCloser, error)      { return c.Cmd.StderrPipe() }
func (c *loggingCmd) SetEnv(env []string)                     { c.Cmd.Env = env }
func (c *loggingCmd) SetStdin(stdin io.Reader)                { c.Cmd.Stdin = stdin }

// Close is a no-op for compatibility
func (e *Executor) Close() error {
	return nil
}

// outputAccumulator captures subprocess output for both display and logging
// It displays output in real-time with indentation and accumulates it for batch logging
type outputAccumulator struct {
	display io.Writer     // Where to write for display (os.Stdout/Stderr)
	buffer  *bytes.Buffer // Accumulate complete output
	mu      sync.Mutex    // Protect concurrent writes
}

func (w *outputAccumulator) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Display immediately with indentation (so user sees real-time output)
	indented := addIndent(p)
	if _, err = w.display.Write(indented); err != nil {
		return 0, err
	}

	// Also accumulate for batch logging later
	w.buffer.Write(p)

	return len(p), nil
}

// addIndent adds 4-space indentation to each line for console display
func addIndent(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	var result []byte
	atLineStart := true

	for i := 0; i < len(data); i++ {
		// Add indent at start of each line
		if atLineStart && data[i] != '\n' {
			result = append(result, []byte("    ")...)
			atLineStart = false
		}

		result = append(result, data[i])

		// Track line boundaries
		if data[i] == '\n' {
			atLineStart = true
		}
	}

	return result
}
