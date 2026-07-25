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
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		svc, found, err := store.GetUserServiceBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			writeInternalError(w, "looking up service", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}

		job, ok := createCompileJob(w, r, store, transit.Job{
			Kind:          transit.JobKindCompileUserService,
			UserServiceID: &svc.ID,
			OwnerID:       &user.ID,
		})
		if !ok {
			return
		}
		enqueueCompile(store, job)
		writeJSON(w, http.StatusAccepted, job)
	}
}

// CompileUserScenario returns a handler for POST /api/user-scenarios/{slug}/compile.
//
// The scenario twin of CompileUserService: it compiles the caller's curated set
// of member services as one network. Owner-scoped identically — a scenario the
// caller does not own answers 404.
func CompileUserScenario(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		sc, found, err := store.GetUserScenarioBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			writeInternalError(w, "looking up scenario", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}

		job, ok := createCompileJob(w, r, store, transit.Job{
			Kind:           transit.JobKindCompileUserScenario,
			UserScenarioID: &sc.ID,
			OwnerID:        &user.ID,
		})
		if !ok {
			return
		}
		enqueueCompile(store, job)
		writeJSON(w, http.StatusAccepted, job)
	}
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
		sc, found, err := store.GetUserScenarioBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			writeInternalError(w, "looking up scenario", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		if !authorizeScenario(w, r, sc) {
			return
		}

		job, found, err := store.GetLatestSucceededUserScenarioJob(r.Context(), sc.Slug)
		if err != nil {
			writeInternalError(w, "looking up compiled graph", err)
			return
		}
		if !found || job.Result == nil {
			writeError(w, http.StatusNotFound, "no compiled graph for this scenario yet")
			return
		}

		// The compiled graph is pure topology — its edges carry no geometry —
		// so a client that wants to draw the scenario needs the member
		// services' routes too. Load them at read time and bundle them
		// alongside the graph, mirroring what the public /api/scenarios/{slug}
		// read does; the persisted job result is left untouched.
		routes, err := memberRoutes(r.Context(), store, sc.ServiceIDs)
		if err != nil {
			writeInternalError(w, "loading member routes", err)
			return
		}

		writeJSON(w, http.StatusOK, userScenarioGraphResponse{
			TransitGraph: job.Result,
			Routes:       routes,
		})
	}
}

// userScenarioGraphResponse is the compiled graph as returned to a client: the
// TransitGraph inlined (services, nodes, merge), plus the member services'
// routes so the caller can draw each service along its alignment rather than
// as straight chords between stops.
type userScenarioGraphResponse struct {
	*transit.TransitGraph
	Routes []transit.Route `json:"routes"`
}

// memberRoutes loads the distinct routes the scenario's member services run on.
// Returns an empty slice (never nil) so the JSON always carries a routes array.
func memberRoutes(ctx context.Context, store CompileStore, serviceIDs []string) ([]transit.Route, error) {
	services, err := store.ListUserServicesByIDs(ctx, serviceIDs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(services))
	routeIDs := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.RouteID == "" || seen[svc.RouteID] {
			continue
		}
		seen[svc.RouteID] = true
		routeIDs = append(routeIDs, svc.RouteID)
	}
	routes, err := store.ListRoutesByIDs(ctx, routeIDs)
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
		writeInternalError(w, "generating job id", err)
		return transit.Job{}, false
	}
	job.ID = id
	job.Status = transit.JobStatusQueued

	if err := store.CreateJob(r.Context(), job); err != nil {
		writeInternalError(w, "creating job", err)
		return transit.Job{}, false
	}
	return job, true
}
