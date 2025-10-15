package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	// Get subprocess flag if present
	isSubprocess := false
	subprocessOutput := ""

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "subprocess" && a.Value.Kind() == slog.KindBool {
			isSubprocess = a.Value.Bool()
		}
		if a.Key == "output" && a.Value.Kind() == slog.KindString {
			subprocessOutput = a.Value.String()
		}
		return true
	})

	// Format subprocess output with indentation
	if isSubprocess && subprocessOutput != "" {
		lines := strings.Split(subprocessOutput, "\n")
		for _, line := range lines {
			if line != "" {
				fmt.Fprintf(h.w, "    %s\n", line)
			}
		}
		return nil
	}

	// Format regular program logs
	buf := make([]byte, 0, 256)

	// Add level prefix with color/symbol
	switch r.Level {
	case slog.LevelDebug:
		buf = append(buf, "[DEBUG] "...)
	case slog.LevelInfo:
		// For info, check for special message types
		msg := r.Message
		if strings.HasPrefix(msg, "→ ") || strings.HasPrefix(msg, "✓ ") {
			// Already formatted, just print it
			fmt.Fprintln(h.w, msg)
			return nil
		}
		buf = append(buf, ""...) // No prefix for regular info
	case slog.LevelWarn:
		buf = append(buf, "Warning: "...)
	case slog.LevelError:
		buf = append(buf, "Error: "...)
	}

	// Add message
	buf = append(buf, r.Message...)

	// Add attributes if any (for non-subprocess logs)
	if !isSubprocess {
		r.Attrs(func(a slog.Attr) bool {
			// Skip internal attributes
			if a.Key == "subprocess" || a.Key == "output" {
				return true
			}
			// Format key=value
			buf = append(buf, ' ')
			buf = append(buf, a.Key...)
			buf = append(buf, '=')
			buf = append(buf, a.Value.String()...)
			return true
		})
	}

	buf = append(buf, '\n')
	_, err := h.w.Write(buf)
	return err
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
