package compile

import (
	"context"
	"log/slog"
	"sync"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type Runner struct {
	store        Store
	boardingWait transit.BoardingWaitPolicy

	wg       sync.WaitGroup
	mu       sync.Mutex
	inFlight map[string]struct{}
}

func NewRunner(store Store, boardingWait transit.BoardingWaitPolicy) *Runner {
	return &Runner{store: store, boardingWait: boardingWait, inFlight: map[string]struct{}{}}
}

func (r *Runner) Enqueue(job transit.Job) {
	r.mu.Lock()
	r.inFlight[job.ID] = struct{}{}
	r.mu.Unlock()
	r.wg.Add(1)

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
		if err := Compile(context.Background(), r.store, job, r.boardingWait); err != nil {
			slog.Error("compile: job failed", "job_id", job.ID, "error", err)
		}
	}()
}

// Drain returns the ids of the compiles still running when ctx ends. Their
// rows stay queued or running; the next boot's sweep fails them.
func (r *Runner) Drain(ctx context.Context) []string {
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

	r.mu.Lock()
	defer r.mu.Unlock()
	abandoned := make([]string, 0, len(r.inFlight))
	for id := range r.inFlight {
		abandoned = append(abandoned, id)
	}
	return abandoned
}
