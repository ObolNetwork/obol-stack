package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGreen  = "\033[32m"
	colorGray   = "\033[90m"
)

// Custom log level for success (between Info and Warn)
const LevelSuccess = slog.Level(2)

// Symbol prefixes with colors for each log level
const (
	prefixDebug   = "\033[90m[·]\033[0m" // Gray bullet
	prefixInfo    = "\033[34m[→]\033[0m" // Blue arrow
	prefixSuccess = "\033[32m[✓]\033[0m" // Green check
	prefixWarn    = "\033[33m[!]\033[0m" // Yellow exclamation
	prefixError   = "\033[31m[✗]\033[0m" // Red X
)

// ConsoleHandler formats logs for human-readable console output
type ConsoleHandler struct {
	opts    slog.HandlerOptions
	preform string // prefix formatting
	w       io.Writer
	attrs   []slog.Attr
	groups  []string
}

// NewConsoleHandler creates a handler that outputs user-friendly logs to the console
func NewConsoleHandler(w io.Writer, opts *slog.HandlerOptions) *ConsoleHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &ConsoleHandler{
		opts: *opts,
		w:    w,
	}
}

func (h *ConsoleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

func (h *ConsoleHandler) Handle(ctx context.Context, r slog.Record) error {
	// Check for subprocess output and command info
	isSubprocess := false
	subprocessOutput := ""
	subprocessCommand := ""
	subprocessArgs := ""

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "subprocess" && a.Value.Kind() == slog.KindBool {
			isSubprocess = a.Value.Bool()
		}
		if a.Key == "output" && a.Value.Kind() == slog.KindString {
			subprocessOutput = a.Value.String()
		}
		if a.Key == "command" && a.Value.Kind() == slog.KindString {
			subprocessCommand = a.Value.String()
		}
		if a.Key == "args" {
			// Args is stored as Any, extract its string representation
			argsStr := a.Value.String()
			// Remove the surrounding brackets if present: "[arg1 arg2]" -> "arg1 arg2"
			argsStr = strings.TrimPrefix(argsStr, "[")
			argsStr = strings.TrimSuffix(argsStr, "]")
			subprocessArgs = argsStr
		}
		return true
	})

	// Format subprocess output with indentation and indicator
	if isSubprocess && subprocessOutput != "" {
		// Print subprocess command indicator if we have command info
		if subprocessCommand != "" {
			cmdLine := subprocessCommand
			if subprocessArgs != "" {
				cmdLine += " " + subprocessArgs
			}
			fmt.Fprintf(h.w, "$ %s\n", cmdLine)
		}

		lines := strings.Split(subprocessOutput, "\n")
		for _, line := range lines {
			if line != "" {
				// Use 4 spaces for subprocess indentation
				fmt.Fprintf(h.w, "    %s\n", line)
			}
		}
		return nil
	}

	// Format regular logs based on level with colored symbol prefixes
	var prefix string

	switch r.Level {
	case slog.LevelDebug:
		prefix = prefixDebug
	case slog.LevelInfo:
		prefix = prefixInfo
	case LevelSuccess:
		prefix = prefixSuccess
	case slog.LevelWarn:
		prefix = prefixWarn
	case slog.LevelError:
		prefix = prefixError
	default:
		prefix = prefixInfo
	}

	// Write: prefix + space + message
	fmt.Fprintf(h.w, "%s %s\n", prefix, r.Message)
	return nil
}

func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandler := *h
	newHandler.attrs = append(newHandler.attrs, attrs...)
	return &newHandler
}

func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	newHandler := *h
	newHandler.groups = append(newHandler.groups, name)
	return &newHandler
}

// MultiHandler sends logs to multiple handlers
type MultiHandler struct {
	handlers []slog.Handler
}

func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			if err := handler.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: newHandlers}
}

func (h *MultiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &MultiHandler{handlers: newHandlers}
}

// LoggerConfig holds configuration for creating a logger
type LoggerConfig struct {
	StateDir string // Directory for log files
	StackID  string // Stack ID for grouping logs
}

// NewSlogLogger creates a new slog.Logger with console and optional file output
// Console output: human-readable with proper formatting
// File output: JSON with full trace details (when stack ID is known)
// Returns the logger and a cleanup function to close the file
func NewSlogLogger(cfg LoggerConfig) (*slog.Logger, func() error) {
	var handlers []slog.Handler

	// Console handler - always present, user-friendly format
	consoleHandler := NewConsoleHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	handlers = append(handlers, consoleHandler)

	var file *os.File
	cleanup := func() error { return nil }

	// File handler - JSON logs with full details (when stack ID known)
	if cfg.StateDir != "" && cfg.StackID != "" {
		logDir := filepath.Join(cfg.StateDir, cfg.StackID, "logs")
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
				cleanup = func() error {
					if file != nil {
						return file.Close()
					}
					return nil
				}
			}
		}
	}

	// Create multi-handler to log to both console and file
	multiHandler := NewMultiHandler(handlers...)

	// Add stack ID to all logs if available
	var logger *slog.Logger
	if cfg.StackID != "" {
		logger = slog.New(multiHandler).With("stack_id", cfg.StackID)
	} else {
		logger = slog.New(multiHandler)
	}

	return logger, cleanup
}

// Success logs a success message with a green check symbol
func Success(logger *slog.Logger, msg string, args ...any) {
	logger.Log(context.Background(), LevelSuccess, msg, args...)
}
