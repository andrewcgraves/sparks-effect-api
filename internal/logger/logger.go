package logger

import (
	"io"
	"log"
	"os"
)

// Logger gates debug output behind a flag. Use Discard for no-op instances.
type Logger struct {
	l     *log.Logger
	debug bool
}

// New creates a Logger writing to out. When debug is false, Debugf is a no-op.
func New(out io.Writer, debug bool) *Logger {
	return &Logger{l: log.New(out, "", log.LstdFlags), debug: debug}
}

// Default creates a Logger writing to stderr using flags from the stdlib log
// package global. When debug is false, Debugf is a no-op.
func Default(debug bool) *Logger {
	return New(os.Stderr, debug)
}

// Discard returns a Logger that discards all output regardless of debug mode.
func Discard() *Logger {
	return &Logger{l: log.New(io.Discard, "", 0), debug: false}
}

// Debugf logs only when the logger was created with debug=true.
// The [DEBUG] prefix is prepended automatically.
func (lg *Logger) Debugf(format string, args ...any) {
	if lg.debug {
		lg.l.Printf("[DEBUG] "+format, args...)
	}
}

// Printf always logs, regardless of debug mode.
func (lg *Logger) Printf(format string, args ...any) {
	lg.l.Printf(format, args...)
}
