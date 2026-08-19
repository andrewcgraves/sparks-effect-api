package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// BacklogFullErrorCode is the machine-readable code for an isochrone refused
// because too much routing work is already in flight (SPA-219).
//
// It is a code rather than a bare 429 because a client's move here is unlike
// its move on any other refusal: nothing about the request is wrong, so there
// is nothing to correct and no point re-checking the origin or the budget —
// the only useful response is to wait and send the same request again. See
// Retry-After for how long.
const BacklogFullErrorCode = "backlog_full"

// backlogFullMessage is what a caller sees when the cap trips. Written for the
// end user, since this is the text that reaches the isochrone form's error
// banner unchanged — the same reasoning as staleRoutingJobMessage.
const backlogFullMessage = "The isochrone service is busy right now. Please try again in a few moments."

// backlogRetryAfter is the Retry-After sent with the refusal: long enough that
// an honest client retrying immediately does not simply collect a second 429,
// short enough that a person who wanted an isochrone is not left waiting well
// past the point the backlog cleared.
const backlogRetryAfter = 15 * time.Second

// RoutingBacklogStore is the one read the enqueue cap needs: how much routing
// work is already outstanding.
//
// Deliberately not folded into RoutingStore. This is the only caller, and the
// three isochrone handlers that do the recording have no business being able
// to ask.
type RoutingBacklogStore interface {
	CountInFlightRoutingJobs(ctx context.Context, within time.Duration) (int, error)
}

// CapIsochroneBacklog refuses isochrone requests with 429 once `limit` routing
// jobs are already queued or running. A limit of zero or less disables it.
//
// It is middleware, wrapping the enqueue endpoints rather than living in the
// shared tail with originInRange, because being refused early is the whole
// point: the request is turned away before its body is read, before a scenario
// and its compiled graph are fetched, and before an id is minted. On the public
// endpoint that leaves one indexed count and nothing else; on the two authored
// ones it sits inside the auth gate, so a session lookup precedes it and an
// anonymous flood never reaches it at all. Either way a refused request costs a
// fraction of an accepted one, which is what makes a flood cheap to serve
// instead of an ever-growing queue that delays everybody's isochrones behind it
// (SPA-198 finding (a)).
//
// The signal is the count of unfinished routing_jobs rather than the broker's
// queue depth. Both describe the same backlog, but the rows are the record the
// API already owns and already writes on this path, so reading them needs no
// management API, no second credential, and no reconnect logic for a broker
// that is already the thing under strain. It also counts a job the worker has
// picked up but not finished, which queue depth does not.
//
// The limit is a ceiling on the whole deployment, not per caller: the queue is
// shared, so one caller's backlog is everyone's wait. That also means an
// anonymous flood filling the backlog refuses authored isochrones too, which is
// accepted deliberately — bounding total work is what SPA-219 asks for, and
// per-caller fairness needs a per-caller key (SPA-198 recommendation 2), which
// this has none of.
//
// Recovery needs nothing: the count falls as the worker finishes jobs, and the
// age bound in CountInFlightRoutingJobs means even a dead worker's backlog
// stops counting rather than wedging the endpoint shut. See that method for why
// that bound is load-bearing.
//
// A count that cannot be read admits the request. A database too unwell to
// answer a count is not a reason to refuse work the rest of the path may still
// manage, and this is a protective cap rather than a correctness gate — failing
// it open costs an unbounded queue in an outage that already has bigger
// problems, while failing it closed takes the isochrone down over a read
// nothing else on the path needed.
func CapIsochroneBacklog(store RoutingBacklogStore, limit int, log *slog.Logger) func(http.Handler) http.Handler {
	if limit <= 0 {
		log.Warn("isochrone enqueue cap disabled; routing backlog is unbounded",
			"max_inflight_isochrones", limit)
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inFlight, err := store.CountInFlightRoutingJobs(r.Context(), RoutingJobStaleAfter)
			if err != nil {
				log.ErrorContext(r.Context(), "routing: could not count in-flight jobs; admitting request",
					"error", err)
				next.ServeHTTP(w, r)
				return
			}
			if inFlight < limit {
				next.ServeHTTP(w, r)
				return
			}

			log.WarnContext(r.Context(), "routing: isochrone refused, backlog full",
				"in_flight", inFlight, "limit", limit)
			w.Header().Set("Retry-After", strconv.Itoa(int(backlogRetryAfter.Seconds())))
			writeErrorCode(w, http.StatusTooManyRequests, BacklogFullErrorCode, backlogFullMessage)
		})
	}
}
