package executor

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Executor wraps subprocess execution with automatic output logging
type Executor struct {
	stateDir  string
	clusterID string
	logFile   *os.File
}

// New creates a new Executor that will log subprocess output to cluster-specific log files
// If stateDir or clusterID are empty, logging is disabled and output goes directly to stdout/stderr
func New(stateDir, clusterID string) *Executor {
	e := &Executor{
		stateDir:  stateDir,
		clusterID: clusterID,
	}

	// Open log file for subprocess output if we have cluster info
	if stateDir != "" && clusterID != "" {
		e.logFile = e.openLogFile()
	}

	return e
}

// Command creates a new command that automatically logs all subprocess output
// Use this exactly like exec.Command - it's a drop-in replacement
// Subprocess output goes to stdout/stderr (so user sees it) and to log files (when available)
//
// NOTE: If you need to use cmd.Output() or cmd.CombinedOutput(), those methods will
// override Stdout, so we only set Stderr by default. For commands using Run(), the
// caller should set both Stdout and Stderr if they want visible output.
func (e *Executor) Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	// Only set up Stderr by default - this works with both Run() and Output()
	// For Run(), the caller can explicitly set Stdout if they want output
	if e.logFile != nil {
		stderr := &teeWriter{
			writers: []io.Writer{os.Stderr},
			logFile: e.logFile,
		}
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	return cmd
}

// CommandWithOutput creates a command that shows output to the user
// Use this for commands that call Run() (not Output()) and you want to see stdout/stderr
// Output is indented with 4 spaces for better visual clarity
func (e *Executor) CommandWithOutput(name string, args ...string) *exec.Cmd {
	cmd := e.Command(name, args...)

	// Set up stdout for visible output with indentation
	if e.logFile != nil {
		stdout := &teeWriter{
			writers:     []io.Writer{os.Stdout},
			logFile:     e.logFile,
			indent:      true,
			indentText:  "    ", // 4 spaces
			atLineStart: true,   // Start at beginning of line
		}
		cmd.Stdout = stdout

		// Update stderr to also indent
		stderr := &teeWriter{
			writers:     []io.Writer{os.Stderr},
			logFile:     e.logFile,
			indent:      true,
			indentText:  "    ", // 4 spaces
			atLineStart: true,   // Start at beginning of line
		}
		cmd.Stderr = stderr
	} else {
		// Create indenting writers even without logging
		stdout := &teeWriter{
			writers:     []io.Writer{os.Stdout},
			indent:      true,
			indentText:  "    ",
			atLineStart: true, // Start at beginning of line
		}
		cmd.Stdout = stdout

		stderr := &teeWriter{
			writers:     []io.Writer{os.Stderr},
			indent:      true,
			indentText:  "    ",
			atLineStart: true, // Start at beginning of line
		}
		cmd.Stderr = stderr
	}

	return cmd
}

// Close closes the log file
func (e *Executor) Close() error {
	if e.logFile != nil {
		return e.logFile.Close()
	}
	return nil
}

// openLogFile opens or creates the subprocess log file for appending
func (e *Executor) openLogFile() *os.File {
	if e.stateDir == "" || e.clusterID == "" {
		return nil
	}

	// Create cluster-specific log directory
	logDir := filepath.Join(e.stateDir, e.clusterID, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil
	}

	// Open subprocess log file for appending (create if doesn't exist)
	logPath := filepath.Join(logDir, "subprocess.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}

	return f
}

// teeWriter writes to multiple writers simultaneously
// This allows subprocess output to go to both stdout/stderr AND log files
// Optionally indents output for better visual clarity
type teeWriter struct {
	writers    []io.Writer
	logFile    *os.File
	indent     bool   // Whether to indent output for visual writers
	indentText string // The indentation text (e.g., "    ")
	atLineStart bool  // Track if we're at the start of a new line
}

func (t *teeWriter) Write(p []byte) (n int, err error) {
	// Write to visual writers (stdout/stderr) with indentation if enabled
	if t.indent {
		// Process the data with indentation
		indented := t.indentData(p)
		for _, w := range t.writers {
			_, err = w.Write(indented)
			if err != nil {
				return n, err
			}
		}
	} else {
		// Write without indentation
		for _, w := range t.writers {
			_, err = w.Write(p)
			if err != nil {
				return n, err
			}
		}
	}

	// Write to log file without indentation (keep raw output)
	if t.logFile != nil {
		t.logFile.Write(p)
	}

	return len(p), nil
}

// indentData adds indentation to each line in the data
func (t *teeWriter) indentData(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	var result []byte

	// Start with indent if we're at the beginning of a line
	if t.atLineStart {
		result = append(result, []byte(t.indentText)...)
		t.atLineStart = false
	}

	// Process each byte
	for i := 0; i < len(data); i++ {
		result = append(result, data[i])

		// If we hit a newline, add indent for the next line
		// (unless it's the last character)
		if data[i] == '\n' && i < len(data)-1 {
			result = append(result, []byte(t.indentText)...)
		} else if data[i] == '\n' && i == len(data)-1 {
			// Mark that we're at line start for next write
			t.atLineStart = true
		}
	}

	return result
}
