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
	srv := New(config.Config{Port: "8080"}, store, chainer, logger.Discard())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
}
