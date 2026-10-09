package errorreport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"

	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
)

// Reporter is nil-safe, like *metrics.Metrics: a nil Reporter's Middleware
// passes requests straight through and Capture records nothing.
type Reporter struct {
	logger log.Logger
}

func New(provider log.LoggerProvider) *Reporter {
	return &Reporter{logger: provider.Logger("github.com/andrewcgraves/sparks-effect-api")}
}

type captured struct {
	op  string
	err error
	// Set for a recovered panic only; an error passed to Capture has no stack
	// worth reporting, since it was built far from where it is captured.
	stack string
}

type scope struct {
	mu       sync.Mutex
	captured []captured
}

type contextKey struct{}

// Capture notes err against the request ctx belongs to. It is reported once
// the request has been answered, because only then does the request carry the
// route pattern the mux matched. Outside a request Middleware wraps, it does
// nothing; the caller's own log line is still the record of it.
func Capture(ctx context.Context, op string, err error) {
	if err == nil {
		return
	}
	capture(ctx, captured{op: op, err: err})
}

func capture(ctx context.Context, c captured) {
	s, ok := ctx.Value(contextKey{}).(*scope)
	if !ok {
		return
	}
	s.mu.Lock()
	s.captured = append(s.captured, c)
	s.mu.Unlock()
}

// Recover answers a panicking handler with a 500 instead of net/http's dropped
// connection, and captures it for Middleware to report. It belongs inside the
// access log, so the 500 is logged and counted like any other; it does not
// touch the request, so it can sit between that and the mux.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			// net/http's own signal to abort the response quietly.
			if v == http.ErrAbortHandler {
				panic(v)
			}
			stack := string(debug.Stack())
			trace, _ := traceid.FromContext(r.Context())
			slog.ErrorContext(r.Context(), "handler: panic", "panic", v, "trace_id", trace, "stack", stack)
			capture(r.Context(), captured{op: "panic", err: fmt.Errorf("%v", v), stack: stack})
			// If the handler had already started the response this is too late
			// to change the status, and net/http says so in its own log.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}` + "\n"))
		}()
		next.ServeHTTP(w, r)
	})
}

// Middleware must sit outside anything that reads r.Pattern afterwards (the
// access log, metrics): it replaces the request's context, and the mux sets
// the pattern on the request it is handed, which is the one passed on here.
func (rep *Reporter) Middleware(next http.Handler) http.Handler {
	if rep == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := &scope{}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
		// Deferred so what was captured before a panic is still reported as
		// the panic unwinds past here.
		defer rep.report(r, s)
		next.ServeHTTP(w, r)
	})
}

func (rep *Reporter) report(r *http.Request, s *scope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trace, _ := traceid.FromContext(r.Context())
	for _, c := range s.captured {
		var rec log.Record
		rec.SetSeverity(log.SeverityError)
		rec.SetSeverityText("ERROR")
		rec.SetBody(attribute.StringValue("internal error"))
		rec.AddAttributes(
			attribute.String("trace_id", trace),
			attribute.String("route", r.Pattern),
			attribute.String("op", c.op),
			attribute.String("exception.message", redact(c.err.Error())),
		)
		if c.stack != "" {
			rec.AddAttributes(
				attribute.String("exception.type", "panic"),
				attribute.String("exception.stacktrace", c.stack),
			)
		}
		rep.logger.Emit(r.Context(), rec)
	}
}

// Nothing from the request itself (headers, body) is ever attached, so a
// credential can only arrive inside an error's text: an upstream echoing the
// header it was sent, say. Belt and braces for that one shape.
var bearer = regexp.MustCompile(`(?i)(bearer\s+)[^\s"',;]+`)

func redact(s string) string {
	return bearer.ReplaceAllString(s, "${1}[redacted]")
}
