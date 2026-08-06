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

// PublishFailedErrorCode is the machine-readable code for an isochrone that was
// resolved and recorded but never made it onto the queue. It is distinct from a
// plain 502 because the client's next move is different: the routing job exists
// and is already marked failed, so retrying the request is the right response,
// while polling the job it never received an id for is not.
const PublishFailedErrorCode = "publish_failed"

// RoutingStore is the slice of the repository the routing job surface needs:
// recording an isochrone handed to the worker, polling it, and failing one the
// broker never confirmed.
type RoutingStore interface {
	CreateRoutingJob(ctx context.Context, j *transit.RoutingJob) error
	GetRoutingJobByID(ctx context.Context, id string) (transit.RoutingJob, bool, error)
	FailRoutingJob(ctx context.Context, id, errMsg string) error
}

// enqueueIsochrone is the shared tail of all three isochrone endpoints: record
// the job, publish it, answer 202.
//
// Everything specific to an endpoint — authentication, ownership, resolving the
// target slug, the stale-graph check — has already happened by the time this
// runs. What arrives here is one point, one budget, one mode, and one specific
// immutable compiled graph, which is exactly what the worker needs and nothing
// more. That is what keeps every piece of resolution logic on this side of the
// repository boundary.
//
// The row is written before the message is published, never after. A worker
// that received a message for a row that did not exist yet would have nothing
// to transition; the reverse ordering only risks a row whose publish failed,
// which is detectable and handled below.
func enqueueIsochrone(w http.ResponseWriter, r *http.Request, store RoutingStore,
	publisher routing.Publisher, job transit.RoutingJob, graph *transit.TransitGraph) {
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

// failUnpublishedJob marks a job whose message never reached the broker, so it
// does not sit in `queued` forever being polled by a client no worker will ever
// answer.
//
// It deliberately takes no request context, using one of its own instead: the
// request's is cancelled the moment the client disconnects, and a client that
// has given up waiting is precisely when leaving the row wrongly queued would
// go unnoticed. It is still bounded, so a wedged database cannot pin the
// request goroutine open indefinitely. A failure to record the failure is
// logged rather than returned — the caller is already being told the enqueue
// failed, which is the part it can act on.
func failUnpublishedJob(store RoutingStore, id string, cause error) {
	slog.Error("routing job was not published", "routing_job_id", id, "error", cause)

	ctx, cancel := context.WithTimeout(context.Background(), failJobTimeout)
	defer cancel()
	if err := store.FailRoutingJob(ctx, id,
		"the isochrone was never enqueued: "+cause.Error()); err != nil {
		slog.Error("could not mark routing job failed", "routing_job_id", id, "error", err)
	}
}

// failJobTimeout bounds the one write that outlives its request. Long enough
// that a busy database still records the failure, short enough that an
// unreachable one does not hold the handler open.
const failJobTimeout = 5 * time.Second

// RoutingJobStatus returns a handler for GET /api/routing-jobs/{id}: the
// queued -> running -> succeeded/failed poll for an isochrone, and the result
// once there is one.
//
// Readability follows the rule already in use for compile jobs, extended to
// cover the public seeded isochrone that has no owner to check against:
//
//   - A job with no owner is readable by anyone holding its id. It came from the
//     unauthenticated /api/isochrone, so there is no identity to match; the id
//     is a v4 UUID, which is unguessable, and the request it answers was public
//     to make in the first place.
//   - An owned job is readable only by its owner or an admin. Everyone else
//     gets the same 404 as an unknown id, so a caller cannot probe which job
//     ids exist.
//
// It sits behind auth.OptionalAuth rather than RequireAuth: an anonymous caller
// must still be able to poll the public isochrone they just requested.
func RoutingJobStatus(store RoutingStore) http.HandlerFunc {
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

		writeJSON(w, http.StatusOK, job)
	}
}

// mayReadRoutingJob applies the ownership rule above. Split out so the rule is
// one expression rather than a condition tangled into the 404.
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
