package compile_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// gatedStore holds every compile at its first status write until release is
// closed, so a test can drain while a compile is provably mid-flight.
type gatedStore struct {
	*fakeStore
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
}

func newGatedStore() *gatedStore {
	return &gatedStore{fakeStore: fixtureStore(), started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (g *gatedStore) UpdateJobStatus(ctx context.Context, id, status, errMsg string) error {
	if status == transit.JobStatusRunning {
		g.started <- struct{}{}
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fakeStore.UpdateJobStatus(ctx, id, status, errMsg)
}

func (g *gatedStore) CompleteJob(ctx context.Context, id string, result transit.TransitGraph, compiledServiceIDs []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fakeStore.CompleteJob(ctx, id, result, compiledServiceIDs)
}

func TestRunnerDrainWaitsForAnInFlightCompile(t *testing.T) {
	store := newGatedStore()
	runner := compile.NewRunner(store, transit.DefaultBoardingWaitPolicy(), nil)

	runner.Enqueue(scenarioJob())
	<-store.started

	drained := make(chan []string, 1)
	go func() { drained <- runner.Drain(context.Background()) }()

	select {
	case <-drained:
		t.Fatal("Drain returned while a compile was still running")
	case <-time.After(20 * time.Millisecond):
	}

	close(store.release)
	if abandoned := <-drained; len(abandoned) != 0 {
		t.Errorf("Drain abandoned %v, want none once the compile finished", abandoned)
	}
	if store.completedWith == nil {
		t.Error("the compile Drain waited on never completed")
	}
}

func TestRunnerDrainReportsCompilesItAbandonsAtTheDeadline(t *testing.T) {
	store := newGatedStore()
	runner := compile.NewRunner(store, transit.DefaultBoardingWaitPolicy(), nil)
	t.Cleanup(func() {
		close(store.release)
		runner.Drain(context.Background())
	})

	runner.Enqueue(transit.Job{ID: "job-b", Kind: transit.JobKindCompileScenario, ScenarioID: ptr("sc-1")})
	runner.Enqueue(transit.Job{ID: "job-a", Kind: transit.JobKindCompileScenario, ScenarioID: ptr("sc-1")})
	<-store.started
	<-store.started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	abandoned := runner.Drain(ctx)

	slices.Sort(abandoned)
	if !slices.Equal(abandoned, []string{"job-a", "job-b"}) {
		t.Errorf("Drain abandoned %v, want [job-a job-b]", abandoned)
	}
}

func TestRunnerRunsNothingEnqueuedOnceDrainHasBegun(t *testing.T) {
	store := newGatedStore()
	close(store.release)
	runner := compile.NewRunner(store, transit.DefaultBoardingWaitPolicy(), nil)
	runner.Drain(context.Background())

	// A handler still running past a timed-out Shutdown can get here. Its row
	// stays queued for the next boot's sweep rather than racing the drain.
	runner.Enqueue(scenarioJob())

	select {
	case <-store.started:
		t.Fatal("a compile enqueued after Drain began was run")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRunnerDrainWithNothingInFlightReturnsAtOnce(t *testing.T) {
	runner := compile.NewRunner(fixtureStore(), transit.DefaultBoardingWaitPolicy(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if abandoned := runner.Drain(ctx); len(abandoned) != 0 {
		t.Errorf("Drain abandoned %v on an idle runner, want none", abandoned)
	}
}
