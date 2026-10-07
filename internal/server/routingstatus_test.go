package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

func TestRoutingStatusIsPublic(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/routing/status", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200 with no credentials: %s", rec.Code, rec.Body)
	}
	var body struct{ Status string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Status != "ok" {
		t.Errorf("body = %s, want status ok", rec.Body)
	}
}

func TestOnlyAnAuthenticatedWorkerRequestCountsAsContact(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	watch := handler.NewWorkerWatch(func() time.Time { return now })
	deps := newStubDeps()
	deps.queue = handler.RoutingQueue{InFlight: 1, OldestQueuedAt: now.Add(-70 * time.Second)}
	mux := &routeTable{ServeMux: http.NewServeMux()}
	registerWorkerRoutes(mux, config.Config{WorkerToken: workerToken}, deps, watch)
	registerRoutingStatusRoutes(mux, deps, watch)
	now = now.Add(2 * time.Minute)

	status := func() string {
		t.Helper()
		var body struct{ Status string }
		rec := request(t, mux, http.MethodGet, "/api/routing/status", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal %q: %v", rec.Body, err)
		}
		return body.Status
	}

	if got := status(); got != "offline" {
		t.Fatalf("status = %q, want offline", got)
	}
	// Anyone can reach the internal path; only the worker's token proves the
	// worker is alive.
	request(t, mux, http.MethodGet, "/api/internal/worker", userToken)
	if got := status(); got != "offline" {
		t.Errorf("status = %q after an unauthenticated ping, want still offline", got)
	}
	request(t, mux, http.MethodGet, "/api/internal/worker", workerToken)
	if got := status(); got != "degraded" {
		t.Errorf("status = %q after the worker's ping, want degraded", got)
	}
}
