package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// stubAuthDeps is a minimal AuthDeps: it knows one admin token and one
// non-admin token, so route *registration* — which gate each path sits behind —
// can be exercised without a database. Handler behaviour itself is covered by
// the handler package's own tests.
type stubAuthDeps struct {
	sessions map[string]transit.User
}

func (s *stubAuthDeps) GetSessionUser(_ context.Context, tokenHash string) (transit.User, bool, error) {
	u, ok := s.sessions[tokenHash]
	return u, ok, nil
}

func (s *stubAuthDeps) GetUserCredentialsByEmail(context.Context, string) (transit.User, string, bool, error) {
	return transit.User{}, "", false, nil
}

// User-authored services (SPA-80): stubbed so route registration can be
// exercised; behaviour lives in the handler package's tests.
func (s *stubAuthDeps) CreateUserService(context.Context, transit.UserService) error { return nil }
func (s *stubAuthDeps) UpdateUserService(context.Context, transit.UserService) error { return nil }
func (s *stubAuthDeps) DeleteUserService(context.Context, string) error              { return nil }

func (s *stubAuthDeps) GetUserServiceByID(context.Context, string) (transit.UserService, bool, error) {
	return transit.UserService{}, false, nil
}

func (s *stubAuthDeps) GetUserServiceBySlug(context.Context, string) (transit.UserService, bool, error) {
	return transit.UserService{}, false, nil
}

func (s *stubAuthDeps) ListUserServicesByOwner(context.Context, string) ([]transit.UserService, error) {
	return nil, nil
}

// User-owned scenarios (SPA-81): stubbed so route registration can be
// exercised; behaviour lives in the handler package's tests.
func (s *stubAuthDeps) CreateUserScenario(context.Context, transit.UserScenario) error { return nil }
func (s *stubAuthDeps) UpdateUserScenario(context.Context, transit.UserScenario) error { return nil }
func (s *stubAuthDeps) DeleteUserScenario(context.Context, string) error               { return nil }

func (s *stubAuthDeps) GetUserScenarioByID(context.Context, string) (transit.UserScenario, bool, error) {
	return transit.UserScenario{}, false, nil
}

func (s *stubAuthDeps) GetUserScenarioBySlug(context.Context, string) (transit.UserScenario, bool, error) {
	return transit.UserScenario{}, false, nil
}

func (s *stubAuthDeps) ListUserScenariosByOwner(context.Context, string) ([]transit.UserScenario, error) {
	return nil, nil
}

func (s *stubAuthDeps) UserServiceIDsOwnedBy(context.Context, string, []string) (map[string]bool, error) {
	return nil, nil
}

func (s *stubAuthDeps) CreateSession(context.Context, transit.Session) error   { return nil }
func (s *stubAuthDeps) DeleteSession(context.Context, string) error            { return nil }
func (s *stubAuthDeps) CreateUser(context.Context, transit.User, string) error { return nil }
func (s *stubAuthDeps) GetUserByEmail(context.Context, string) (transit.User, bool, error) {
	return transit.User{}, false, nil
}
func (s *stubAuthDeps) ListScenariosByOwner(context.Context, string) ([]transit.Scenario, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListServicesByOwner(context.Context, string) ([]transit.Service, error) {
	return nil, nil
}
func (s *stubAuthDeps) CreateRoute(context.Context, transit.Route) error { return nil }
func (s *stubAuthDeps) GetRouteBySlug(context.Context, string) (transit.Route, bool, error) {
	return transit.Route{}, false, nil
}
func (s *stubAuthDeps) ListRouteSummaries(context.Context) ([]transit.RouteSummary, error) {
	return nil, nil
}
func (s *stubAuthDeps) GetScenarioBySlug(context.Context, string) (transit.Scenario, bool, error) {
	return transit.Scenario{}, false, nil
}
func (s *stubAuthDeps) CreateJob(context.Context, transit.Job) error { return nil }
func (s *stubAuthDeps) GetJobByID(context.Context, string) (transit.Job, bool, error) {
	return transit.Job{}, false, nil
}
func (s *stubAuthDeps) UpdateJobStatus(context.Context, string, string, string) error { return nil }
func (s *stubAuthDeps) CompleteJob(context.Context, string, transit.TransitGraph, []string) error {
	return nil
}
func (s *stubAuthDeps) GetLatestSucceededJob(context.Context, string, string) (transit.Job, bool, error) {
	return transit.Job{}, false, nil
}
func (s *stubAuthDeps) GetLatestSucceededUserScenarioJob(context.Context, string) (transit.Job, bool, error) {
	return transit.Job{}, false, nil
}
func (s *stubAuthDeps) ListUserServicesByIDs(context.Context, []string) ([]transit.UserService, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListRoutesByIDs(context.Context, []string) ([]transit.Route, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListRoutesByScenario(context.Context, string) ([]transit.Route, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListStationsByScenario(context.Context, string) ([]transit.Station, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListServicesByScenario(context.Context, string) ([]transit.Service, error) {
	return nil, nil
}
func (s *stubAuthDeps) ListVehicleTypes(context.Context) ([]transit.VehicleType, error) {
	return nil, nil
}

const (
	adminToken = "admin-token"
	userToken  = "user-token"
)

func newTestServer(t *testing.T, deps AuthDeps) http.Handler {
	t.Helper()
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour}
	return New(cfg, store, deps, chainer, logger.Discard()).Handler
}

func newStubDeps() *stubAuthDeps {
	return &stubAuthDeps{sessions: map[string]transit.User{
		auth.HashToken(adminToken): {ID: "admin-1", Email: "admin@example.com", IsAdmin: true},
		auth.HashToken(userToken):  {ID: "user-1", Email: "user@example.com"},
	}}
}

// request builds and serves a test request. An optional body (JSON) may be
// passed as a single trailing arg; omitting it sends an empty body.
func request(t *testing.T, h http.Handler, method, path, token string, body ...string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if len(body) == 0 || body[0] == "" {
		req = httptest.NewRequest(method, path, http.NoBody)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body[0]))
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Every protected route must reject an anonymous caller. This is the guard that
// keeps a future endpoint from being registered outside the middleware.
func TestProtectedRoutesRejectAnonymousCallers(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	protected := []struct{ method, path string }{
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/auth/logout"},
		{http.MethodGet, "/api/me/scenarios"},
		{http.MethodGet, "/api/me/services"},
		{http.MethodPost, "/api/admin/users"},
		{http.MethodPost, "/api/admin/routes"},
		{http.MethodPost, "/api/scenarios/ca-hsr/compile"},
		{http.MethodGet, "/api/jobs/some-id"},
		{http.MethodPost, "/api/services/some-slug/compile"},
		{http.MethodPost, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios/some-slug"},
		{http.MethodPut, "/api/user-scenarios/some-slug"},
		{http.MethodDelete, "/api/user-scenarios/some-slug"},
		{http.MethodPost, "/api/user-scenarios/some-slug/compile"},
		{http.MethodGet, "/api/user-scenarios/some-slug/graph"},
	}

	for _, p := range protected {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			rec := request(t, h, p.method, p.path, "")
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 for an unauthenticated caller", rec.Code)
			}
		})
	}
}

// Admin-gated routes must reject an authenticated non-admin with 403. Route
// ingestion sits behind this same gate, which is what keeps it admin-only.
func TestAdminRoutesRejectNonAdmins(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, path := range []string{"/api/admin/users", "/api/admin/routes"} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, path, userToken)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for a non-admin", rec.Code)
			}

			// The same route admits an admin — proving the 403 was the gate and
			// not a misrouted request.
			rec = request(t, h, http.MethodPost, path, adminToken)
			if rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
				t.Errorf("admin was blocked from an admin route: status %d", rec.Code)
			}
		})
	}
}

func TestAuthenticatedRoutesAdmitValidTokens(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, path := range []string{"/api/auth/me", "/api/me/scenarios", "/api/me/services"} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodGet, path, userToken)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A valid token must reach the compile-job handlers rather than being stuck
// at the auth gate. stubAuthDeps has no scenarios or jobs, so the handlers
// themselves answer 404 — the point here is only that it isn't 401/403.
func TestCompileJobRoutesAdmitValidTokens(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, p := range []struct{ method, path string }{
		{http.MethodPost, "/api/scenarios/ca-hsr/compile"},
		{http.MethodGet, "/api/jobs/some-id"},
		{http.MethodPost, "/api/services/some-slug/compile"},
		{http.MethodPost, "/api/user-scenarios/some-slug/compile"},
		{http.MethodGet, "/api/user-scenarios/some-slug/graph"},
	} {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			rec := request(t, h, p.method, p.path, userToken)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Errorf("status = %d, a valid token must pass the auth gate", rec.Code)
			}
		})
	}
}

// A valid token must reach the user-scenario CRUD handlers, and the existing
// public curated-scenario read at the same base path must be unaffected — the
// two live at distinct paths (/api/user-scenarios vs /api/scenarios) so
// neither shadows the other.
func TestUserScenarioRoutesAdmitValidTokensAndDontShadowPublicReads(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, p := range []struct{ method, path string }{
		{http.MethodPost, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios/some-slug"},
		{http.MethodPut, "/api/user-scenarios/some-slug"},
		{http.MethodDelete, "/api/user-scenarios/some-slug"},
	} {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			rec := request(t, h, p.method, p.path, userToken)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Errorf("status = %d, a valid token must pass the auth gate", rec.Code)
			}
		})
	}

	// The curated public read at /api/scenarios/{slug} must still answer
	// unauthenticated, proving /api/user-scenarios did not shadow it.
	rec := request(t, h, http.MethodGet, "/api/scenarios/ca-hsr", "")
	if rec.Code != http.StatusOK {
		t.Errorf("public curated read status = %d, want 200", rec.Code)
	}
}

// The public read endpoints predate auth and must stay reachable — adding
// authentication must not silently gate the existing curated data.
func TestPublicReadRoutesStayOpen(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, path := range []string{"/healthz", "/api/scenarios", "/api/scenarios/ca-hsr"} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 without a token", rec.Code)
			}
		})
	}
}

// The route-read endpoint is public: a client previewing /routes/:slug has no
// session yet, so it must not require one.
func TestRouteReadEndpointStaysOpenWithoutAToken(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/routes/no-such-route", "")
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("status = %d, route read must not require auth", rec.Code)
	}
	// stubAuthDeps has no routes, so an anonymous caller should see the
	// ordinary not-found response rather than an auth rejection.
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown slug", rec.Code)
	}
}

// The route list shares the read endpoint's public posture: the picker that
// consumes it runs before the user has picked anything, let alone signed in.
// A 200 here also proves the sibling /api/routes/{slug} read does not shadow
// the collection.
func TestRouteListEndpointStaysOpenWithoutAToken(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/routes", "")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 without a token; body %s", rec.Code, rec.Body.String())
	}
}

// The snap preview is public for the same reason as the route read it
// projects against, and it is registered under POST — a client sending one
// must not be met with a 405 from the GET-only read patterns beside it.
func TestSnapStopsEndpointStaysOpenWithoutAToken(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodPost, "/api/routes/no-such-route/snap-stops", "",
		`{"stops":[{"lat":37.79,"lng":-122.4}]}`)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("status = %d, the snap preview must not require auth", rec.Code)
	}
	// stubAuthDeps has no routes, so an anonymous caller should see the
	// ordinary not-found response rather than an auth rejection or a 405.
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown slug", rec.Code)
	}
}

// The compiled-graph read is public, like the other scenario reads — a
// caller checking whether a compile has finished has no session yet either.
func TestGraphEndpointStaysOpenWithoutAToken(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/scenarios/ca-hsr/graph", "")
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Errorf("status = %d, the compiled graph read must not require auth", rec.Code)
	}
	// stubAuthDeps has no succeeded jobs, so this is "not compiled yet", not an
	// auth rejection.
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 with nothing compiled yet", rec.Code)
	}
}

// With no database there is no user or session store, so the auth endpoints
// must say so plainly rather than 404 or panic.
func TestAuthRoutesReportUnavailableWithoutADatabase(t *testing.T) {
	h := newTestServer(t, nil)

	for _, p := range []struct{ method, path string }{
		{http.MethodPost, "/api/auth/login"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPost, "/api/admin/users"},
		{http.MethodGet, "/api/me/scenarios"},
		{http.MethodPost, "/api/scenarios/ca-hsr/compile"},
		{http.MethodGet, "/api/jobs/some-id"},
		{http.MethodPost, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios"},
		{http.MethodGet, "/api/user-scenarios/some-slug"},
	} {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			rec := request(t, h, p.method, p.path, adminToken)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 with no database configured", rec.Code)
			}
		})
	}

	// The public read path must still work in database-less local dev.
	if rec := request(t, h, http.MethodGet, "/api/scenarios", ""); rec.Code != http.StatusOK {
		t.Errorf("public route status = %d, want 200 without a database", rec.Code)
	}

	// Route reads live in Postgres, so without a database they must say so
	// plainly too, rather than 404ing as if the endpoint didn't exist.
	if rec := request(t, h, http.MethodGet, "/api/routes/ca-hsr-trunk", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("route read status = %d, want 503 with no database configured", rec.Code)
	}

	// The collection is checked separately from the slug read above: it is a
	// distinct registration, for the reason given at registerRouteRoutes.
	if rec := request(t, h, http.MethodGet, "/api/routes", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("route list status = %d, want 503 with no database configured", rec.Code)
	}

	// The snap preview reads the same Postgres-backed geometry, so it must
	// answer 503 too rather than 404 as if it were never deployed.
	if rec := request(t, h, http.MethodPost, "/api/routes/ca-hsr-trunk/snap-stops", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("snap-stops status = %d, want 503 with no database configured", rec.Code)
	}

	// The compiled graph is likewise Postgres-backed and unauthenticated, so
	// it needs its own database-less check independent of the auth-gated loop
	// above.
	if rec := request(t, h, http.MethodGet, "/api/scenarios/ca-hsr/graph", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("graph read status = %d, want 503 with no database configured", rec.Code)
	}
}
