package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

type PlainHandler struct {
	mu    sync.Mutex
	w     io.Writer
	level slog.Level
}

func NewPlainHandler(w io.Writer, level slog.Level) *PlainHandler {
	return &PlainHandler{w: w, level: level}
}

func (h *PlainHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *PlainHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	timestamp := record.Time.Format("2006-01-02 15:04:05,000")
	level := strings.ToUpper(record.Level.String())
	_, err := fmt.Fprintf(h.w, "[%s] [%s] %s\n", timestamp, level, record.Message)
	return err
}

func (h *PlainHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *PlainHandler) WithGroup(_ string) slog.Handler { return h }
