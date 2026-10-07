package server

import (
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/metrics/metricstest"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestRequestsAreCountedByRoutePattern(t *testing.T) {
	m, reader := metricstest.New(t)
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour}
	h := New(cfg, store, newStubDeps(), &routing.FakePublisher{}, nil, logger.Discard(), m, nil).Handler

	request(t, h, http.MethodGet, "/api/scenarios/ca-hsr", "")
	request(t, h, http.MethodGet, "/api/scenarios/no-such-scenario", "")
	request(t, h, http.MethodGet, "/no/such/path", "")

	if got := reader.Count(t, "http_requests",
		attribute.String("route", "/api/scenarios/{slug}"),
		attribute.String("method", "GET"),
		attribute.String("status_class", "2xx"),
	); got != 1 {
		t.Errorf("2xx scenario reads = %d, want 1", got)
	}
	if got := reader.Count(t, "http_requests",
		attribute.String("route", "/api/scenarios/{slug}"),
		attribute.String("method", "GET"),
		attribute.String("status_class", "4xx"),
	); got != 1 {
		t.Errorf("4xx scenario reads = %d, want 1", got)
	}
	if got := reader.Count(t, "http_requests",
		attribute.String("route", "unmatched"),
		attribute.String("method", "GET"),
		attribute.String("status_class", "4xx"),
	); got != 1 {
		t.Errorf("unmatched reads = %d, want 1", got)
	}
	if got := reader.Count(t, "http_request_duration",
		attribute.String("route", "/api/scenarios/{slug}"),
	); got != 2 {
		t.Errorf("scenario read durations = %d, want 2", got)
	}
}

func TestUnknownMethodsShareOneLabel(t *testing.T) {
	m, reader := metricstest.New(t)
	h := New(config.Config{Port: "8080"}, nil, nil, nil, nil, logger.Discard(), m, nil).Handler

	request(t, h, "BREW", "/no/such/path", "")

	if got := reader.Count(t, "http_requests",
		attribute.String("route", "unmatched"),
		attribute.String("method", "other"),
		attribute.String("status_class", "4xx"),
	); got != 1 {
		t.Errorf("unknown-method reads = %d, want 1; series %v", got, reader.Series(t, "http_requests"))
	}
}

func TestRateLimitRefusalsAreCountedByLimiter(t *testing.T) {
	m, reader := metricstest.New(t)
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour, RateLimitLogin: tinyPolicy()}
	h := New(cfg, store, newStubDeps(), &routing.FakePublisher{}, nil, logger.Discard(), m, nil).Handler

	for range 3 {
		requestFrom(t, h, http.MethodPost, "/api/auth/login", "", "203.0.113.7:1234", "", loginBody)
	}

	if got := reader.Count(t, "rate_limited", attribute.String("limiter", "login")); got != 2 {
		t.Errorf("login refusals = %d, want 2", got)
	}
	if got := reader.Count(t, "rate_limited", attribute.String("limiter", "isochrone")); got != 0 {
		t.Errorf("isochrone refusals = %d, want 0", got)
	}
}
