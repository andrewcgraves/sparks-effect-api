package handler_test

import (
	"context"
	"sync"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeRoutingStore is an in-memory handler.RoutingStore. Every isochrone
// endpoint now records a routing job, so it is embedded into the seeded,
// service, and scenario fakes rather than written out three times.
//
// It is a struct with a zero value that works, so embedding it costs the
// enclosing fake nothing: `fakeRoutingStore` on its own is a usable empty
// store, and only tests that care about what was recorded reach into it.
type fakeRoutingStore struct {
	mu sync.Mutex

	// routingJobs holds what was created, keyed by id.
	routingJobs map[string]transit.RoutingJob
	// createErr and failErr make the write paths fail on demand.
	createErr error
	failErr   error
	// lookupErr fails the poll read specifically, so a read failure can be
	// tested without also breaking the writes.
	lookupErr error
}

func (f *fakeRoutingStore) CreateRoutingJob(_ context.Context, j *transit.RoutingJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if f.routingJobs == nil {
		f.routingJobs = map[string]transit.RoutingJob{}
	}
	// The real store fills these in from the database, and the 202 body carries
	// them, so a fake that left them zero would hide a handler that never
	// persisted the row at all.
	j.CreatedAt = fixedNow
	j.UpdatedAt = fixedNow
	f.routingJobs[j.ID] = *j
	return nil
}

func (f *fakeRoutingStore) GetRoutingJobByID(_ context.Context, id string) (transit.RoutingJob, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookupErr != nil {
		return transit.RoutingJob{}, false, f.lookupErr
	}
	j, ok := f.routingJobs[id]
	return j, ok, nil
}

func (f *fakeRoutingStore) FailRoutingJob(_ context.Context, id, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	j, ok := f.routingJobs[id]
	if !ok {
		return nil
	}
	j.Status = transit.JobStatusFailed
	j.Error = errMsg
	f.routingJobs[id] = j
	return nil
}

// only returns the single routing job recorded, failing the test's expectation
// of exactly one if there is any other number.
func (f *fakeRoutingStore) only() (transit.RoutingJob, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.routingJobs) != 1 {
		return transit.RoutingJob{}, false
	}
	for _, j := range f.routingJobs {
		return j, true
	}
	return transit.RoutingJob{}, false
}

func (f *fakeRoutingStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.routingJobs)
}

// put seeds a routing job directly, for the poll tests that need a row without
// going through an isochrone request to make one.
func (f *fakeRoutingStore) put(j transit.RoutingJob) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.routingJobs == nil {
		f.routingJobs = map[string]transit.RoutingJob{}
	}
	f.routingJobs[j.ID] = j
}
