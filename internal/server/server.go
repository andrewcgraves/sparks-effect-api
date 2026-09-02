package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
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
	// OwnedScenarioStore backs the owner-scoped CRUD over the seeded scenario
	// model, whose table the curated ca-hsr baseline also lives in.
	handler.OwnedScenarioStore
	// OwnedStationStore and OwnedTravelTimesStore back the two surfaces that
	// make an owned scenario compilable rather than an empty shell: its
	// stations, and the segment run times between them.
	handler.OwnedStationStore
	handler.OwnedTravelTimesStore
	// OwnedServiceStore backs the owner-scoped CRUD over the seeded service
	// model — the operating patterns that run over an owned scenario's routes.
	handler.OwnedServiceStore
	// OwnedRouteStore backs the owner-scoped CRUD over the seeded route model,
	// which shares its table with the curated alignments RouteStore reads.
	handler.OwnedRouteStore
	// RoutingStore backs the isochrone enqueue surface and the routing job poll.
	handler.RoutingStore
	// WorkerStore backs the authenticated write surface the routing worker
	// uses instead of a database connection (SPA-273).
	handler.WorkerStore
	// RoutingBacklogStore backs the enqueue cap that refuses isochrones once
	// too much routing work is already outstanding (SPA-219).
	handler.RoutingBacklogStore
	// PrerenderedStore backs the curated isochrone surface: two public reads
	// and the admin write that curates one.
	handler.PrerenderedStore
	// GetSessionUser backs the middleware's auth.SessionLookup.
	GetSessionUser(ctx context.Context, tokenHash string) (transit.User, bool, error)
}

// New builds an *http.Server with all routes registered, ready to be
// started by the caller.
//
// deps may be nil when no database is configured, and publisher may be nil when
// no broker is. Each missing dependency turns the routes that need it into
// 503s rather than 404s, so a client can tell "not deployed with that piece"
// from "no such endpoint". The isochrone routes need both: since SPA-182 they
// resolve a compiled graph out of Postgres and hand it to the routing worker
// over the queue, computing nothing themselves.
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

// registerRouteRoutes wires the public route surface: the collection a picker
// lists from, one route by slug, and the stop-snapping preview built on that
// same geometry. Ingested routes live in Postgres, not the embedded scenario
// store, so with no database configured they answer 503 rather than 404.
//
// The preview is public for the same reason the reads are: it projects onto an
// alignment anyone may already fetch, and tells a caller nothing that geometry
// does not. It is a POST only because it carries a body — hence the name here
// is not "read routes", though nothing registered below writes anything.
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

// registerCompileRoutes wires the public half of the async job model: the
// compiled graph fetched by scenario slug, the seeded isochrone enqueued over
// that same graph, and the poll that answers for it. Triggering a compile and
// polling a *compile* job both require authentication and are registered in
// registerAuthRoutes instead, alongside the other identity-gated routes.
//
// The seeded isochrone belongs here rather than beside the embedded-store reads
// above because since SPA-181 it resolves its transit data through the compile
// job that produced the scenario's graph, exactly as the authored isochrones
// do. That also makes it Postgres-backed: with no database configured there are
// no compile jobs to resolve, so it answers 503 like every other route whose
// storage is missing, rather than silently falling back to a graph with no
// identity to offer.
//
// The routing job poll is public in the same sense the seeded isochrone is:
// registered without a gate, but wrapped in auth.OptionalAuth so an owned job
// can still recognise its owner. See handler.RoutingJobStatus for the rule.
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

// registerPrerenderedRoutes wires the public half of the curated isochrone
// surface: a scenario's entries, and one of them in full. The admin write that
// creates one is registered in registerAuthRoutes instead, beside the other
// adminOnly routes.
//
// The entries live in Postgres, so with no database configured these answer
// 503 rather than 404. Both database-less patterns are registered without a
// method, which is what also covers the admin POST at the collection path —
// registerAuthRoutes' own 503 list therefore needs no entry for it.
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

// passThrough is the identity middleware, used for the enqueue cap in a build
// with no database: there is nothing to count in-flight jobs in, and the
// isochrone routes are registered as 503s in that build anyway.
func passThrough(next http.Handler) http.Handler { return next }

// requirePublisher guards an isochrone endpoint with the broker it depends on.
//
// An isochrone is now entirely someone else's work: with no queue to publish
// to there is no way to do it and no partial answer worth inventing. Answering
// 503 up front is more honest than accepting the request, recording a routing
// job, and immediately marking it failed — which is what the handler would do
// with a publisher that cannot reach anything.
func requirePublisher(publisher routing.Publisher, h http.Handler) http.Handler {
	if publisher == nil {
		return serviceUnavailable("the routing queue is unavailable: no broker configured")
	}
	return h
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

	// Public: the only unauthenticated auth route. There is deliberately no
	// registration endpoint — accounts come from POST /api/admin/users.
	mux.HandleFunc("POST /api/auth/login", handler.Login(deps, cfg.SessionTTL))

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
	mux.Handle("POST /api/admin/users", adminOnly(handler.CreateUser(deps)))
	mux.Handle("POST /api/admin/routes", adminOnly(handler.CreateRoute(deps)))
	// Curating a prerendered isochrone is editorial content on a public page,
	// so it sits behind the same admin gate — even though it hangs off the
	// public /api/scenarios path rather than /api/admin. Its two sibling reads
	// are registered in registerPrerenderedRoutes; the database-less 503 for
	// this path comes from there too, so the list above needs no entry.
	mux.Handle("POST /api/scenarios/{slug}/prerendered-isochrones",
		adminOnly(handler.CreatePrerenderedIsochrone(deps)))
}

// registerWorkerRoutes wires the authenticated write surface the routing
// worker uses to record job results and reuse egress polygons (SPA-273).
//
// These used to be SQL the worker ran against a DATABASE_URL. Exposing that
// database past the API's private network is what this gate exists to stop:
// the worker presents a shared bearer token, not a user session, and the
// SQL stays in this process.
//
// A missing database or a missing WORKER_TOKEN both 503 rather than 404, and
// rather than leaving the writes unauthenticated. An empty token matching an
// empty Authorization header would be worse than not registering the routes.
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

// noDatabase answers 503 for a route whose backing store is Postgres when no
// database is configured — the great majority of them, hence its own wrapper
// over serviceUnavailable rather than the suffix repeated at each call site.
func noDatabase(what string) http.HandlerFunc {
	return serviceUnavailable(what + ": no database configured")
}

// serviceUnavailable answers 503 for a route one of whose dependencies is not
// configured — Postgres for most, the queue broker for the isochrones — so a
// client can tell "not deployed with that piece" from "no such endpoint".
//
// msg is the whole message, including which dependency is missing: it used to
// name the database itself, which stopped being true once a second dependency
// could be the one missing. Most callers go through noDatabase above.
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

// statusRecorder captures the status code a handler answers with, so
// logRequests can log it: http.ResponseWriter has no getter of its own, and
// an access log that cannot say whether a request succeeded is not worth
// having.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// logRequests logs one structured line per request — method, path, status,
// duration, and the request's trace id — after it completes. It must sit
// inside traceid.Middleware so the trace id is already on the context by the
// time it reads it.
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

// allowedOrigins are exact Origins always permitted for CORS, regardless of
// the ALLOW_LOCALHOST_CORS testing flag. Production SPA hosts live here.
var allowedOrigins = map[string]bool{
	"https://sparks-effect-website.vercel.app": true,
}

// vercelPreviewHost is the Vercel team that hosts preview deployments
// (SPA-252). Those previews talk to the staging API, so they must be allowed
// the same way production is. The matcher is the team suffix, not a branch
// name: Vercel assigns a new hostname per deployment (a 9-character hash, or
// a truncated git alias plus a short slug) and the DNS label is capped at 63
// characters, so the branch rarely appears in full. What is stable is
// -<team>.vercel.app. Not a wildcard *.vercel.app, and not the production
// alias already in allowedOrigins.
const vercelPreviewHost = "andrewcgraves-projects.vercel.app"

// sparksEffectHost is the project's own domain. Every host under it — the
// apex, dev., and whatever subdomain comes next — serves this project's own
// frontend against this API, so the domain is allowed as a whole rather than
// re-listed in allowedOrigins each time one appears.
//
// Unlike the Vercel team slug above, the match here requires a real subdomain
// boundary: the dot is part of the suffix, so a lookalike registration such as
// notsparks-effect.app cannot slip in on a bare string suffix.
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

// isSparksEffectOrigin reports whether origin is an HTTPS host on the project's
// own domain — the apex sparks-effect.app or any subdomain of it, such as
// dev.sparks-effect.app. HTTPS only, matching the rest of the allowlist: the
// domain is served over TLS, so a plaintext Origin claiming it is not one of
// ours.
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

// isVercelPreviewOrigin reports whether origin is an HTTPS deployment on the
// Vercel team that hosts this project's previews. Real hosts from this
// project look like sparks-effect-website-git-claude-2643c5-andrewcgraves-projects.vercel.app
// (truncated git alias) or sparks-effect-website-<9-char-hash>-andrewcgraves-projects.vercel.app
// (per-commit URL). The team slug is a suffix of the DNS label, not a
// subdomain — there is no extra dot before it.
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
