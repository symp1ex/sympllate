package logger

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPlainHandlerFormatAndLevel(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	handler := NewPlainHandler(&output, slog.LevelWarn)
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info level unexpectedly enabled")
	}
	record := slog.NewRecord(time.Date(2026, 8, 5, 12, 34, 56, 0, time.Local), slog.LevelWarn, "message", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "] [WARN] message\n") || !strings.HasPrefix(got, "[2026-08-05 12:34:56,000]") {
		t.Fatalf("unexpected log line: %q", got)
	}
}
