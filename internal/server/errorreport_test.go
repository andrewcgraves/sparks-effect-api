package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/errorreport/errorreporttest"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type sessionOutageDeps struct{ *stubAuthDeps }

func (sessionOutageDeps) GetSessionUser(context.Context, string) (account.User, bool, error) {
	return account.User{}, false, errors.New("pgx: connection reset by peer")
}

func TestAnInternalErrorIsReportedWithTheRequestsTraceIDAndRoute(t *testing.T) {
	rep, rec := errorreporttest.New(t)
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour}
	h := New(cfg, store, sessionOutageDeps{newStubDeps()}, &routing.FakePublisher{}, nil, logger.Discard(), nil, rep).Handler

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set(traceid.Header, "trace-from-the-website")
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	reports := rec.Reports()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	got := reports[0].Attrs
	if got["trace_id"] != "trace-from-the-website" {
		t.Errorf("trace_id = %q, want the request's", got["trace_id"])
	}
	if got["route"] != "GET /api/auth/me" {
		t.Errorf("route = %q, want the matched pattern", got["route"])
	}
	for k, v := range got {
		if strings.Contains(v, userToken) {
			t.Errorf("attr %s leaks the session token", k)
		}
	}
}
