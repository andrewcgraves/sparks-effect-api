package traceid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
)

func serve(t *testing.T, req *http.Request) (*httptest.ResponseRecorder, string, bool) {
	t.Helper()
	var got string
	var ok bool
	h := traceid.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = traceid.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, got, ok
}

func TestMiddleware_generatesATraceIDWhenNoneSupplied(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec, got, ok := serve(t, req)

	if !ok || got == "" {
		t.Fatal("no trace id was attached to the request context")
	}
	if header := rec.Header().Get(traceid.Header); header != got {
		t.Errorf("response header %q = %q, want the generated id %q", traceid.Header, header, got)
	}
}

func TestMiddleware_usesTheSuppliedTraceID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(traceid.Header, "caller-supplied-id")

	rec, got, ok := serve(t, req)

	if !ok || got != "caller-supplied-id" {
		t.Errorf("context trace id = %q, ok=%v, want %q", got, ok, "caller-supplied-id")
	}
	if header := rec.Header().Get(traceid.Header); header != "caller-supplied-id" {
		t.Errorf("response header = %q, want it echoed back", header)
	}
}

func TestMiddleware_generatesADifferentIDPerRequest(t *testing.T) {
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	_, first, _ := serve(t, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	_, second, _ := serve(t, req2)

	if first == second {
		t.Errorf("two untraced requests got the same trace id %q", first)
	}
}

func TestFromContext_falseWhenUnset(t *testing.T) {
	if _, ok := traceid.FromContext(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Error("FromContext reported ok=true on a plain context")
	}
}
