package compile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func ptr(s string) *string { return &s }

type fakeStore struct {
	routes           []transit.Route
	stations         []transit.Station
	services         []transit.Service
	vehicleTypes     []transit.VehicleType
	scenarios        map[string]transit.Scenario
	travelTimes      map[string]transit.TravelTimes
	userScenarios    map[string]transit.UserScenario
	userServices     map[string]transit.UserService
	listErr          error
	statusCalls      []string
	lastErrMsg       string
	updateErr        error
	completedWith    *transit.TransitGraph
	completedWithIDs []string
	completeErr      error
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

func (f *fakeStore) GetScenarioByID(_ context.Context, id string) (transit.Scenario, bool, error) {
	if f.listErr != nil {
		return transit.Scenario{}, false, f.listErr
	}
	sc, ok := f.scenarios[id]
	return sc, ok, nil
}

func (f *fakeStore) GetTravelTimes(_ context.Context, scenarioSlug string) (transit.TravelTimes, bool, error) {
	if f.listErr != nil {
		return transit.TravelTimes{}, false, f.listErr
	}
	tt, ok := f.travelTimes[scenarioSlug]
	return tt, ok, nil
}

func (f *fakeStore) GetUserScenarioByID(_ context.Context, id string) (transit.UserScenario, bool, error) {
	if f.listErr != nil {
		return transit.UserScenario{}, false, f.listErr
	}
	sc, ok := f.userScenarios[id]
	return sc, ok, nil
}

func (f *fakeStore) GetUserServiceByID(_ context.Context, id string) (transit.UserService, bool, error) {
	if f.listErr != nil {
		return transit.UserService{}, false, f.listErr
	}
	svc, ok := f.userServices[id]
	return svc, ok, nil
}

func (f *fakeStore) ListUserServicesByIDs(_ context.Context, ids []string) ([]transit.UserService, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []transit.UserService
	for _, id := range ids {
		if svc, ok := f.userServices[id]; ok {
			out = append(out, svc)
		}
	}
	return out, nil
}

func (f *fakeStore) ListRoutesByIDs(_ context.Context, ids []string) ([]transit.Route, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []transit.Route
	for _, rt := range f.routes {
		if want[rt.ID] {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateJobStatus(_ context.Context, _, status, errMsg string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.statusCalls = append(f.statusCalls, status)
	f.lastErrMsg = errMsg
	return nil
}

func (f *fakeStore) CompleteJob(_ context.Context, _ string, result transit.TransitGraph, compiledServiceIDs []string) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completedWith = &result
	f.completedWithIDs = compiledServiceIDs
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
		scenarios: map[string]transit.Scenario{
			"sc-1": {ID: "sc-1", Slug: "test"},
		},
		travelTimes: map[string]transit.TravelTimes{
			"test": {ScenarioSlug: "test", Segments: []transit.SegmentTime{
				{FromSlug: "a", ToSlug: "b", RunSeconds: 600, RouteID: "rt-1"},
			}},
		},
	}
}

func userFixtureStore() *fakeStore {
	f := fixtureStore()
	usvc := transit.UserService{
		ID: "usvc-1", Slug: "line-a", RouteID: "rt-1",
		Vehicle: transit.VehicleParams{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []transit.ServiceStopPoint{
			{Name: "A", Lat: 0, Lng: 0, Seq: 0, Slug: "line-a--a"},
			{Name: "B", Lat: 0, Lng: 1, Seq: 1, Slug: "line-a--b"},
		},
	}
	f.userServices = map[string]transit.UserService{usvc.ID: usvc}
	f.userScenarios = map[string]transit.UserScenario{
		"uscn-1": {ID: "uscn-1", Slug: "trip", ServiceIDs: []string{"usvc-1"}},
	}
	return f
}

func scenarioJob() transit.Job {
	return transit.Job{ID: "job-1", Kind: transit.JobKindCompileScenario, ScenarioID: ptr("sc-1")}
}

func TestCompileRunsThenSucceeds(t *testing.T) {
	store := fixtureStore()

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err != nil {
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
	// The compiled member ids are recorded for staleness detection.
	if len(store.completedWithIDs) != 1 || store.completedWithIDs[0] != "svc-1" {
		t.Errorf("compiled service ids = %v, want [svc-1]", store.completedWithIDs)
	}
}

func TestCompileUserScenario(t *testing.T) {
	store := userFixtureStore()
	job := transit.Job{ID: "job-2", Kind: transit.JobKindCompileUserScenario, UserScenarioID: ptr("uscn-1")}

	if err := compile.Compile(context.Background(), store, job, transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if store.completedWith == nil || len(store.completedWith.Services) != 1 {
		t.Fatalf("completed result = %+v, want one compiled service", store.completedWith)
	}
	if store.completedWith.Services[0].ServiceID != "usvc-1" {
		t.Errorf("compiled ServiceID = %q, want usvc-1", store.completedWith.Services[0].ServiceID)
	}
	if len(store.completedWithIDs) != 1 || store.completedWithIDs[0] != "usvc-1" {
		t.Errorf("compiled service ids = %v, want [usvc-1]", store.completedWithIDs)
	}
}

func TestCompileUserService(t *testing.T) {
	store := userFixtureStore()
	job := transit.Job{ID: "job-3", Kind: transit.JobKindCompileUserService, UserServiceID: ptr("usvc-1")}

	if err := compile.Compile(context.Background(), store, job, transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if store.completedWith == nil || len(store.completedWith.Services) != 1 {
		t.Fatalf("completed result = %+v, want one compiled service", store.completedWith)
	}
	if len(store.completedWithIDs) != 1 || store.completedWithIDs[0] != "usvc-1" {
		t.Errorf("compiled service ids = %v, want [usvc-1]", store.completedWithIDs)
	}
}

func TestCompileUserServiceNotFoundFailsJob(t *testing.T) {
	store := userFixtureStore()
	job := transit.Job{ID: "job-4", Kind: transit.JobKindCompileUserService, UserServiceID: ptr("gone")}

	if err := compile.Compile(context.Background(), store, job, transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil (a missing target belongs on the job)", err)
	}
	if len(store.statusCalls) != 2 || store.statusCalls[1] != transit.JobStatusFailed {
		t.Fatalf("status transitions = %v, want [running failed]", store.statusCalls)
	}
	if store.completedWith != nil {
		t.Error("CompleteJob must not be called for a missing target")
	}
}

func TestCompileUnknownKindFailsJob(t *testing.T) {
	store := fixtureStore()
	job := transit.Job{ID: "job-5", Kind: "compute", ScenarioID: ptr("sc-1")}

	if err := compile.Compile(context.Background(), store, job, transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if len(store.statusCalls) != 2 || store.statusCalls[1] != transit.JobStatusFailed {
		t.Fatalf("status transitions = %v, want [running failed]", store.statusCalls)
	}
}

func TestCompileRecordsFailureOnBadScenarioData(t *testing.T) {
	store := fixtureStore()
	store.services[0].VehicleTypeID = "no-such-vehicle-type"

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err != nil {
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

func TestCompileRecordsFailureWhenLoadingScenarioDataFails(t *testing.T) {
	store := fixtureStore()
	store.listErr = errors.New("connection reset")

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if len(store.statusCalls) != 2 || store.statusCalls[1] != transit.JobStatusFailed {
		t.Fatalf("status transitions = %v, want [running failed]", store.statusCalls)
	}
}

func TestCompileReturnsErrorWhenItCannotMarkRunning(t *testing.T) {
	store := fixtureStore()
	store.updateErr = errors.New("database is down")

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err == nil {
		t.Error("Compile() error = nil, want an error when the job cannot be marked running")
	}
}

func TestCompileSucceedsForAnEmptyScenario(t *testing.T) {
	store := fixtureStore()
	store.services = nil

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if store.completedWith == nil || len(store.completedWith.Services) != 0 {
		t.Errorf("completed result = %+v, want an empty graph", store.completedWith)
	}
}

func TestCompileUsesCalibratedRunTimes(t *testing.T) {
	store := fixtureStore()

	if err := compile.Compile(context.Background(), store, scenarioJob(), transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("Compile() error = %v, want nil", err)
	}
	if store.completedWith == nil || len(store.completedWith.Services) != 1 {
		t.Fatalf("completed result = %+v, want one compiled service", store.completedWith)
	}

	// 600 s of run time plus the dwell resolved at the arriving stop: b's
	// platform matches the vehicle floor (level, 30 s), a's does not (step, 60 s).
	want := map[string]int{"a->b": 630, "b->a": 660}
	got := make(map[string]int)
	for _, e := range store.completedWith.Services[0].Edges {
		got[e.FromSlug+"->"+e.ToSlug] = e.Seconds
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("edge %s = %d s, want %d s (calibrated run time + dwell)", k, got[k], w)
		}
	}

	// The graph carries its own geometry, which is what the isochrone plots from.
	if len(store.completedWith.Nodes) != len(store.stations) {
		t.Errorf("len(Nodes) = %d, want %d (one per station)",
			len(store.completedWith.Nodes), len(store.stations))
	}
}
