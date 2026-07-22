package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// AuthDeps is the persistence the authenticated routes need: credential
// lookup, session storage, account provisioning, and owner-scoped reads.
// *postgres.Repo satisfies it.
//
// It is nil when the server runs without a database (the read-only embedded
// store used for local dev), in which case the authenticated routes are
// registered as 503s — see registerAuthRoutes.
type AuthDeps interface {
	handler.AuthStore
	handler.UserStore
	handler.OwnerStore
	// RouteStore backs admin route ingestion and the public route-read endpoint.
	handler.RouteStore
	// CompileStore backs the async compile job surface: triggering a scenario
	// compile, polling its job, and reading the compiled graph back by slug.
	handler.CompileStore
	// ServiceStore backs the user-authored service CRUD endpoints.
	handler.ServiceStore
	// ScenarioStore backs the user-owned scenario CRUD endpoints.
	handler.ScenarioStore
	// GetSessionUser backs the middleware's auth.SessionLookup.
	GetSessionUser(ctx context.Context, tokenHash string) (transit.User, bool, error)
}

// New builds an *http.Server with all routes registered, ready to be
// started by the caller. deps may be nil when no database is configured.
func New(cfg config.Config, store *transit.Store, deps AuthDeps, chainer isochrone.Chainer, lg *logger.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handler.Health)

	// Public reads: the curated scenario data, unauthenticated by design.
	//
	// This is a distinct resource from the owner-scoped UserScenario CRUD
	// registered below at /api/user-scenarios: these routes read the seeded,
	// compiled TransitGraph store and must keep answering exactly what they
	// answer today. Rather than repurpose /api/scenarios/{slug} for both, the
	// new resource lives at a path of its own — see registerAuthRoutes.
	mux.HandleFunc("GET /api/scenarios", handler.Scenarios(store))
	mux.HandleFunc("GET /api/scenarios/{slug}", handler.ScenarioBySlug(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/routes", handler.ScenarioRoutes(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/services", handler.ScenarioServices(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/stations", handler.ScenarioStations(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/travel-times", handler.ScenarioTravelTimes(store))

	mux.HandleFunc("POST /api/isochrone", handler.Isochrone(chainer, lg))

	registerRouteReadRoutes(mux, deps)
	registerCompileRoutes(mux, deps)
	registerAuthRoutes(mux, cfg, deps)

	h := cors(mux, cfg.AllowLocalhostCORS)

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logRequests(h),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// registerRouteReadRoutes wires the public route-read endpoints: the
// collection a picker lists from, and one route by slug. Ingested routes live
// in Postgres, not the embedded scenario store, so with no database configured
// they answer 503 rather than 404.
func registerRouteReadRoutes(mux *http.ServeMux, deps AuthDeps) {
	if deps == nil {
		// The collection is registered separately from the subtree: a pattern of
		// /api/routes/ does not serve /api/routes, it redirects to it, so without
		// its own entry the list would answer 301 rather than 503.
		mux.HandleFunc("/api/routes", serviceUnavailable("route storage is unavailable"))
		mux.HandleFunc("/api/routes/", serviceUnavailable("route storage is unavailable"))
		return
	}
	mux.HandleFunc("GET /api/routes", handler.Routes(deps))
	mux.HandleFunc("GET /api/routes/{slug}", handler.RouteBySlug(deps))
}

// registerCompileRoutes wires the public read half of the async compile job
// model: the compiled graph, fetched by scenario slug. Triggering a compile
// and polling the resulting job both require authentication and are
// registered in registerAuthRoutes instead, alongside the other identity-gated
// routes.
func registerCompileRoutes(mux *http.ServeMux, deps AuthDeps) {
	if deps == nil {
		mux.HandleFunc("GET /api/scenarios/{slug}/graph", serviceUnavailable("compiled graph storage is unavailable"))
		return
	}
	mux.HandleFunc("GET /api/scenarios/{slug}/graph", handler.ScenarioGraph(deps))
}

// registerAuthRoutes wires the invite-only auth surface.
//
// Routes are grouped by the gate they sit behind, so the protection of an
// endpoint is visible at its registration rather than buried in the handler:
//
//   - public:        login only — the way in.
//   - authenticated: identity and the caller's own scenarios/services.
//   - admin:         account provisioning and route ingestion. Further
//     admin-only writes register here too, by wrapping them in adminOnly. Note
//     the database-less 503 list below is matched by path, so a new path must
//     be added there as well or it will 404 in that build — anything under
//     /api/admin/ is already covered by the prefix entry.
//
// With no database configured there is nothing to authenticate against, so
// every route answers 503 rather than 404 — a client can tell "not deployed
// with auth" from "no such endpoint".
func registerAuthRoutes(mux *http.ServeMux, cfg config.Config, deps AuthDeps) {
	if deps == nil {
		for _, pattern := range []string{
			"/api/auth/login", "/api/auth/logout", "/api/auth/me",
			"/api/me/scenarios", "/api/me/services", "/api/admin/",
			"/api/scenarios/{slug}/compile", "/api/jobs/{id}",
			"/api/services", "/api/services/",
			"/api/user-scenarios", "/api/user-scenarios/",
		} {
			mux.HandleFunc(pattern, serviceUnavailable("authentication is unavailable"))
		}
		return
	}

	authenticated := auth.RequireAuth(deps.GetSessionUser)
	adminOnly := auth.RequireAdmin(deps.GetSessionUser)

	// Public: the only unauthenticated auth route. There is deliberately no
	// registration endpoint — accounts come from POST /api/admin/users.
	mux.HandleFunc("POST /api/auth/login", handler.Login(deps, cfg.SessionTTL))

	// Authenticated.
	mux.Handle("POST /api/auth/logout", authenticated(handler.Logout(deps)))
	mux.Handle("GET /api/auth/me", authenticated(handler.Me()))
	mux.Handle("GET /api/me/scenarios", authenticated(handler.MyScenarios(deps)))
	mux.Handle("GET /api/me/services", authenticated(handler.MyServices(deps)))
	// Async compile jobs: any authenticated caller may trigger a compile or
	// poll a job. JobStatus enforces ownership itself (see its doc comment),
	// since "not found" there means something different from "not admin".
	mux.Handle("POST /api/scenarios/{slug}/compile", authenticated(handler.CompileScenario(deps)))
	mux.Handle("GET /api/jobs/{id}", authenticated(handler.JobStatus(deps)))

	// User-authored services: owner-scoped CRUD. Reads are owner-scoped too —
	// unlike the curated scenario data these are a user's own drafts, so they
	// sit behind the same gate as the writes rather than the public reads.
	mux.Handle("POST /api/services", authenticated(handler.CreateService(deps)))
	mux.Handle("GET /api/services", authenticated(handler.MyUserServices(deps)))
	mux.Handle("GET /api/services/{slug}", authenticated(handler.GetService(deps)))
	mux.Handle("PUT /api/services/{slug}", authenticated(handler.UpdateService(deps)))
	mux.Handle("DELETE /api/services/{slug}", authenticated(handler.DeleteService(deps)))

	// User-owned scenarios: owner-scoped CRUD over a curated set of UserService
	// ids. Named /api/user-scenarios, distinct from the public /api/scenarios
	// collection above, so the existing curated read path is untouched rather
	// than repurposed or ambiguously overloaded.
	mux.Handle("POST /api/user-scenarios", authenticated(handler.CreateUserScenario(deps)))
	mux.Handle("GET /api/user-scenarios", authenticated(handler.MyUserScenarios(deps)))
	mux.Handle("GET /api/user-scenarios/{slug}", authenticated(handler.GetUserScenario(deps)))
	mux.Handle("PUT /api/user-scenarios/{slug}", authenticated(handler.UpdateUserScenario(deps)))
	mux.Handle("DELETE /api/user-scenarios/{slug}", authenticated(handler.DeleteUserScenario(deps)))

	// Admin-only.
	mux.Handle("POST /api/admin/users", adminOnly(handler.CreateUser(deps)))
	mux.Handle("POST /api/admin/routes", adminOnly(handler.CreateRoute(deps)))
}

// serviceUnavailable answers 503 for a route whose backing store is Postgres
// when no database is configured, so a client can tell "not deployed with a
// database" from "no such endpoint".
func serviceUnavailable(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		body := `{"error":"` + msg + `: no database configured"}` + "\n"
		if _, err := w.Write([]byte(body)); err != nil {
			log.Printf("server: failed to write response: %v", err)
		}
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// allowedOrigins are always permitted for CORS, regardless of the
// ALLOW_LOCALHOST_CORS testing flag.
var allowedOrigins = map[string]bool{
	"https://sparks-effect-website.vercel.app": true,
}

func cors(next http.Handler, allowLocalhost bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] || (allowLocalhost && isLocalhostOrigin(origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalhostOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	host := origin
	if i := strings.Index(origin, "://"); i >= 0 {
		host = origin[i+3:]
	}
	return strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")
}
