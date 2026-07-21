package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/andrewcgraves/sparks-effect-api/internal/worker"
)

// fakeStore is an in-memory stand-in for the repository slice a compile job
// needs, plus recorders so tests can assert the status sequence a poller
// would observe.
type fakeStore struct {
	routes       []transit.Route
	stations     []transit.Station
	services     []transit.Service
	vehicleTypes []transit.VehicleType

	listErr error

	statusCalls []string // status argument of each UpdateJobStatus call, in order
	lastErrMsg  string
	updateErr   error

	completedWith *transit.TransitGraph
	completeErr   error
}

func (f *fakeStore) ListRoutesByScenario(_ context.Context, _ string) ([]transit.Route, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.routes, nil
}

func (f *fakeStore) ListStationsByScenario(_ context.Context, _ string) ([]transit.Station, error) {
	return f.stations, nil
}

func (f *fakeStore) ListServicesByScenario(_ context.Context, _ string) ([]transit.Service, error) {
	return f.services, nil
}

func (f *fakeStore) ListVehicleTypes(_ context.Context) ([]transit.VehicleType, error) {
	return f.vehicleTypes, nil
}

func (f *fakeStore) UpdateJobStatus(_ context.Context, _, status, errMsg string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.statusCalls = append(f.statusCalls, status)
	f.lastErrMsg = errMsg
	return nil
}

func (f *fakeStore) CompleteJob(_ context.Context, _ string, result transit.TransitGraph) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completedWith = &result
	return nil
}

func fixtureStore() *fakeStore {
	return &fakeStore{
		routes: []transit.Route{{
			ID:   "rt-1",
			Slug: "rt-1",
			Geometry: transit.GeoLineString{
				Type:        "LineString",
				Coordinates: [][]float64{{0, 0}, {1, 0}},
			},
		}},
		stations: []transit.Station{
			{ID: "st-a", Slug: "a", Location: transit.GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "low"},
			{ID: "st-b", Slug: "b", Location: transit.GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
		},
		services: []transit.Service{{
			ID: "svc-1", RouteID: "rt-1", VehicleTypeID: "vt-1", Active: true,
			Stops: []transit.ServiceStop{
				{StationID: "st-a", Sequence: 1},
				{StationID: "st-b", Sequence: 2},
			},
		}},
		vehicleTypes: []transit.VehicleType{{
			ID: "vt-1", MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1,
			FloorHeight: "high", DwellLevelS: 30, DwellStepS: 60,
		}},
	}
}

// The headline lifecycle: queued -> running -> succeeded, with the compiled
// graph stored on completion.
func TestCompileRunsThenSucceeds(t *testing.T) {
	store := fixtureStore()

	if err := worker.Compile(context.Background(), store, "job-1", "sc-1"); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}

	if len(store.statusCalls) != 1 || store.statusCalls[0] != transit.JobStatusRunning {
		t.Fatalf("status transitions = %v, want [running] (success completes via CompleteJob, not UpdateJobStatus)", store.statusCalls)
	}
	if store.completedWith == nil {
		t.Fatal("CompleteJob was never called")
	}
	if len(store.completedWith.Services) != 1 || store.completedWith.Services[0].ServiceID != "svc-1" {
		t.Errorf("completed result = %+v, want one ServiceGraph for svc-1", store.completedWith)
	}
}

// A scenario whose data the physics compiler rejects (here, a service
// pointing at a route id that doesn't exist in the loaded set) fails the job
// with the error recorded, rather than panicking or silently succeeding.
func TestCompileRecordsFailureOnBadScenarioData(t *testing.T) {
	store := fixtureStore()
	store.services[0].RouteID = "no-such-route"

	if err := worker.Compile(context.Background(), store, "job-1", "sc-1"); err != nil {
		t.Fatalf("Compile() error = %v, want nil (failure belongs on the job)", err)
	}

	if len(store.statusCalls) != 2 || store.statusCalls[1] != transit.JobStatusFailed {
		t.Fatalf("status transitions = %v, want [running failed]", store.statusCalls)
	}
	if store.lastErrMsg == "" {
		t.Error("failed job has no error message")
	}
	if store.completedWith != nil {
		t.Error("CompleteJob must not be called for a failed compile")
	}
}

// A repository failure while loading the scenario's composition is also a
// job failure, not a crash.
func TestCompileRecordsFailureWhenLoadingScenarioDataFails(t *testing.T) {
	store := fixtureStore()
	store.listErr = errors.New("connection reset")

	if err := worker.Compile(context.Background(), store, "job-1", "sc-1"); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if len(store.statusCalls) != 2 || store.statusCalls[1] != transit.JobStatusFailed {
		t.Fatalf("status transitions = %v, want [running failed]", store.statusCalls)
	}
}

// If the store can't even be marked running, Compile reports that to its
// caller — there is nowhere else for that failure to go.
func TestCompileReturnsErrorWhenItCannotMarkRunning(t *testing.T) {
	store := fixtureStore()
	store.updateErr = errors.New("database is down")

	if err := worker.Compile(context.Background(), store, "job-1", "sc-1"); err == nil {
		t.Error("Compile() error = nil, want an error when the job cannot be marked running")
	}
}

// An empty scenario (no services yet) is a legitimate success, not a failure
// — this is the state a freshly-created, not-yet-authored scenario compiles
// to.
func TestCompileSucceedsForAnEmptyScenario(t *testing.T) {
	store := fixtureStore()
	store.services = nil

	if err := worker.Compile(context.Background(), store, "job-1", "sc-1"); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if store.completedWith == nil || len(store.completedWith.Services) != 0 {
		t.Errorf("completed result = %+v, want an empty graph", store.completedWith)
	}
}
