package handler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
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
	// The first enqueue since lastContact; zero when there is none. A live
	// worker marks a job running within seconds of its enqueue, and that
	// write-back clears this.
	enqueuedSinceContact time.Time
}

func NewWorkerWatch(now func() time.Time) *WorkerWatch {
	return &WorkerWatch{now: now, lastContact: now()}
}

func (ww *WorkerWatch) RecordContact(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww.mu.Lock()
		ww.lastContact = ww.now()
		ww.enqueuedSinceContact = time.Time{}
		ww.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (ww *WorkerWatch) Publisher(next routing.Publisher) routing.Publisher {
	return watchedPublisher{next: next, watch: ww}
}

type watchedPublisher struct {
	next  routing.Publisher
	watch *WorkerWatch
}

func (p watchedPublisher) Publish(ctx context.Context, msg routing.Message) error {
	at := p.watch.now()
	if err := p.next.Publish(ctx, msg); err != nil {
		return err
	}
	p.watch.recordEnqueue(at)
	return nil
}

func (ww *WorkerWatch) recordEnqueue(at time.Time) {
	ww.mu.Lock()
	defer ww.mu.Unlock()
	// A fast worker can mark the job running before Publish returns; contact
	// since the enqueue began has already answered it.
	if ww.lastContact.After(at) || !ww.enqueuedSinceContact.IsZero() {
		return
	}
	ww.enqueuedSinceContact = at
}

func (ww *WorkerWatch) silence(now time.Time) (silent, enqueueWait time.Duration) {
	ww.mu.Lock()
	defer ww.mu.Unlock()
	if !ww.enqueuedSinceContact.IsZero() {
		enqueueWait = now.Sub(ww.enqueuedSinceContact)
	}
	return now.Sub(ww.lastContact), enqueueWait
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
		// The queue is the rule SPA-442 states. The enqueue the worker never
		// answered is what keeps a polled job waiting after failIfStale has
		// failed it at 90 s, which is before two minutes of silence can pass.
		silent, enqueueWait := watch.silence(now)
		queueWaiting := waited > queuedTooLongAfter
		waiting := queueWaiting || enqueueWait > queuedTooLongAfter
		status := RoutingStatusOK
		switch {
		case waiting && silent >= workerSilentAfter:
			status = RoutingStatusOffline
		case queueWaiting:
			status = RoutingStatusDegraded
		}
		w.Header().Set("Cache-Control", "max-age=10")
		writeJSON(w, http.StatusOK, routingStatusResponse{
			Status: status, OldestQueuedSecs: int(waited.Seconds()), InFlight: q.InFlight,
		})
	}
}
