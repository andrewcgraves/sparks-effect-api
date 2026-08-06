package handler

import (
	"context"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// CompileUserService returns a handler for POST /api/services/{slug}/compile.
//
// It is the user-authored twin of CompileScenario: it enqueues a job and hands
// the compile to a background goroutine, returning the queued job with the same
// 202 and shape. It differs only in target — a single UserService, compiled
// alone as the degenerate one-member scenario (see transit.CompileUserScenario)
// — and in being owner-scoped: a caller may only compile their own service, and
// a service they do not own answers 404, exactly as the service CRUD does.
func CompileUserService(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compileAuthoredTarget(w, r, store, serviceTarget{store})
	}
}

// CompileUserScenario returns a handler for POST /api/user-scenarios/{slug}/compile.
//
// The scenario twin of CompileUserService: it compiles the caller's curated set
// of member services as one network. Owner-scoped identically — a scenario the
// caller does not own answers 404.
func CompileUserScenario(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compileAuthoredTarget(w, r, store, scenarioTarget{store})
	}
}

// compileAuthoredTarget triggers a compile of one authored target: the shared
// body of both compile handlers.
//
// Authentication is checked before the target is resolved, so an unauthenticated
// caller gets a 401 rather than the 404 an unknown slug would earn — the request
// is unauthenticated regardless of which slug it names.
func compileAuthoredTarget(w http.ResponseWriter, r *http.Request, store CompileStore, target authoredTarget) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	owned, ok := target.load(w, r)
	if !ok {
		return
	}

	job, ok := createCompileJob(w, r, store, owned.compileJob(user.ID))
	if !ok {
		return
	}
	enqueueCompile(store, job)
	writeJSON(w, http.StatusAccepted, job)
}

// UserScenarioGraph returns a handler for GET /api/user-scenarios/{slug}/graph:
// a user scenario's compiled graph, addressed by slug — the user-authored
// counterpart to ScenarioGraph.
//
// Unlike the seeded ScenarioGraph, which is public, this is owner-scoped: a user
// scenario is authored content, so the caller must own it (and a non-owner sees
// the same 404 as an unknown slug). Ownership is resolved by loading the
// scenario first, so the graph read never leaks a stranger's compiled result.
func UserScenarioGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetGraph(w, r, store, scenarioTarget{store})
	}
}

// UserServiceGraph returns a handler for GET /api/services/{slug}/graph: a
// single service's compiled graph, addressed by slug.
//
// The twin of UserScenarioGraph for a service compiled alone as the degenerate
// one-member scenario (see CompileUserService). Owner-scoped like the rest of
// the service CRUD — a service the caller does not own answers 404, not 403,
// so a non-owner cannot probe which slugs exist.
//
// Like the scenario graph read, this deliberately does not stale-check.
// Staleness is enforced on the isochrone, where an out-of-date graph would
// change the answer; drawing a slightly stale alignment would not.
func UserServiceGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetGraph(w, r, store, serviceTarget{store})
	}
}

// authoredTargetGraph reads one authored target's compiled graph: the shared
// body of both graph handlers.
//
// The compiled graph is pure topology — its edges carry no geometry — so a
// client that wants to draw the target needs the routes its member services run
// on too. They are loaded at read time and bundled alongside the graph,
// mirroring what the public /api/scenarios/{slug} read does; the persisted job
// result is left untouched.
func authoredTargetGraph(w http.ResponseWriter, r *http.Request, store routeStore, target authoredTarget) {
	owned, ok := target.load(w, r)
	if !ok {
		return
	}

	job, ok := loadCompiledGraph(w, r, owned)
	if !ok {
		return
	}

	_, members, err := owned.members(r.Context())
	if err != nil {
		writeInternalError(r.Context(), w, "loading member services", err)
		return
	}
	routes, err := routesByIDs(r.Context(), store, serviceRouteIDs(members))
	if err != nil {
		writeInternalError(r.Context(), w, "loading routes", err)
		return
	}

	writeJSON(w, http.StatusOK, compiledGraphResponse{
		TransitGraph: job.Result,
		Routes:       routes,
	})
}

// compiledGraphResponse is a compiled graph as returned to a client: the
// TransitGraph inlined (services, nodes, merge), plus the routes its services
// run on so the caller can draw each along its alignment rather than as
// straight chords between stops.
//
// One type serves both the scenario and single-service reads so the two
// responses cannot drift apart — a client's graph-to-map code works against
// either without knowing which it asked for.
type compiledGraphResponse struct {
	*transit.TransitGraph
	Routes []transit.Route `json:"routes"`
}

// routeStore is the alignment lookup a graph read bundles from — the only part
// of the repository the shared graph body touches itself, the rest coming
// through the target adapter.
type routeStore interface {
	ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error)
}

// serviceRouteIDs is the distinct set of routes a group of services runs on, in
// first-seen order. A service with no route contributes nothing, so the result
// can be shorter than the input — or empty.
func serviceRouteIDs(services []transit.UserService) []string {
	seen := make(map[string]bool, len(services))
	ids := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.RouteID == "" || seen[svc.RouteID] {
			continue
		}
		seen[svc.RouteID] = true
		ids = append(ids, svc.RouteID)
	}
	return ids
}

// routesByIDs loads routes by id, normalising nil to an empty slice so the JSON
// always carries a routes array rather than null.
func routesByIDs(ctx context.Context, store routeStore, ids []string) ([]transit.Route, error) {
	routes, err := store.ListRoutesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if routes == nil {
		routes = []transit.Route{}
	}
	return routes, nil
}

// createCompileJob mints an id, fills in the queued status, and persists the
// job, writing a 500 and reporting ok=false on any failure. The caller supplies
// the kind, target FK, and owner; everything else is uniform across the compile
// triggers.
func createCompileJob(w http.ResponseWriter, r *http.Request, store CompileStore, job transit.Job) (transit.Job, bool) {
	id, err := ids.NewUUID()
	if err != nil {
		writeInternalError(r.Context(), w, "generating job id", err)
		return transit.Job{}, false
	}
	job.ID = id
	job.Status = transit.JobStatusQueued

	if err := store.CreateJob(r.Context(), job); err != nil {
		writeInternalError(r.Context(), w, "creating job", err)
		return transit.Job{}, false
	}
	return job, true
}
