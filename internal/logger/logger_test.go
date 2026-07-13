package logger_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

func TestDebugf_noopWhenOff(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, false)
	lg.Debugf("should not appear %s", "here")
	if buf.Len() != 0 {
		t.Errorf("Debugf wrote output when debug=false: %q", buf.String())
	}
}

func TestDebugf_writesWhenOn(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, true)
	lg.Debugf("hello %s", "world")
	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Errorf("Debugf output missing expected content: %q", out)
	}
	if !strings.Contains(out, "[DEBUG]") {
		t.Errorf("Debugf output missing [DEBUG] prefix: %q", out)
	}
}

func TestDiscard_noOutput(t *testing.T) {
	lg := logger.Discard()
	lg.Debugf("no output")
	lg.Printf("also no output")
}

func TestPrintf_alwaysWrites(t *testing.T) {
	var buf bytes.Buffer
	lg := logger.New(&buf, false)
	lg.Printf("always %s", "visible")
	if !strings.Contains(buf.String(), "always visible") {
		t.Errorf("Printf did not write when debug=false: %q", buf.String())
	}
}
