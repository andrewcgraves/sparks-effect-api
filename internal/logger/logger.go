package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func New(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
}

func Default(level slog.Level) *slog.Logger {
	return New(os.Stderr, level)
}

func Discard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func Init(level slog.Level) {
	slog.SetDefault(Default(level))
}
