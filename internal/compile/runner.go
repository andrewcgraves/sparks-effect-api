package compile

import (
	"context"
	"log/slog"
	"sync"

	"github.com/andrewcgraves/sparks-effect-api/internal/metrics"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type Runner struct {
	store        Store
	boardingWait transit.BoardingWaitPolicy
	metrics      *metrics.Metrics

	wg       sync.WaitGroup
	mu       sync.Mutex
	draining bool
	inFlight map[string]struct{}
}

func NewRunner(store Store, boardingWait transit.BoardingWaitPolicy, m *metrics.Metrics) *Runner {
	return &Runner{store: store, boardingWait: boardingWait, metrics: m, inFlight: map[string]struct{}{}}
}

func (r *Runner) Enqueue(job transit.Job) {
	// Admission and wg.Add share Drain's lock, so no compile can join the
	// WaitGroup once Drain is waiting on it. A refused job keeps its queued
	// row, which the next boot's sweep fails.
	r.mu.Lock()
	if r.draining {
		r.mu.Unlock()
		slog.Warn("compile: refused while shutting down; the next boot fails it", "job_id", job.ID)
		return
	}
	r.inFlight[job.ID] = struct{}{}
	r.wg.Add(1)
	r.mu.Unlock()

	// Detached from the request that enqueued it: the handler answers 202 at
	// once, and the compile has to outlive that response. Shutdown reaches it
	// through Drain instead.
	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.inFlight, job.ID)
			r.mu.Unlock()
			r.wg.Done()
		}()
		if err := Compile(context.Background(), r.store, job, r.boardingWait, r.metrics); err != nil {
			slog.Error("compile: job failed", "job_id", job.ID, "error", err)
		}
	}()
}

func (r *Runner) Drain(ctx context.Context) []string {
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	// What is still in flight at the deadline is abandoned: its row stays
	// queued or running, and the next boot's sweep fails it.
	r.mu.Lock()
	defer r.mu.Unlock()
	abandoned := make([]string, 0, len(r.inFlight))
	for id := range r.inFlight {
		abandoned = append(abandoned, id)
	}
	return abandoned
}
