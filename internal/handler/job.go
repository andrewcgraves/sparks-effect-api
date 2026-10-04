package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type CompileStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error)
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	CreateJob(ctx context.Context, j transit.Job) error
	GetJobByID(ctx context.Context, id string) (transit.Job, bool, error)
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error)
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (transit.Job, bool, error)
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (transit.Job, bool, error)
	FailInterruptedJob(ctx context.Context, id, errMsg string) (bool, error)
	compile.Store
}

func CompileScenario(store CompileStore, compiles *compile.Runner) http.HandlerFunc {
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
		// An owned scenario is compilable, but only by the person who authored
		// it. A stranger is told it does not exist rather than that they may
		// not touch it, matching every other owner-scoped read.
		if !found || !mayReachScenario(r.Context(), sc) {
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
		compiles.Enqueue(job)
		writeJSON(w, http.StatusAccepted, job)
	}
}

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

		job = failIfCompileInterrupted(r.Context(), store, job)

		writeJSON(w, http.StatusOK, job)
	}
}

const CompileJobInterruptedAfter = 5 * time.Minute

const interruptedCompileJobMessage = "the compile was interrupted before it finished; compile again to retry"

func failIfCompileInterrupted(ctx context.Context, store CompileStore, job transit.Job) transit.Job {
	if job.Status != transit.JobStatusQueued && job.Status != transit.JobStatusRunning {
		return job
	}
	// A compile runs in-process and takes seconds, so one unfinished this long
	// lost its goroutine to a process the boot sweep has not reached: another
	// replica, or one that died with no restart (SPA-431).
	if time.Since(job.CreatedAt) <= CompileJobInterruptedAfter {
		return job
	}

	failed, err := store.FailInterruptedJob(ctx, job.ID, interruptedCompileJobMessage)
	if err != nil {
		slog.ErrorContext(ctx, "compile: could not mark interrupted compile job failed",
			"job_id", job.ID, "error", err)
		return job
	}
	if !failed {
		// It finished between the read above and the write. Answer with what
		// it finished as, not the unfinished row this request first saw.
		if current, found, err := store.GetJobByID(ctx, job.ID); err == nil && found {
			return current
		}
		return job
	}

	slog.WarnContext(ctx, "compile: job interrupted; marked failed",
		"job_id", job.ID, "age", time.Since(job.CreatedAt))
	job.Status = transit.JobStatusFailed
	job.Error = interruptedCompileJobMessage
	return job
}

func ScenarioGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		// Resolved first, purely to apply the ownership gate: the compiled
		// graph of an owned scenario is as private as the scenario is, and
		// without this the job read would hand it to anyone with the slug.
		// A curated scenario passes without an identity, which is what keeps
		// this endpoint public for the seeded data it was built for.
		sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
		if err != nil {
			writeInternalError(r.Context(), w, "looking up scenario", err)
			return
		}
		if !found || !mayReachScenario(r.Context(), sc) {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}

		job, ok := latestSeededCompile(w, r, store, slug)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, job.Result)
	}
}

type SeededGraphReader interface {
	GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error)
}

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
