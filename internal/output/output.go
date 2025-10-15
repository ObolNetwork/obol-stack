package output

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Writer handles user-friendly output and logging
type Writer struct {
	stateDir  string
	clusterID string
	logFile   *os.File
}

// New creates a new output writer
// If stateDir and clusterID are provided, all output is also logged to files
func New(stateDir, clusterID string) *Writer {
	w := &Writer{
		stateDir:  stateDir,
		clusterID: clusterID,
	}

	// Open log file if we have cluster info
	if stateDir != "" && clusterID != "" {
		w.logFile = w.openLogFile()
	}

	return w
}

// openLogFile opens or creates the log file for this session
func (w *Writer) openLogFile() *os.File {
	if w.stateDir == "" || w.clusterID == "" {
		return nil
	}

	// Create cluster-specific log directory
	logDir := filepath.Join(w.stateDir, w.clusterID, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil
	}

	// Open log file for appending (create if doesn't exist)
	logPath := filepath.Join(logDir, "session.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}

	return f
}

// Info prints an informational message to stdout and logs it
func (w *Writer) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	w.log("INFO", msg)
}

// Success prints a success message (with checkmark) to stdout and logs it
func (w *Writer) Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("✓ %s\n", msg)
	w.log("SUCCESS", msg)
}

// Warn prints a warning message to stderr and logs it
func (w *Writer) Warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
	w.log("WARN", msg)
}

// Error prints an error message to stderr and logs it
func (w *Writer) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	w.log("ERROR", msg)
}

// Step prints a step/progress message to stdout and logs it
func (w *Writer) Step(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("→ %s\n", msg)
	w.log("STEP", msg)
}

// log writes a timestamped message to the log file
func (w *Writer) log(level, msg string) {
	if w.logFile == nil {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("[%s] %s: %s\n", timestamp, level, msg)
	w.logFile.WriteString(logLine)
}

// Close closes the log file
func (w *Writer) Close() error {
	if w.logFile != nil {
		return w.logFile.Close()
	}
	return nil
}

// GetLogWriter returns an io.Writer that logs to the file
// This is useful for subprocess output that needs to be logged
func (w *Writer) GetLogWriter() io.Writer {
	if w.logFile != nil {
		return w.logFile
	}
	return io.Discard
}
