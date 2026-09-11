package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type AuthDeps interface {
	handler.AuthStore
	handler.UserStore
	handler.OwnerStore
	handler.RouteStore
	handler.CompileStore
	handler.ServiceStore
	handler.ScenarioStore
	handler.OwnedScenarioStore
	handler.OwnedStationStore
	handler.OwnedTravelTimesStore
	handler.OwnedServiceStore
	handler.OwnedRouteStore
	handler.RoutingStore
	handler.WorkerStore
	handler.RoutingBacklogStore
	handler.PrerenderedStore
	GetSessionUser(ctx context.Context, tokenHash string) (account.User, bool, error)
}

var _ AuthDeps = (*postgres.Repo)(nil)

func New(cfg config.Config, store *transit.Store, deps AuthDeps, publisher routing.Publisher, lg *slog.Logger) *http.Server {
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
	mux.HandleFunc("GET /api/scenarios/{slug}/services", handler.ScenarioServices(store, cfg.BoardingWait))
	mux.HandleFunc("GET /api/scenarios/{slug}/stations", handler.ScenarioStations(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/travel-times", handler.ScenarioTravelTimes(store))

	// One cap shared by all three isochrone endpoints: they enqueue onto the
	// same queue for the same single worker, so a per-endpoint ceiling would
	// bound nothing. Built here rather than at each registration so the
	// "disabled" warning is logged once (SPA-219).
	capBacklog := passThrough
	if deps != nil {
		capBacklog = handler.CapIsochroneBacklog(deps, cfg.MaxInFlightIsochrones, lg)
	}

	registerRouteRoutes(mux, deps)
	registerCompileRoutes(mux, deps, publisher, capBacklog, lg)
	registerPrerenderedRoutes(mux, deps)
	registerAuthRoutes(mux, cfg, deps, publisher, capBacklog, lg)
	registerWorkerRoutes(mux, cfg, deps)

	h := cors(mux, cfg.AllowLocalhostCORS)

	return &http.Server{
		Addr: ":" + cfg.Port,
		// traceid.Middleware runs outermost: logRequests reads the trace id it
		// attaches, and every handler downstream that enqueues routing work
		// forwards the same id to the worker (see handler.enqueueIsochrone).
		Handler:           traceid.Middleware(logRequests(lg, h)),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func registerRouteRoutes(mux *http.ServeMux, deps AuthDeps) {
	if deps == nil {
		// The collection needs its own entry alongside the subtree: /api/routes/
		// does not serve /api/routes, it makes the mux answer that path with a
		// 307 to the trailing-slash form. Without this the list would redirect
		// rather than report itself unavailable.
		mux.HandleFunc("/api/routes", noDatabase("route storage is unavailable"))
		mux.HandleFunc("/api/routes/", noDatabase("route storage is unavailable"))
		return
	}
	// The list filters in SQL, so it needs no identity: ListCuratedRouteSummaries
	// never loads an owned row.
	mux.HandleFunc("GET /api/routes", handler.Routes(deps))

	// The two by-slug paths take OptionalAuth rather than nothing. They stay
	// public — the curated alignments are what they exist to serve — but a
	// route now has an owner, and without an identity on the context an owner
	// could not read back their own draft here, while everyone else would be
	// able to confirm it exists by guessing its slug. Same shape as
	// GET /api/routing-jobs/{id}, which is public for an unowned job and
	// owner-scoped for an owned one.
	optional := auth.OptionalAuth(deps.GetSessionUser)
	mux.Handle("GET /api/routes/{slug}", optional(handler.RouteBySlug(deps)))
	mux.Handle("POST /api/routes/{slug}/snap-stops", optional(handler.SnapStops(deps)))
}

func registerCompileRoutes(mux *http.ServeMux, deps AuthDeps, publisher routing.Publisher,
	capBacklog func(http.Handler) http.Handler, lg *slog.Logger) {
	if deps == nil {
		mux.HandleFunc("GET /api/scenarios/{slug}/graph", noDatabase("compiled graph storage is unavailable"))
		mux.HandleFunc("POST /api/isochrone", noDatabase("compiled graph storage is unavailable"))
		mux.HandleFunc("GET /api/routing-jobs/{id}", noDatabase("routing job storage is unavailable"))
		return
	}
	// All three are public, and all three take OptionalAuth for the same
	// reason: a scenario now has an owner, so each has a curated half anyone
	// may reach and an owned half only its owner may. Requiring auth would
	// take the seeded data away from the anonymous callers these exist for;
	// omitting it would hand an owner's compiled graph to anyone with the slug.
	optional := auth.OptionalAuth(deps.GetSessionUser)
	mux.Handle("GET /api/scenarios/{slug}/graph", optional(handler.ScenarioGraph(deps)))
	mux.Handle("POST /api/isochrone",
		optional(requirePublisher(publisher, capBacklog(handler.Isochrone(deps, publisher, lg)))))
	mux.Handle("GET /api/routing-jobs/{id}", optional(handler.RoutingJobStatus(deps)))
}

func registerPrerenderedRoutes(mux *http.ServeMux, deps AuthDeps) {
	const unavailable = "prerendered isochrone storage is unavailable"
	if deps == nil {
		mux.HandleFunc("/api/scenarios/{slug}/prerendered-isochrones", noDatabase(unavailable))
		mux.HandleFunc("/api/prerendered-isochrones/{id}", noDatabase(unavailable))
		return
	}
	mux.HandleFunc("GET /api/scenarios/{slug}/prerendered-isochrones", handler.PrerenderedIsochrones(deps))
	mux.HandleFunc("GET /api/prerendered-isochrones/{id}", handler.PrerenderedIsochrone(deps))
}

func passThrough(next http.Handler) http.Handler { return next }

func requirePublisher(publisher routing.Publisher, h http.Handler) http.Handler {
	if publisher == nil {
		return serviceUnavailable("the routing queue is unavailable: no broker configured")
	}
	return h
}

func registerAuthRoutes(mux *http.ServeMux, cfg config.Config, deps AuthDeps, publisher routing.Publisher,
	capBacklog func(http.Handler) http.Handler, lg *slog.Logger) {
	if deps == nil {
		for _, pattern := range []string{
			"/api/auth/login", "/api/auth/logout", "/api/auth/me",
			"/api/me/scenarios", "/api/me/services",
			// The owner-scoped seeded-model CRUD. Each collection needs its own
			// entry alongside its subtree: "/api/me/routes/" does not serve
			// "/api/me/routes", it makes the mux answer that path with a 307 to
			// the trailing-slash form — so without both, the list would redirect
			// rather than report itself unavailable. The two entries above are
			// exact paths and do not cover their {slug} subtrees either.
			"/api/me/routes", "/api/me/routes/",
			"/api/me/scenarios/", "/api/me/services/",
			"/api/admin/",
			"/api/scenarios/{slug}/compile", "/api/jobs/{id}",
			"/api/services", "/api/services/",
			"/api/user-scenarios", "/api/user-scenarios/",
		} {
			mux.HandleFunc(pattern, noDatabase("authentication is unavailable"))
		}
		return
	}

	authenticated := auth.RequireAuth(deps.GetSessionUser)
	adminOnly := auth.RequireAdmin(deps.GetSessionUser)

	// One hasher for both password paths. Login's constant-time padding has to
	// spend the same work provisioning does, so they must not be built with
	// different costs. cfg.PasswordHashCost is zero everywhere but the tests,
	// where it is bcrypt.MinCost.
	hasher := auth.NewHasher(cfg.PasswordHashCost)

	// Public: the only unauthenticated auth route. There is deliberately no
	// registration endpoint — accounts come from POST /api/admin/users.
	mux.HandleFunc("POST /api/auth/login", handler.Login(deps, cfg.SessionTTL, hasher))

	// Authenticated.
	mux.Handle("POST /api/auth/logout", authenticated(handler.Logout(deps)))
	mux.Handle("GET /api/auth/me", authenticated(handler.Me()))
	mux.Handle("GET /api/me/scenarios", authenticated(handler.MyScenarios(deps)))
	mux.Handle("GET /api/me/services", authenticated(handler.MyServices(deps, cfg.BoardingWait)))
	// Async compile jobs: any authenticated caller may trigger a compile or
	// poll a job. JobStatus enforces ownership itself (see its doc comment),
	// since "not found" there means something different from "not admin".
	mux.Handle("POST /api/scenarios/{slug}/compile", authenticated(handler.CompileScenario(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/jobs/{id}", authenticated(handler.JobStatus(deps)))

	// Owner-scoped CRUD over the seeded route model. Distinct from the public
	// /api/routes reads registered in registerRouteRoutes: those serve the
	// curated alignments anyone may pick from, while these are a caller's own
	// drafts, so they sit behind the auth gate rather than beside them.
	//
	// /api/me is where the owner-scoped view of the seeded models already
	// lives — GET /api/me/scenarios and /api/me/services predate this — so the
	// ownership scope is in the path where a reader sees it.
	mux.Handle("POST /api/me/routes", authenticated(handler.CreateOwnedRoute(deps)))
	mux.Handle("GET /api/me/routes", authenticated(handler.MyRoutes(deps)))
	mux.Handle("GET /api/me/routes/{slug}", authenticated(handler.GetOwnedRoute(deps)))
	mux.Handle("PUT /api/me/routes/{slug}", authenticated(handler.UpdateOwnedRoute(deps)))
	mux.Handle("DELETE /api/me/routes/{slug}", authenticated(handler.DeleteOwnedRoute(deps)))

	// Owner-scoped CRUD over the seeded scenario model. The list read at
	// GET /api/me/scenarios is registered above and predates the writes.
	//
	// Distinct from /api/user-scenarios, which curates a set of UserService
	// ids: this is a scenario in the seeded sense — it holds its own routes,
	// stations, segments, and services, and compiles through the same path the
	// ca-hsr baseline does.
	mux.Handle("POST /api/me/scenarios", authenticated(handler.CreateOwnedScenario(deps)))
	mux.Handle("GET /api/me/scenarios/{slug}", authenticated(handler.GetOwnedScenario(deps)))
	mux.Handle("PUT /api/me/scenarios/{slug}", authenticated(handler.UpdateOwnedScenario(deps)))
	mux.Handle("DELETE /api/me/scenarios/{slug}", authenticated(handler.DeleteOwnedScenario(deps)))

	// A scenario's stations, and the segment run times between them. Together
	// with its routes and services these are what make an owned scenario
	// compilable — without stations there is nothing for a service to stop at,
	// and without segments the compiler has no path to place those stops on.
	//
	// Stations are addressed by (scenario, slug) because stations.slug is
	// unique per scenario rather than globally.
	mux.Handle("GET /api/me/scenarios/{slug}/stations", authenticated(handler.ListOwnedStations(deps)))
	mux.Handle("POST /api/me/scenarios/{slug}/stations", authenticated(handler.CreateOwnedStation(deps)))
	mux.Handle("PUT /api/me/scenarios/{slug}/stations/{stationSlug}", authenticated(handler.UpdateOwnedStation(deps)))
	mux.Handle("DELETE /api/me/scenarios/{slug}/stations/{stationSlug}", authenticated(handler.DeleteOwnedStation(deps)))

	// Travel times are written as a whole set rather than per segment: a
	// segment has no identity a client can address, and the set is meaningful
	// only entire — the compiler walks it as one graph.
	mux.Handle("GET /api/me/scenarios/{slug}/travel-times", authenticated(handler.GetOwnedTravelTimes(deps)))
	mux.Handle("PUT /api/me/scenarios/{slug}/travel-times", authenticated(handler.ReplaceOwnedTravelTimes(deps)))

	// Owner-scoped CRUD over the seeded service model. Addressed by id, not
	// slug: the services table has no slug column, which removes slug minting
	// from this model entirely.
	//
	// Distinct from /api/services, which is the self-contained UserService with
	// its own inline vehicle and embedded stops. These are seeded services:
	// they reference a scenario's stations and the shared vehicle-type catalog,
	// and compile through the same path the ca-hsr baseline does.
	mux.Handle("POST /api/me/services", authenticated(handler.CreateOwnedService(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/me/services/{id}", authenticated(handler.GetOwnedService(deps, cfg.BoardingWait)))
	mux.Handle("PUT /api/me/services/{id}", authenticated(handler.UpdateOwnedService(deps, cfg.BoardingWait)))
	mux.Handle("DELETE /api/me/services/{id}", authenticated(handler.DeleteOwnedService(deps)))

	// User-authored services: owner-scoped CRUD. Reads are owner-scoped too —
	// unlike the curated scenario data these are a user's own drafts, so they
	// sit behind the same gate as the writes rather than the public reads.
	mux.Handle("POST /api/services", authenticated(handler.CreateService(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/services", authenticated(handler.MyUserServices(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/services/{slug}", authenticated(handler.GetService(deps, cfg.BoardingWait)))
	mux.Handle("PUT /api/services/{slug}", authenticated(handler.UpdateService(deps, cfg.BoardingWait)))
	mux.Handle("DELETE /api/services/{slug}", authenticated(handler.DeleteService(deps)))
	// Compiling a single service is the degenerate scenario compile; owner-scoped
	// like the rest of the authored surface.
	mux.Handle("POST /api/services/{slug}/compile", authenticated(handler.CompileUserService(deps, cfg.BoardingWait)))
	// Read that compile back, and plot over it, without wrapping the service in
	// a scenario first (SPA-140). Twins of the /api/user-scenarios pair below,
	// owner-scoped identically. The database-less 503 list above needs no entry
	// for either: "/api/services/" is a subtree pattern and already covers them.
	mux.Handle("GET /api/services/{slug}/graph", authenticated(handler.UserServiceGraph(deps)))
	mux.Handle("POST /api/services/{slug}/isochrone",
		authenticated(requirePublisher(publisher,
			capBacklog(handler.UserServiceIsochrone(deps, publisher, lg, cfg.BoardingWait)))))

	// User-owned scenarios: owner-scoped CRUD over a curated set of UserService
	// ids. Named /api/user-scenarios, distinct from the public /api/scenarios
	// collection above, so the existing curated read path is untouched rather
	// than repurposed or ambiguously overloaded.
	mux.Handle("POST /api/user-scenarios", authenticated(handler.CreateUserScenario(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/user-scenarios", authenticated(handler.MyUserScenarios(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/user-scenarios/{slug}", authenticated(handler.GetUserScenario(deps, cfg.BoardingWait)))
	mux.Handle("PUT /api/user-scenarios/{slug}", authenticated(handler.UpdateUserScenario(deps, cfg.BoardingWait)))
	mux.Handle("DELETE /api/user-scenarios/{slug}", authenticated(handler.DeleteUserScenario(deps)))
	// Compile a user scenario's curated members into one graph, then read it back
	// by slug. Both owner-scoped, unlike the public seeded /api/scenarios/{slug}/graph.
	mux.Handle("POST /api/user-scenarios/{slug}/compile", authenticated(handler.CompileUserScenario(deps, cfg.BoardingWait)))
	mux.Handle("GET /api/user-scenarios/{slug}/graph", authenticated(handler.UserScenarioGraph(deps)))
	// The user-authored counterpart to POST /api/isochrone (SPA-83): computes
	// over the scenario's compiled graph rather than the seeded store, and
	// answers 409 with a distinct code when that graph is stale (SPA-116).
	mux.Handle("POST /api/user-scenarios/{slug}/isochrone",
		authenticated(requirePublisher(publisher,
			capBacklog(handler.UserScenarioIsochrone(deps, publisher, lg, cfg.BoardingWait)))))

	// Admin-only.
	mux.Handle("POST /api/admin/users", adminOnly(handler.CreateUser(deps, hasher)))
	mux.Handle("POST /api/admin/routes", adminOnly(handler.CreateRoute(deps)))
	// Curating a prerendered isochrone is editorial content on a public page,
	// so it sits behind the same admin gate — even though it hangs off the
	// public /api/scenarios path rather than /api/admin. Its two sibling reads
	// are registered in registerPrerenderedRoutes; the database-less 503 for
	// this path comes from there too, so the list above needs no entry.
	mux.Handle("POST /api/scenarios/{slug}/prerendered-isochrones",
		adminOnly(handler.CreatePrerenderedIsochrone(deps)))
}

func registerWorkerRoutes(mux *http.ServeMux, cfg config.Config, deps AuthDeps) {
	const unavailable = "worker API is unavailable"
	if deps == nil {
		mux.HandleFunc("/api/internal/", noDatabase(unavailable))
		return
	}
	if cfg.WorkerToken == "" {
		mux.HandleFunc("/api/internal/", serviceUnavailable(unavailable+": no WORKER_TOKEN configured"))
		return
	}
	gate := auth.RequireWorkerToken(cfg.WorkerToken)
	mux.Handle("GET /api/internal/worker", gate(handler.WorkerReady()))
	mux.Handle("POST /api/internal/routing-jobs/{id}/running", gate(handler.WorkerMarkRunning(deps)))
	mux.Handle("POST /api/internal/routing-jobs/{id}/succeeded", gate(handler.WorkerMarkSucceeded(deps)))
	mux.Handle("POST /api/internal/routing-jobs/{id}/failed", gate(handler.WorkerMarkFailed(deps)))
	mux.Handle("POST /api/internal/isochrone-cache/lookup", gate(handler.WorkerCacheLookup(deps)))
	mux.Handle("POST /api/internal/isochrone-cache", gate(handler.WorkerCachePut(deps)))
}

func noDatabase(what string) http.HandlerFunc {
	return serviceUnavailable(what + ": no database configured")
}

func serviceUnavailable(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		body := `{"error":"` + msg + `"}` + "\n"
		if _, err := w.Write([]byte(body)); err != nil {
			slog.ErrorContext(r.Context(), "server: failed to write response", "error", err)
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func logRequests(lg *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		trace, _ := traceid.FromContext(r.Context())
		lg.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"trace_id", trace,
		)
	})
}

var allowedOrigins = map[string]bool{
	"https://sparks-effect-website.vercel.app": true,
}

const vercelPreviewHost = "andrewcgraves-projects.vercel.app"

const sparksEffectHost = "sparks-effect.app"

func cors(next http.Handler, allowLocalhost bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if originAllowed(origin, allowLocalhost) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Trace-Id")
			// Response headers a browser client may read. Without this the
			// Retry-After on a capped isochrone's 429 (SPA-219) is invisible
			// to the SPA, which is the one caller it is written for.
			w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(origin string, allowLocalhost bool) bool {
	if origin == "" {
		return false
	}
	if allowedOrigins[origin] || isSparksEffectOrigin(origin) || isVercelPreviewOrigin(origin) {
		return true
	}
	return allowLocalhost && isLocalhostOrigin(origin)
}

func isSparksEffectOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == sparksEffectHost || strings.HasSuffix(host, "."+sparksEffectHost)
}

func isVercelPreviewOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if host == vercelPreviewHost {
		return true
	}
	return strings.HasSuffix(host, "."+vercelPreviewHost) ||
		strings.HasSuffix(host, "-"+vercelPreviewHost)
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
