package errorreport_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/log"

	"github.com/andrewcgraves/sparks-effect-api/internal/errorreport"
	"github.com/andrewcgraves/sparks-effect-api/internal/errorreport/errorreporttest"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
)

// serve runs one request through the reporter in front of a mux, the way the
// server stacks them, and answers the recorded reports.
func serve(t *testing.T, pattern string, h http.HandlerFunc, req *http.Request) []errorreporttest.Report {
	t.Helper()
	reports, _ := serveRecorded(t, pattern, h, req)
	return reports
}

func serveRecorded(t *testing.T, pattern string, h http.HandlerFunc, req *http.Request) ([]errorreporttest.Report, *httptest.ResponseRecorder) {
	t.Helper()
	rep, rec := errorreporttest.New(t)
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, h)
	w := httptest.NewRecorder()
	traceid.Middleware(rep.Middleware(errorreport.Recover(mux))).ServeHTTP(w, req)
	return rec.Reports(), w
}

func TestACapturedInternalErrorIsReportedWithItsTraceIDAndRoute(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr", nil)
	req.Header.Set(traceid.Header, "trace-123")

	reports := serve(t, "GET /api/scenarios/{slug}", func(w http.ResponseWriter, r *http.Request) {
		errorreport.Capture(r.Context(), "load scenario", errors.New("pq: connection refused"))
		w.WriteHeader(http.StatusInternalServerError)
	}, req)

	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	got := reports[0]
	if got.Severity != log.SeverityError {
		t.Errorf("severity = %v, want ERROR", got.Severity)
	}
	want := map[string]string{
		"trace_id":          "trace-123",
		"route":             "GET /api/scenarios/{slug}",
		"op":                "load scenario",
		"exception.message": "pq: connection refused",
	}
	for k, v := range want {
		if got.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, got.Attrs[k], v)
		}
	}
}

func TestAReportCarriesNoBearerTokenOrPassword(t *testing.T) {
	const token = "s3ss10n-t0k3n-abcdef"
	const password = "hunter2-correct-horse"
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)

	reports := serve(t, "POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		errorreport.Capture(r.Context(), "login", fmt.Errorf("upstream said: Authorization: Bearer %s", token))
		w.WriteHeader(http.StatusInternalServerError)
	}, req)

	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	for k, v := range reports[0].Attrs {
		if strings.Contains(v, token) || strings.Contains(v, password) {
			t.Errorf("attr %s = %q leaks a credential", k, v)
		}
	}
	if !strings.Contains(reports[0].Attrs["exception.message"], "Bearer [redacted]") {
		t.Errorf("exception.message = %q, want the token replaced by [redacted]", reports[0].Attrs["exception.message"])
	}
}

func explode() { panic("nil map in the compiler") }

func TestAPanicIsReportedWithItsStackAndAnswered500(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios", nil)
	req.Header.Set(traceid.Header, "trace-panic")

	reports, w := serveRecorded(t, "GET /api/scenarios", func(http.ResponseWriter, *http.Request) {
		explode()
	}, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	got := reports[0].Attrs
	if got["trace_id"] != "trace-panic" || got["route"] != "GET /api/scenarios" {
		t.Errorf("trace_id, route = %q, %q", got["trace_id"], got["route"])
	}
	if got["exception.type"] != "panic" || got["exception.message"] != "nil map in the compiler" {
		t.Errorf("exception.type, message = %q, %q", got["exception.type"], got["exception.message"])
	}
	if !strings.Contains(got["exception.stacktrace"], "errorreport_test.explode") {
		t.Errorf("exception.stacktrace = %q, want the panicking frame", got["exception.stacktrace"])
	}
}

func TestAnAbortedHandlerIsNotReported(t *testing.T) {
	rep, rec := errorreporttest.New(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/scenarios", func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})
	h := rep.Middleware(errorreport.Recover(mux))

	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler re-panicked for net/http", v)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/scenarios", nil))
	}()
	if n := len(rec.Reports()); n != 0 {
		t.Errorf("reports = %d, want 0", n)
	}
}

func TestA4xxIsNotReported(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope", nil)
	reports := serve(t, "GET /api/scenarios/{slug}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}, req)
	if len(reports) != 0 {
		t.Errorf("reports = %d, want 0", len(reports))
	}
}
