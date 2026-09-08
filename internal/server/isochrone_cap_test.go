package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const isochroneBody = `{"lat":37.79,"lng":-122.397,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`

func newCappedServer(t *testing.T, deps AuthDeps, limit int) http.Handler {
	t.Helper()
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour, MaxInFlightIsochrones: limit}
	return New(cfg, store, deps, &routing.FakePublisher{}, logger.Discard()).Handler
}

var isochroneRoutes = []struct{ path, token string }{
	{"/api/isochrone", ""},
	{"/api/services/some-slug/isochrone", userToken},
	{"/api/user-scenarios/some-slug/isochrone", userToken},
}

func TestIsochroneRoutesRefuseAFullBacklog(t *testing.T) {
	deps := newStubDeps()
	deps.inFlight = 4
	h := newCappedServer(t, deps, 4)

	for _, route := range isochroneRoutes {
		t.Run(route.path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, route.path, route.token, isochroneBody)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
			}

			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body.Code != handler.BacklogFullErrorCode {
				t.Errorf("code = %q, want %q", body.Code, handler.BacklogFullErrorCode)
			}
		})
	}
}

func TestIsochroneRoutesPassAnEmptyBacklogThrough(t *testing.T) {
	deps := newStubDeps()
	h := newCappedServer(t, deps, 4)

	for _, route := range isochroneRoutes {
		t.Run(route.path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, route.path, route.token, isochroneBody)
			if rec.Code == http.StatusTooManyRequests {
				t.Errorf("status = 429 with an empty backlog; body %s", rec.Body.String())
			}
		})
	}
}

func TestNonEnqueueingRoutesAreNotCapped(t *testing.T) {
	deps := newStubDeps()
	deps.inFlight = 1000
	h := newCappedServer(t, deps, 1)

	for _, path := range []string{"/api/scenarios", "/api/routing-jobs/some-id", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodGet, path, "")
			if rec.Code == http.StatusTooManyRequests {
				t.Errorf("status = 429 on a route that enqueues nothing; body %s", rec.Body.String())
			}
		})
	}
}

func TestIsochroneRoutesUncappedWhenDisabled(t *testing.T) {
	deps := newStubDeps()
	deps.inFlight = 1000
	h := newCappedServer(t, deps, 0)

	for _, route := range isochroneRoutes {
		t.Run(route.path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, route.path, route.token, isochroneBody)
			if rec.Code == http.StatusTooManyRequests {
				t.Errorf("status = 429 with the cap disabled; body %s", rec.Body.String())
			}
		})
	}
}

func TestAuthoredIsochronesStill401BeforeTheCap(t *testing.T) {
	deps := newStubDeps()
	deps.inFlight = 1000
	h := newCappedServer(t, deps, 1)

	for _, path := range []string{
		"/api/services/some-slug/isochrone",
		"/api/user-scenarios/some-slug/isochrone",
	} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, path, "", isochroneBody)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}
