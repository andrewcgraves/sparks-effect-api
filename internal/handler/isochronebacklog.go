package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

const BacklogFullErrorCode = "backlog_full"

const backlogFullMessage = "The isochrone service is busy right now. Please try again in a few moments."

const backlogRetryAfter = 15 * time.Second

type RoutingBacklogStore interface {
	CountInFlightRoutingJobs(ctx context.Context, within time.Duration) (int, error)
}

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
