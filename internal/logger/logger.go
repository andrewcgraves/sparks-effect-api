// Package logger configures the API's structured logging.
//
// Every log line is a single JSON object — timestamp, level, message, and
// whatever structured fields the call site attaches — rather than free text,
// so a shipper like Grafana Alloy can forward and query them without
// scraping. The level is configurable at boot (see config.Config.LogLevel)
// rather than fixed, so a deployment can turn on debug output without a
// rebuild.
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// ParseLevel maps a LOG_LEVEL value to a slog.Level. Recognised values are
// "debug", "info", "warn" (or "warning"), and "error", case-insensitively;
// anything else — including an unset flag — falls back to Info, the
// quietest level that still surfaces everything worth seeing in production.
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

// New returns a JSON logger writing to out, gated at level.
func New(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
}

// Default returns a JSON logger writing to stderr, gated at level — what the
// process logs with once running.
func Default(level slog.Level) *slog.Logger {
	return New(os.Stderr, level)
}

// Discard returns a logger that writes nothing, for tests and callers with no
// interest in log output.
func Discard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// Init installs a JSON logger at level as the process-wide default, so code
// with no logger threaded to it — deep helpers, stdlib-shaped call sites —
// still logs at the same level and in the same JSON shape as everything else,
// through slog's package-level functions (slog.Info, slog.Error, ...).
func Init(level slog.Level) {
	slog.SetDefault(Default(level))
}
