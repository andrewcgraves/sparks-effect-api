package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/andrewcgraves/sparks-effect-api/internal/worker"
)

// CompileStore is the slice of the repository the async compile job surface
// needs: resolving a compile target by slug (seeded scenario, user scenario, or
// user service), persisting the job, and — via worker.Store — the compile
// itself.
type CompileStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	CreateJob(ctx context.Context, j transit.Job) error
	GetJobByID(ctx context.Context, id string) (transit.Job, bool, error)
	// GetLatestSucceededJob backs ScenarioGraph: the seeded scenario's result,
	// retrievable by slug without the caller ever needing a job id.
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error)
	// GetLatestSucceededUserScenarioJob is its user-authored counterpart,
	// backing UserScenarioGraph.
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (transit.Job, bool, error)
	// GetLatestSucceededUserServiceJob backs UserServiceGraph, for a service
	// compiled on its own rather than as a scenario member.
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (transit.Job, bool, error)
	worker.Store
}

// CompileScenario returns a handler for POST /api/scenarios/{slug}/compile. It
// persists a queued job and hands the compile off to a background goroutine,
// so the caller gets a job id back immediately rather than waiting for the
// physics compile and graph build to finish.
func CompileScenario(store CompileStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		slug := r.PathValue("slug")
		sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up scenario", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}

		job, ok := createCompileJob(w, r, store, transit.Job{
			Kind:       transit.JobKindCompileScenario,
			ScenarioID: &sc.ID,
			OwnerID:    &user.ID,
		})
		if !ok {
			return
		}
		enqueueCompile(store, job, boardingWait)
		writeJSON(w, http.StatusAccepted, job)
	}
}

// enqueueCompile hands a persisted, queued job off to a background goroutine so
// the triggering caller gets its job id back immediately rather than waiting
// for the physics compile and graph build to finish.
//
// The goroutine is detached from the request context: the compile must run to
// completion regardless of whether the triggering client is still connected by
// the time it finishes. Shared by every compile trigger — seeded scenario, user
// scenario, and single user service — since the queued → running →
// succeeded/failed surface is identical across them; only the job's kind and
// target differ, and worker.Compile switches on that.
func enqueueCompile(store CompileStore, job transit.Job, boardingWait transit.BoardingWaitPolicy) {
	go func() {
		if err := worker.Compile(context.Background(), store, job, boardingWait); err != nil {
			slog.Error("worker: compile job failed", "job_id", job.ID, "error", err)
		}
	}()
}

// JobStatus returns a handler for GET /api/jobs/{id}: the queued -> running ->
// succeeded/failed poll. A job belonging to someone else answers the same 404
// as an unknown id, so a caller learns nothing about which job ids exist;
// admins may view any job.
func JobStatus(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id := r.PathValue("id")
		job, found, err := store.GetJobByID(r.Context(), id)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up job", err)
			return
		}
		if !found || (!user.IsAdmin && (job.OwnerID == nil || *job.OwnerID != user.ID)) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}

		writeJSON(w, http.StatusOK, job)
	}
}

// ScenarioGraph returns a handler for GET /api/scenarios/{slug}/graph: a
// compile job's result, addressed by the scenario's slug rather than a job
// id — the read path once a caller already knows compilation finished, with
// no job id to carry around. It is public, like the other scenario reads.
func ScenarioGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, ok := latestSeededCompile(w, r, store, r.PathValue("slug"))
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, job.Result)
	}
}

// SeededGraphReader resolves a seeded scenario's compiled graph by slug. It is
// the one read both public surfaces of that graph share: the graph itself, and
// the isochrone plotted over it.
type SeededGraphReader interface {
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error)
}

// latestSeededCompile reads a seeded scenario's latest succeeded compile,
// writing the response and reporting ok=false when there is none. The whole job
// is returned, not just its Result: the graph read wants the bytes, but the
// isochrone wants the job id, since that is a compiled graph's identity and
// what a routing job names (see loadSeededCompile).
//
// A scenario that has never compiled and one whose compile stored no result are
// the same "not compiled yet" to a caller — as on the authored side (see
// loadCompiledGraph), which answers the same way for the same reason.
func latestSeededCompile(w http.ResponseWriter, r *http.Request, store SeededGraphReader, slug string) (transit.Job, bool) {
	job, found, err := store.GetLatestSucceededJob(r.Context(), slug, transit.JobKindCompileScenario)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up compiled graph", err)
		return transit.Job{}, false
	}
	if !found || job.Result == nil {
		writeError(w, http.StatusNotFound, "no compiled graph for this scenario yet")
		return transit.Job{}, false
	}
	return job, true
}
