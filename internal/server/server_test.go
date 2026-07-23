package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestNew_healthz(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080"}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
}

func TestCORS_flagOn_localhostOrigin_GET(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

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
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

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
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: true}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

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
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://sparks-effect-website.vercel.app")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "https://sparks-effect-website.vercel.app" {
		t.Errorf("Access-Control-Allow-Origin: want %q, got %q", "https://sparks-effect-website.vercel.app", got)
	}
}

func TestCORS_flagOff_localhostOrigin(t *testing.T) {
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	srv := New(config.Config{Port: "8080", AllowLocalhostCORS: false}, store, nil, chainer, &stadia.FakeClient{}, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("Access-Control-Allow-Origin: want empty when flag off, got %q", got)
	}
}
