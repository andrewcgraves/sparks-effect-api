package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestNew_healthz(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080"}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
}

func TestCORS_flagOn_localhostOrigin_GET(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://localhost:5173" {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", "http://localhost:5173", got)
	}
}

func TestCORS_flagOn_localhostOrigin_OPTIONS(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	req.Header.Set("Origin", "http://127.0.0.1:4173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status: want 204, got %d", rec.Code)
	}
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "http://127.0.0.1:4173" {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", "http://127.0.0.1:4173", got)
	}
}

func TestCORS_flagOn_nonLocalhostOrigin(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("Access-Control-Allow-Origin: want empty for non-localhost, got %q", got)
	}
}

func TestCORS_productionOrigin_allowedRegardlessOfFlag(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://sparks-effect-website.vercel.app")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "https://sparks-effect-website.vercel.app" {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", "https://sparks-effect-website.vercel.app", got)
	}
}

// Preview deployments (SPA-252) talk to the staging API from a per-deployment
// hostname on the Vercel team, not the production alias. The git alias is
// truncated to a prefix plus a short slug — not the full branch name.
func TestCORS_previewOrigin_allowedRegardlessOfFlag(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	const origin = "https://sparks-effect-website-git-claude-2643c5-andrewcgraves-projects.vercel.app"
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != origin {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", origin, got)
	}
}

func TestCORS_previewOrigin_OPTIONS(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	const origin = "https://sparks-effect-website-7geea1s8q-andrewcgraves-projects.vercel.app"
	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status: want 204, got %d", rec.Code)
	}
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != origin {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", origin, got)
	}
}

func TestCORS_unrelatedVercelOrigin_rejected(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://some-other-app.vercel.app")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("Access-Control-Allow-Origin: want empty for unrelated vercel.app, got %q", got)
	}
}

func TestCORS_allowsXTraceIdHeader(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "X-Trace-Id")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(got, "X-Trace-Id") {
		t.Errorf("Access-Control-Allow-Headers: want to contain %q, got %q", "X-Trace-Id", got)
	}
}

// The Retry-After on a capped isochrone's 429 (SPA-219) is only readable from
// a browser if it is named here — a cross-origin response otherwise exposes
// none of its headers to script.
func TestCORS_exposesRetryAfter(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(got, "Retry-After") {
		t.Errorf("Access-Control-Expose-Headers: want to contain %q, got %q", "Retry-After", got)
	}
}

func TestCORS_flagOff_localhostOrigin(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("Access-Control-Allow-Origin: want empty when flag off, got %q", got)
	}
}

// The project's own domain is allowed whole: the apex and dev. are the two
// hosts in use today, but the point of the domain match is that a third does
// not need a code change to reach the API.
func TestCORS_sparksEffectOrigin_allowedRegardlessOfFlag(t *testing.T) {
	for _, origin := range []string{
		"https://sparks-effect.app",
		"https://dev.sparks-effect.app",
	} {
		store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("Access-Control-Allow-Origin: want %q, got %q", origin, got)
		}
	}
}

// The preflight is the half that matters for an authenticated POST from the
// SPA: without a 204 on OPTIONS the browser never sends the real request.
func TestCORS_sparksEffectOrigin_OPTIONS(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, &routing.FakePublisher{}, logger.Discard())

	const origin = "https://dev.sparks-effect.app"
	req := httptest.NewRequest(http.MethodOptions, "/api/isochrone", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: want %d, got %d", http.StatusNoContent, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", origin, got)
	}
}

func TestIsSparksEffectOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{origin: "https://sparks-effect.app", want: true},
		{origin: "https://dev.sparks-effect.app", want: true},
		{origin: "https://api.dev.sparks-effect.app", want: true},
		// Port and case are part of the Origin a browser may send.
		{origin: "https://sparks-effect.app:443", want: true},
		{origin: "https://DEV.Sparks-Effect.app", want: true},
		// The domain is served over TLS; a plaintext claim on it is not ours.
		{origin: "http://sparks-effect.app", want: false},
		// A lookalike registration must not match on a bare string suffix.
		{origin: "https://notsparks-effect.app", want: false},
		{origin: "https://evil-sparks-effect.app", want: false},
		// Nor a host that only carries the domain as a prefix of its own.
		{origin: "https://sparks-effect.app.evil.com", want: false},
		{origin: "https://evil.com", want: false},
		{origin: "", want: false},
		{origin: "null", want: false},
	}
	for _, tt := range tests {
		if got := isSparksEffectOrigin(tt.origin); got != tt.want {
			t.Errorf("isSparksEffectOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

func TestIsVercelPreviewOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{origin: "https://andrewcgraves-projects.vercel.app", want: true},
		// Truncated git alias from sparks-effect-website#66 — branch name is not in the host.
		{origin: "https://sparks-effect-website-git-claude-2643c5-andrewcgraves-projects.vercel.app", want: true},
		// Per-commit URL: project + 9-character hash + team slug.
		{origin: "https://sparks-effect-website-7geea1s8q-andrewcgraves-projects.vercel.app", want: true},
		{origin: "https://preview.andrewcgraves-projects.vercel.app", want: true},
		{origin: "https://sparks-effect-website.vercel.app", want: false},
		{origin: "https://some-other-app.vercel.app", want: false},
		{origin: "https://notandrewcgraves-projects.vercel.app", want: false},
		{origin: "http://sparks-effect-website-git-claude-2643c5-andrewcgraves-projects.vercel.app", want: false},
		{origin: "https://andrewcgraves-projects.vercel.app.evil.com", want: false},
		{origin: "https://evil.com", want: false},
		{origin: "", want: false},
		{origin: "null", want: false},
	}
	for _, tt := range tests {
		if got := isVercelPreviewOrigin(tt.origin); got != tt.want {
			t.Errorf("isVercelPreviewOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}
