package handler

import (
	"context"
	"log"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/andrewcgraves/sparks-effect-api/internal/worker"
)

// jobKindCompile is the only Job.Kind this ticket produces. "compute" jobs
// (the isochrone side of the async model) are a later addition.
const jobKindCompile = "compile"

// CompileStore is the slice of the repository the async compile job surface
// needs: resolving a scenario by slug, persisting the job, and — via
// worker.Store — the compile itself.
type CompileStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	CreateJob(ctx context.Context, j transit.Job) error
	GetJobByID(ctx context.Context, id string) (transit.Job, bool, error)
	// GetLatestSucceededJob backs ScenarioGraph: the result, retrievable by
	// scenario slug, without the caller ever needing a job id.
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error)
	worker.Store
}

// CompileScenario returns a handler for POST /api/scenarios/{slug}/compile. It
// persists a queued job and hands the compile off to a background goroutine,
// so the caller gets a job id back immediately rather than waiting for the
// physics compile and graph build to finish.
func CompileScenario(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		slug := r.PathValue("slug")
		sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
		if err != nil {
			log.Printf("handler: looking up scenario failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			log.Printf("handler: generating job id failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		job := transit.Job{
			ID:         id,
			Kind:       jobKindCompile,
			Status:     transit.JobStatusQueued,
			ScenarioID: &sc.ID,
			OwnerID:    &user.ID,
		}
		if err := store.CreateJob(r.Context(), job); err != nil {
			log.Printf("handler: creating job failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// Detached from the request context: the compile must run to
		// completion regardless of whether the triggering client is still
		// connected by the time it finishes.
		go func() {
			if err := worker.Compile(context.Background(), store, job.ID, sc.ID); err != nil {
				log.Printf("worker: compile job %s: %v", job.ID, err)
			}
		}()

		writeJSON(w, http.StatusAccepted, job)
	}
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
			log.Printf("handler: looking up job failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
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
		slug := r.PathValue("slug")
		job, found, err := store.GetLatestSucceededJob(r.Context(), slug, jobKindCompile)
		if err != nil {
			log.Printf("handler: looking up compiled graph failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found || job.Result == nil {
			writeError(w, http.StatusNotFound, "no compiled graph for this scenario yet")
			return
		}
		writeJSON(w, http.StatusOK, job.Result)
	}
}
