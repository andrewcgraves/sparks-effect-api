package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReady(t *testing.T) {
	up := pingerFunc(func(context.Context) error { return nil })
	down := pingerFunc(func(context.Context) error { return errors.New("connection refused") })

	cases := []struct {
		name       string
		db, broker Pinger
		wantStatus int
		wantPG     string
		wantMQ     string
	}{
		{"both disabled", nil, nil, http.StatusOK, "disabled", "disabled"},
		{"both ok", up, up, http.StatusOK, "ok", "ok"},
		{"postgres only", up, nil, http.StatusOK, "ok", "disabled"},
		{"postgres down", down, up, http.StatusServiceUnavailable, "unavailable", "ok"},
		{"broker down", up, down, http.StatusServiceUnavailable, "ok", "unavailable"},
		{"broker down, no database", nil, down, http.StatusServiceUnavailable, "disabled", "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Ready(tc.db, tc.broker, logger.Discard())(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, rec.Code)
			}
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
			}
			if got["postgres"] != tc.wantPG || got["amqp"] != tc.wantMQ {
				t.Errorf("body: want postgres=%q amqp=%q, got %v", tc.wantPG, tc.wantMQ, got)
			}
		})
	}
}

func TestReady_boundsASlowComponent(t *testing.T) {
	// A check that never returns on its own: only the handler's deadline ends it.
	// The production bound is a second; pin that initializer, then shorten it
	// so the suite is not waiting that second out. The elapsed assertion
	// fails if Ready stops consulting readyTimeout.
	if readyTimeout != time.Second {
		t.Fatalf("production readyTimeout = %s, want %s", readyTimeout, time.Second)
	}
	readyTimeout = 25 * time.Millisecond
	t.Cleanup(func() { readyTimeout = time.Second })

	hang := pingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	rec := httptest.NewRecorder()
	start := time.Now()
	Ready(hang, nil, logger.Discard())(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("readyz returned after %s; the handler deadline did not bound the check", elapsed)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", rec.Code)
	}
}
