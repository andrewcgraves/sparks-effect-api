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
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
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

// New returns a JSON logger writing to out, gated at level. A record written
// through a context carrying a trace id (see internal/traceid) gets a
// trace_id attribute automatically — a call site that logs with a *Context
// method never has to attach it by hand.
func New(out io.Writer, level slog.Level) *slog.Logger {
	handler := traceHandler{Handler: slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})}
	return slog.New(handler)
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

// traceHandler wraps an slog.Handler and adds a trace_id attribute to every
// record whose context carries one (see internal/traceid.FromContext), so a
// request's trace id reaches its logs by being attached to the context once
// — in traceid.Middleware — rather than by every call site remembering to
// pass it explicitly. A call site that logs without a context (the plain
// Info/Error/... methods, which log against context.Background()) is
// unaffected; it keeps attaching trace_id by hand if it wants it, same as
// before.
type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := traceid.FromContext(ctx); ok {
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{Handler: h.Handler.WithGroup(name)}
}
