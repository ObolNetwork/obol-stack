package output

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/obol/obol-stack/internal/logging"
)

// Writer handles user-friendly output and logging using slog
type Writer struct {
	logger *slog.Logger
	file   *os.File // Keep reference to close later
}

// New creates a new output writer with slog-based logging
// Console output: human-readable with proper formatting
// File output: JSON with full trace details (when cluster ID is known)
func New(stateDir, clusterID string) *Writer {
	var handlers []slog.Handler

	// Console handler - always present, user-friendly format
	consoleHandler := logging.NewConsoleHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	handlers = append(handlers, consoleHandler)

	var file *os.File

	// File handler - JSON logs with full details (when cluster ID known)
	if stateDir != "" && clusterID != "" {
		logDir := filepath.Join(stateDir, clusterID, "logs")
		if err := os.MkdirAll(logDir, 0755); err == nil {
			logPath := filepath.Join(logDir, "session.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				file = f
				// JSON handler for detailed file logging
				jsonHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{
					Level:     slog.LevelDebug, // Log everything to file
					AddSource: true,            // Add source file/line info
				})
				handlers = append(handlers, jsonHandler)
			}
		}
	}

	// Create multi-handler to log to both console and file
	multiHandler := logging.NewMultiHandler(handlers...)

	// Add cluster ID to all logs if available
	var logger *slog.Logger
	if clusterID != "" {
		logger = slog.New(multiHandler).With("cluster_id", clusterID)
	} else {
		logger = slog.New(multiHandler)
	}

	return &Writer{
		logger: logger,
		file:   file,
	}
}

// Info prints an informational message
func (w *Writer) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Info(msg)
}

// Success prints a success message with checkmark
func (w *Writer) Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Info("✓ " + msg)
}

// Warn prints a warning message
func (w *Writer) Warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Warn(msg)
}

// Error prints an error message
func (w *Writer) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Error(msg)
}

// Step prints a step/progress message
func (w *Writer) Step(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Info("→ " + msg)
}

// Debug prints a debug message (only to file, not console)
func (w *Writer) Debug(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	w.logger.Debug(msg)
}

// LogSubprocess logs subprocess output
// This will be indented on console and stored as a string in JSON logs
func (w *Writer) LogSubprocess(output string) {
	w.logger.Info("subprocess output",
		slog.Bool("subprocess", true),
		slog.String("output", output),
	)
}

// Close closes the log file
func (w *Writer) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// Logger returns the underlying slog.Logger for advanced usage
func (w *Writer) Logger() *slog.Logger {
	return w.logger
}
