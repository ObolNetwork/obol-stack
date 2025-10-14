package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// SessionID is a unique identifier for a command execution session
type SessionID string

// CommandEntry represents a command execution in history
type CommandEntry struct {
	SessionID   SessionID         `json:"session_id"`
	ClusterID   string            `json:"cluster_id,omitempty"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	StartTime   time.Time         `json:"start_time"`
	EndTime     time.Time         `json:"end_time,omitempty"`
	ExitCode    int               `json:"exit_code,omitempty"`
	Error       string            `json:"error,omitempty"`
	WorkingDir  string            `json:"working_dir"`
	Environment map[string]string `json:"environment,omitempty"`
}

// Logger wraps slog.Logger with session tracking
type Logger struct {
	*slog.Logger
	sessionID SessionID
	stateDir  string
	historyFile *os.File
}

// NewLogger creates a new logger with session tracking
func NewLogger(stateDir string) (*Logger, error) {
	// Generate unique session ID
	sessionID := SessionID(uuid.New().String())

	// Create state directory if it doesn't exist
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Create logs directory
	logsDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log file for this session
	logFile := filepath.Join(logsDir, fmt.Sprintf("%s.log", time.Now().Format("2006-01-02")))
	logFileHandle, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Create slog handler with JSON format for file
	jsonHandler := slog.NewJSONHandler(logFileHandle, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})

	// Create text handler for console output
	consoleHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	// Use tee handler to write to both, with session ID
	teeHandler := &teeHandler{
		handlers: []slog.Handler{consoleHandler, jsonHandler},
	}

	// Wrap with session ID
	handlerWithSession := teeHandler.WithAttrs([]slog.Attr{
		slog.String("session_id", string(sessionID)),
	})

	logger := slog.New(handlerWithSession)

	// Open history file
	historyFile := filepath.Join(stateDir, "history.jsonl")
	historyHandle, err := os.OpenFile(historyFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logFileHandle.Close()
		return nil, fmt.Errorf("failed to open history file: %w", err)
	}

	return &Logger{
		Logger:      logger,
		sessionID:   sessionID,
		stateDir:    stateDir,
		historyFile: historyHandle,
	}, nil
}

// SessionID returns the current session ID
func (l *Logger) SessionID() SessionID {
	return l.sessionID
}

// LogCommand records a command execution in history
func (l *Logger) LogCommand(cmd string, args []string) error {
	return l.LogCommandWithClusterID(cmd, args, "")
}

// LogCommandWithClusterID records a command execution with cluster_id in history
func (l *Logger) LogCommandWithClusterID(cmd string, args []string, clusterID string) error {
	entry := CommandEntry{
		SessionID:   l.sessionID,
		ClusterID:   clusterID,
		Command:     cmd,
		Args:        args,
		StartTime:   time.Now(),
		WorkingDir:  getCurrentDir(),
		Environment: getRelevantEnv(),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal command entry: %w", err)
	}

	if _, err := l.historyFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write history entry: %w", err)
	}

	if clusterID != "" {
		l.Info("Command started",
			"command", cmd,
			"args", args,
			"cluster_id", clusterID,
		)
	} else {
		l.Info("Command started",
			"command", cmd,
			"args", args,
		)
	}

	return nil
}

// LogCommandComplete marks a command as completed
func (l *Logger) LogCommandComplete(cmd string, exitCode int, err error) error {
	entry := CommandEntry{
		SessionID: l.sessionID,
		Command:   cmd,
		EndTime:   time.Now(),
		ExitCode:  exitCode,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal command completion: %w", err)
	}

	if _, err := l.historyFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write history completion: %w", err)
	}

	if entry.Error != "" {
		l.Error("Command failed",
			"command", cmd,
			"exit_code", exitCode,
			"error", entry.Error,
		)
	} else {
		l.Info("Command completed",
			"command", cmd,
			"exit_code", exitCode,
		)
	}

	return nil
}

// Close closes the logger and flushes any pending writes
func (l *Logger) Close() error {
	if l.historyFile != nil {
		return l.historyFile.Close()
	}
	return nil
}

// getCurrentDir returns the current working directory
func getCurrentDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// getRelevantEnv returns relevant environment variables for debugging
func getRelevantEnv() map[string]string {
	env := make(map[string]string)
	relevantKeys := []string{
		"OBOL_CONFIG_DIR",
		"OBOL_BIN_DIR",
		"OBOL_DATA_DIR",
		"OBOL_STATE_DIR",
		"OBOL_DEVELOPMENT",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
		"KUBECONFIG",
	}

	for _, key := range relevantKeys {
		if val := os.Getenv(key); val != "" {
			env[key] = val
		}
	}

	return env
}

// teeHandler writes to multiple handlers
type teeHandler struct {
	handlers []slog.Handler
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &teeHandler{handlers: handlers}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &teeHandler{handlers: handlers}
}
