package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
)

func TestNew_writesJSON(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, slog.LevelInfo)
	lg.Info("hello", "who", "world")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if line["msg"] != "hello" {
		t.Errorf("msg = %v, want %q", line["msg"], "hello")
	}
	if line["who"] != "world" {
		t.Errorf("who = %v, want %q", line["who"], "world")
	}
}

func TestNew_gatesBelowLevel(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, slog.LevelInfo)
	lg.Debug("should not appear")
	if buf.Len() != 0 {
		t.Errorf("Debug wrote output below the configured level: %q", buf.String())
	}
}

func TestNew_debugLevelWritesDebug(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, slog.LevelDebug)
	lg.Debug("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("Debug output missing expected content: %q", buf.String())
	}
}

func TestNew_attachesTraceIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, slog.LevelInfo)
	ctx := traceid.WithContext(context.Background(), "trace-abc-123")
	lg.InfoContext(ctx, "hello")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if line["trace_id"] != "trace-abc-123" {
		t.Errorf("trace_id = %v, want %q", line["trace_id"], "trace-abc-123")
	}
}

func TestNew_omitsTraceIDWhenContextCarriesNone(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, slog.LevelInfo)
	lg.InfoContext(context.Background(), "hello")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if _, ok := line["trace_id"]; ok {
		t.Errorf("trace_id = %v, want no such field", line["trace_id"])
	}
}

func TestDiscard_noOutput(t *testing.T) {
	lg := logger.Discard()
	lg.Debug("no output")
	lg.Info("also no output")
	lg.Error("still no output")
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" debug ": slog.LevelDebug,
		"info":    slog.LevelInfo,
		"":        slog.LevelInfo,
		"bogus":   slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for input, want := range cases {
		if got := logger.ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}
