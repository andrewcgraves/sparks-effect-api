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
	// GetSessionUser backs the middleware's auth.SessionLookup.
	GetSessionUser(ctx context.Context, tokenHash string) (transit.User, bool, error)
}

// New builds an *http.Server with all routes registered, ready to be
// started by the caller. deps may be nil when no database is configured.
func New(cfg config.Config, store *transit.Store, deps AuthDeps, chainer isochrone.Chainer, lg *logger.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handler.Health)

	// Public reads: the curated scenario data, unauthenticated by design.
	mux.HandleFunc("GET /api/scenarios", handler.Scenarios(store))
	mux.HandleFunc("GET /api/scenarios/{slug}", handler.ScenarioBySlug(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/routes", handler.ScenarioRoutes(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/services", handler.ScenarioServices(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/stations", handler.ScenarioStations(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/travel-times", handler.ScenarioTravelTimes(store))

	mux.HandleFunc("POST /api/isochrone", handler.Isochrone(chainer, lg))

	registerAuthRoutes(mux, cfg, deps)

	h := cors(mux, cfg.AllowLocalhostCORS)

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logRequests(h),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// registerAuthRoutes wires the invite-only auth surface.
//
// Routes are grouped by the gate they sit behind, so the protection of an
// endpoint is visible at its registration rather than buried in the handler:
//
//   - public:        login only — the way in.
//   - authenticated: identity and the caller's own scenarios/services.
//   - admin:         account provisioning. SPA-75's route-write endpoints
//     register here too, by wrapping them in adminOnly — whatever path they
//     take. Note the database-less 503 list below is matched by path, so a new
//     path must be added there as well or it will 404 in that build.
//
// With no database configured there is nothing to authenticate against, so
// every route answers 503 rather than 404 — a client can tell "not deployed
// with auth" from "no such endpoint".
func registerAuthRoutes(mux *http.ServeMux, cfg config.Config, deps AuthDeps) {
	if deps == nil {
		for _, pattern := range []string{
			"/api/auth/login", "/api/auth/logout", "/api/auth/me",
			"/api/me/scenarios", "/api/me/services", "/api/admin/",
		} {
			mux.HandleFunc(pattern, authUnavailable)
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

	// Admin-only.
	mux.Handle("POST /api/admin/users", adminOnly(handler.CreateUser(deps)))
}

func authUnavailable(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	if _, err := w.Write([]byte(`{"error":"authentication is unavailable: no database configured"}` + "\n")); err != nil {
		log.Printf("server: failed to write response: %v", err)
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
