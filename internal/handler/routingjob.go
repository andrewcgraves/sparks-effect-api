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

// OriginOutOfRangeErrorCode is the machine-readable code for an isochrone whose
// origin is too far from every station in the graph for the requested mode and
// budget to reach any of them. The client's move is to move the pin or raise the
// budget, not to retry, which is why it is a code rather than a bare 422
// (SPA-200).
const OriginOutOfRangeErrorCode = "origin_out_of_range"

// originOutOfRangeDetail is the payload accompanying that code: how far the
// nearest station actually was, and how far the request could have reached.
//
// Both are sent because neither alone lets a client say anything useful. "Too
// far" is what the code already says; "the nearest station is 40 km away and
// this budget covers 5 km" is what a person can act on. The slug is there so a
// client that holds the graph can name or highlight that station.
type originOutOfRangeDetail struct {
	NearestStationSlug string  `json:"nearest_station_slug"`
	NearestStationKm   float64 `json:"nearest_station_km"`
	MaxReachKm         float64 `json:"max_reach_km"`
}

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
	if !originInRange(w, job, graph) {
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

// originInRange refuses an isochrone whose origin no station could be reached
// from, writing the 422 and reporting false when it does (SPA-200).
//
// It runs first, before the id is minted and before anything is written or
// published, because refusing after any of that would defeat the point: the
// costs this exists to avoid are the routing job row, a queue message carrying
// the whole compiled graph inline, and a worker slot the routing server plots
// one chain at a time from. A far-away origin already produces a lone origin
// polygon and an empty station list, so nothing that used to be a plot becomes
// an error here — what used to be a blank map becomes an explanation.
//
// It sits in this shared tail rather than in the three handlers so the seeded
// isochrone and both authored ones are covered by one check. They differ in how
// they resolve a graph and who may ask for one; by here they have all resolved
// to the same three things this needs — a point, a budget and a mode — and the
// graph that answers the question.
//
// A graph the check cannot read is enqueued unchanged. See
// transit.CheckOriginReach for why that is the only safe reading of "I cannot
// see any stations".
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
