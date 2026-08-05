package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sympllate/translator/internal/config"
)

var (
	loggers    = make(map[string]*slog.Logger)
	mu         sync.Mutex
	logDir     string
	retainDays = 2
	logLevel   = "warning"
	Sympllate  Logger
)

type PrintLogger interface {
	Printf(format string, args ...any)
}

type Logger struct {
	*slog.Logger
}

func Configure(cfg config.LogsConfig) {
	mu.Lock()
	logDir = filepath.Join(config.WorkDir(), "logs")
	retainDays = cfg.StoreDays
	logLevel = cfg.LogLevel
	loggers = make(map[string]*slog.Logger)
	mu.Unlock()

	Sympllate = Logger{Get("sympllate")}
}

func levelFromString(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l Logger) Printf(format string, args ...any) { l.Infof(format, args...) }

func (l Logger) Infof(format string, args ...any) { l.logf(slog.LevelInfo, format, args...) }

func (l Logger) Debugf(format string, args ...any) { l.logf(slog.LevelDebug, format, args...) }

func (l Logger) Warnf(format string, args ...any) { l.logf(slog.LevelWarn, format, args...) }

func (l Logger) Errorf(format string, args ...any) { l.logf(slog.LevelError, format, args...) }

func (l Logger) Writer() io.Writer {
	if l.Logger == nil {
		return io.Discard
	}
	return slog.NewLogLogger(l.Handler(), slog.LevelInfo).Writer()
}

func (l Logger) logf(level slog.Level, format string, args ...any) {
	if l.Logger == nil {
		return
	}
	ctx := context.Background()
	if l.Enabled(ctx, level) {
		l.Log(ctx, level, fmt.Sprintf(format, args...))
	}
}

func Get(name string) *slog.Logger {
	mu.Lock()
	defer mu.Unlock()

	if existing, ok := loggers[name]; ok {
		return existing
	}
	if logDir == "" {
		logDir = filepath.Join(config.WorkDir(), "logs")
	}
	writer := NewRotatingWriter(name)
	result := slog.New(NewPlainHandler(writer, levelFromString(logLevel)))
	loggers[name] = result
	return result
}
