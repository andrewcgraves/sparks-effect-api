package handler

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const (
	RoutingStatusOK       = "ok"
	RoutingStatusDegraded = "degraded"
	RoutingStatusOffline  = "offline"
)

// The thresholds SPA-442 settled on. The worker heartbeats every 30 s, so a
// live one survives three missed beats and the fourth reads as gone; a job a
// minute old has waited half the browser's 120 s deadline.
const (
	workerSilentAfter  = 2 * time.Minute
	queuedTooLongAfter = 60 * time.Second
)

type RoutingQueue struct {
	InFlight       int
	OldestQueuedAt time.Time
}

type RoutingQueueStore interface {
	RoutingQueueSnapshot(ctx context.Context, within time.Duration) (RoutingQueue, error)
}

type WorkerWatch struct {
	now func() time.Time

	mu          sync.Mutex
	lastContact time.Time
}

func NewWorkerWatch(now func() time.Time) *WorkerWatch {
	return &WorkerWatch{now: now, lastContact: now()}
}

func (ww *WorkerWatch) RecordContact(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww.mu.Lock()
		ww.lastContact = ww.now()
		ww.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (ww *WorkerWatch) silence(now time.Time) time.Duration {
	ww.mu.Lock()
	defer ww.mu.Unlock()
	return now.Sub(ww.lastContact)
}

type routingStatusResponse struct {
	Status           string `json:"status"`
	OldestQueuedSecs int    `json:"oldest_queued_secs"`
	InFlight         int    `json:"inflight"`
}

func RoutingStatus(store RoutingQueueStore, watch *WorkerWatch) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := store.RoutingQueueSnapshot(r.Context(), RoutingJobStaleAfter)
		if err != nil {
			writeInternalError(r.Context(), w, "reading routing queue", err)
			return
		}
		now := watch.now()
		var waited time.Duration
		if !q.OldestQueuedAt.IsZero() {
			waited = now.Sub(q.OldestQueuedAt)
		}
		// The worker heartbeats every 30 s and never scales to zero, so silence
		// alone means it is down, whether or not a job waits (SPA-503).
		status := RoutingStatusOK
		switch {
		case watch.silence(now) >= workerSilentAfter:
			status = RoutingStatusOffline
		case waited > queuedTooLongAfter:
			status = RoutingStatusDegraded
		}
		w.Header().Set("Cache-Control", "max-age=10")
		writeJSON(w, http.StatusOK, routingStatusResponse{
			Status: status, OldestQueuedSecs: int(waited.Seconds()), InFlight: q.InFlight,
		})
	}
}
