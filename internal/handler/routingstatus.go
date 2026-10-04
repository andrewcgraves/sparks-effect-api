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

// The thresholds SPA-442 settled on. The worker heartbeats every 30 s, so two
// minutes is four missed beats; a job a minute old has waited half the
// browser's 120 s deadline.
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
	// The first job published since lastContact; zero when there is none. A
	// live worker marks a job running within seconds of its publish, and that
	// write-back clears this.
	unansweredSince time.Time
}

func NewWorkerWatch(now func() time.Time) *WorkerWatch {
	return &WorkerWatch{now: now, lastContact: now()}
}

func (ww *WorkerWatch) RecordContact(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww.mu.Lock()
		ww.lastContact = ww.now()
		ww.unansweredSince = time.Time{}
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
	if err := p.next.Publish(ctx, msg); err != nil {
		return err
	}
	p.watch.mu.Lock()
	if p.watch.unansweredSince.IsZero() {
		p.watch.unansweredSince = p.watch.now()
	}
	p.watch.mu.Unlock()
	return nil
}

func (ww *WorkerWatch) silence(now time.Time) (silent, unanswered time.Duration) {
	ww.mu.Lock()
	defer ww.mu.Unlock()
	if !ww.unansweredSince.IsZero() {
		unanswered = now.Sub(ww.unansweredSince)
	}
	return now.Sub(ww.lastContact), unanswered
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
		// The queue is the rule SPA-442 states. The unanswered publish is what
		// keeps a polled job counting after failIfStale has marked it failed
		// at 90 s, which is before two minutes of silence can elapse.
		silent, unanswered := watch.silence(now)
		stuck := waited > queuedTooLongAfter || unanswered > queuedTooLongAfter
		status := RoutingStatusOK
		switch {
		case stuck && silent >= workerSilentAfter:
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
