package handler

import (
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
		writeJSON(w, http.StatusOK, job.Result)
	}
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
