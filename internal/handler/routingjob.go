package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const PublishFailedErrorCode = "publish_failed"

const OriginOutOfRangeErrorCode = "origin_out_of_range"

type originOutOfRangeDetail struct {
	NearestStationSlug string  `json:"nearest_station_slug"`
	NearestStationKm   float64 `json:"nearest_station_km"`
	MaxReachKm         float64 `json:"max_reach_km"`
}

type RoutingStore interface {
	CreateRoutingJob(ctx context.Context, j *transit.RoutingJob) error
	GetRoutingJobByID(ctx context.Context, id string) (transit.RoutingJob, bool, error)
	FailRoutingJob(ctx context.Context, id, errMsg string) error
	FindReusableRoutingJob(ctx context.Context, want transit.RoutingJob) (transit.RoutingJob, bool, error)
}

type RoutingJobPollStore interface {
	RoutingStore
	CountInFlightRoutingJobsBefore(ctx context.Context, createdAt time.Time, within time.Duration) (int, error)
}

func enqueueIsochrone(w http.ResponseWriter, r *http.Request, store RoutingStore,
	publisher routing.Publisher, job transit.RoutingJob, graph *transit.TransitGraph) {
	if !originInRange(w, job, graph) {
		return
	}

	// A repeat is answered with the job that already answered it, so it costs
	// no queue slot (SPA-331). The key and freshness rules are migration
	// 00034's. 200 rather than 202: nothing was accepted for processing, and
	// the job is already succeeded, so a caller that polls it anyway gets the
	// same answer at once. A failed lookup only loses the shortcut.
	if prior, ok, err := store.FindReusableRoutingJob(r.Context(), job); err != nil {
		slog.ErrorContext(r.Context(), "routing: reuse lookup failed; enqueueing", "error", err)
	} else if ok {
		slog.Debug("routing job reused", "routing_job_id", prior.ID)
		writeJSON(w, http.StatusOK, prior)
		return
	}

	id, err := ids.NewUUID()
	if err != nil {
		writeInternalError(r.Context(), w, "generating routing job id", err)
		return
	}
	job.ID = id
	job.Status = transit.JobStatusQueued

	if err := store.CreateRoutingJob(r.Context(), &job); err != nil {
		writeInternalError(r.Context(), w, "creating routing job", err)
		return
	}

	// The trace id is whatever traceid.Middleware attached to this request —
	// caller-supplied or minted for it — so the worker can log this job's
	// computation under the same trace as the request that created it.
	trace, _ := traceid.FromContext(r.Context())
	if err := publisher.Publish(r.Context(), routing.MessageFor(job, graph, trace)); err != nil {
		failUnpublishedJob(store, job.ID, err)
		writeErrorCode(w, http.StatusBadGateway, PublishFailedErrorCode,
			"could not enqueue the isochrone; the routing job was marked failed")
		return
	}

	slog.Debug("routing job enqueued", "routing_job_id", job.ID, "trace_id", trace)
	writeJSON(w, http.StatusAccepted, job)
}

func originInRange(w http.ResponseWriter, job transit.RoutingJob, graph *transit.TransitGraph) bool {
	reach, ok := transit.CheckOriginReach(graph, job.Lat, job.Lng, job.Mode, job.BudgetMins)
	if !ok || reach.InRange {
		return true
	}

	slog.Debug("isochrone origin out of range",
		"lat", job.Lat, "lng", job.Lng, "mode", job.Mode, "budget_mins", job.BudgetMins,
		"nearest_station_slug", reach.NearestSlug,
		"nearest_station_km", reach.NearestKm,
		"max_reach_km", reach.MaxReachKm)

	writeErrorDetail(w, http.StatusUnprocessableEntity, OriginOutOfRangeErrorCode,
		"the origin is too far from any station to reach one within this travel time",
		originOutOfRangeDetail{
			NearestStationSlug: reach.NearestSlug,
			NearestStationKm:   reach.NearestKm,
			MaxReachKm:         reach.MaxReachKm,
		})
	return false
}

func failUnpublishedJob(store RoutingStore, id string, cause error) {
	slog.Error("routing job was not published", "routing_job_id", id, "error", cause)

	ctx, cancel := context.WithTimeout(context.Background(), failJobTimeout)
	defer cancel()
	if err := store.FailRoutingJob(ctx, id,
		"the isochrone was never enqueued: "+cause.Error()); err != nil {
		slog.Error("could not mark routing job failed", "routing_job_id", id, "error", err)
	}
}

const failJobTimeout = 5 * time.Second

func RoutingJobStatus(store RoutingJobPollStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, found, err := store.GetRoutingJobByID(r.Context(), r.PathValue("id"))
		if err != nil {
			writeInternalError(r.Context(), w, "looking up routing job", err)
			return
		}
		if !found || !mayReadRoutingJob(r, job) {
			writeError(w, http.StatusNotFound, "routing job not found")
			return
		}

		job = failIfStale(r.Context(), store, job)

		writeJSON(w, http.StatusOK, routingJobPoll{
			RoutingJob:    job,
			QueuePosition: queuePosition(r.Context(), store, job),
		})
	}
}

type routingJobPoll struct {
	transit.RoutingJob
	QueuePosition *int `json:"queue_position,omitempty"`
}

// The worker takes one job at a time, oldest first, so a queued job waits
// behind every in-flight job created before it (SPA-437). A failed count only
// loses the courtesy, not the poll.
func queuePosition(ctx context.Context, store RoutingJobPollStore, job transit.RoutingJob) *int {
	if job.Status != transit.JobStatusQueued {
		return nil
	}
	ahead, err := store.CountInFlightRoutingJobsBefore(ctx, job.CreatedAt, RoutingJobStaleAfter)
	if err != nil {
		slog.ErrorContext(ctx, "routing: could not count in-flight jobs before this one; omitting queue position",
			"routing_job_id", job.ID, "error", err)
		return nil
	}
	return &ahead
}

const RoutingJobStaleAfter = 90 * time.Second

const staleRoutingJobMessage = "The isochrone service isn't responding right now. Please try again in a few minutes."

func failIfStale(ctx context.Context, store RoutingStore, job transit.RoutingJob) transit.RoutingJob {
	if job.Status != transit.JobStatusQueued && job.Status != transit.JobStatusRunning {
		return job
	}
	if time.Since(job.CreatedAt) <= RoutingJobStaleAfter {
		return job
	}

	if err := store.FailRoutingJob(ctx, job.ID, staleRoutingJobMessage); err != nil {
		slog.ErrorContext(ctx, "routing: could not mark stale routing job failed",
			"routing_job_id", job.ID, "error", err)
		return job
	}

	slog.WarnContext(ctx, "routing: job stale; marked failed",
		"routing_job_id", job.ID, "age", time.Since(job.CreatedAt))
	job.Status = transit.JobStatusFailed
	job.Error = staleRoutingJobMessage
	return job
}

func mayReadRoutingJob(r *http.Request, job transit.RoutingJob) bool {
	if job.OwnerID == nil {
		return true
	}
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		return false
	}
	return user.IsAdmin || *job.OwnerID == user.ID
}
