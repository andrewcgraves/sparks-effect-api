package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeCompileStore is an in-memory stand-in for the repository slice the
// async compile job surface needs, including the worker.Store methods a
// triggered compile actually runs against.
type fakeCompileStore struct {
	mu sync.Mutex

	scenarios map[string]transit.Scenario
	jobs      map[string]transit.Job

	routes       []transit.Route
	stations     []transit.Station
	services     []transit.Service
	vehicleTypes []transit.VehicleType

	createJobErr   error
	getScenarioErr error
	getJobErr      error
	getGraphErr    error

	// completed receives a copy of the job every time it reaches a terminal
	// state, so a test can wait for the background compile without sleeping.
	completed chan transit.Job
}

func newFakeCompileStore() *fakeCompileStore {
	return &fakeCompileStore{
		scenarios: map[string]transit.Scenario{
			"scenario-a": {ID: "sc-a", Slug: "scenario-a", Name: "Scenario A"},
		},
		jobs:      map[string]transit.Job{},
		completed: make(chan transit.Job, 10),
	}
}

// compilableFixture equips the store with a route/station/service/vehicle set
// that a real compile succeeds against, for tests exercising the whole async
// round trip.
func (f *fakeCompileStore) compilableFixture() {
	f.routes = []transit.Route{{
		ID:   "rt-1",
		Slug: "rt-1",
		Geometry: transit.GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{0, 0}, {1, 0}},
		},
	}}
	f.stations = []transit.Station{
		{ID: "st-a", Slug: "a", Location: transit.GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "low"},
		{ID: "st-b", Slug: "b", Location: transit.GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
	f.services = []transit.Service{{
		ID: "svc-1", RouteID: "rt-1", VehicleTypeID: "vt-1", Active: true,
		Stops: []transit.ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}}
	f.vehicleTypes = []transit.VehicleType{{
		ID: "vt-1", MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1,
		FloorHeight: "high", DwellLevelS: 30, DwellStepS: 60,
	}}
}

func (f *fakeCompileStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	if f.getScenarioErr != nil {
		return transit.Scenario{}, false, f.getScenarioErr
	}
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

func (f *fakeCompileStore) CreateJob(_ context.Context, j transit.Job) error {
	if f.createJobErr != nil {
		return f.createJobErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[j.ID] = j
	return nil
}

func (f *fakeCompileStore) GetJobByID(_ context.Context, id string) (transit.Job, bool, error) {
	if f.getJobErr != nil {
		return transit.Job{}, false, f.getJobErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	return j, ok, nil
}

func (f *fakeCompileStore) GetLatestSucceededJob(_ context.Context, scenarioSlug, kind string) (transit.Job, bool, error) {
	if f.getGraphErr != nil {
		return transit.Job{}, false, f.getGraphErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sc, ok := f.scenarios[scenarioSlug]
	if !ok {
		return transit.Job{}, false, nil
	}
	var latest transit.Job
	found := false
	for _, j := range f.jobs {
		if j.ScenarioID == nil || *j.ScenarioID != sc.ID {
			continue
		}
		if j.Kind != kind || j.Status != transit.JobStatusSucceeded {
			continue
		}
		if !found || j.CreatedAt.After(latest.CreatedAt) {
			latest, found = j, true
		}
	}
	return latest, found, nil
}

func (f *fakeCompileStore) ListRoutesByScenario(_ context.Context, _ string) ([]transit.Route, error) {
	return f.routes, nil
}

func (f *fakeCompileStore) ListStationsByScenario(_ context.Context, _ string) ([]transit.Station, error) {
	return f.stations, nil
}

func (f *fakeCompileStore) ListServicesByScenario(_ context.Context, _ string) ([]transit.Service, error) {
	return f.services, nil
}

func (f *fakeCompileStore) ListVehicleTypes(_ context.Context) ([]transit.VehicleType, error) {
	return f.vehicleTypes, nil
}

func (f *fakeCompileStore) UpdateJobStatus(_ context.Context, id, status, errMsg string) error {
	f.mu.Lock()
	j, ok := f.jobs[id]
	if !ok {
		f.mu.Unlock()
		return errors.New("job not found")
	}
	j.Status, j.Error = status, errMsg
	f.jobs[id] = j
	f.mu.Unlock()
	if status == transit.JobStatusFailed {
		f.completed <- j
	}
	return nil
}

func (f *fakeCompileStore) CompleteJob(_ context.Context, id string, result transit.TransitGraph) error {
	f.mu.Lock()
	j, ok := f.jobs[id]
	if !ok {
		f.mu.Unlock()
		return errors.New("job not found")
	}
	j.Status, j.Result = transit.JobStatusSucceeded, &result
	f.jobs[id] = j
	f.mu.Unlock()
	f.completed <- j
	return nil
}

func (f *fakeCompileStore) waitForCompletion(t *testing.T) transit.Job {
	t.Helper()
	select {
	case j := <-f.completed:
		return j
	case <-time.After(2 * time.Second):
		t.Fatal("compile did not complete in time")
		return transit.Job{}
	}
}

func postAs(t *testing.T, h http.Handler, path, pathValueName, pathValue string, user transit.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.SetPathValue(pathValueName, pathValue)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getWithPathValueAs(t *testing.T, h http.Handler, path, pathValueName, pathValue string, user *transit.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue(pathValueName, pathValue)
	if user != nil {
		req = req.WithContext(auth.WithUser(req.Context(), *user))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The headline acceptance criterion: a POST returns a job id, and a worker
// actually runs the compile and stores a retrievable result.
func TestCompileScenarioReturnsQueuedJobAndCompilesAsync(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableFixture()

	rec := postAs(t, handler.CompileScenario(store), "/api/scenarios/scenario-a/compile", "slug", "scenario-a",
		transit.User{ID: "user-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	var job transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.ID == "" {
		t.Error("job has no id")
	}
	if job.Status != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued", job.Status)
	}
	if job.ScenarioID == nil || *job.ScenarioID != "sc-a" {
		t.Errorf("scenario_id = %v, want sc-a", job.ScenarioID)
	}
	if job.OwnerID == nil || *job.OwnerID != "user-1" {
		t.Errorf("owner_id = %v, want user-1", job.OwnerID)
	}

	completed := store.waitForCompletion(t)
	if completed.Status != transit.JobStatusSucceeded {
		t.Fatalf("completed status = %q, want succeeded", completed.Status)
	}
	if completed.Result == nil || len(completed.Result.Services) != 1 || completed.Result.Services[0].ServiceID != "svc-1" {
		t.Errorf("completed result = %+v, want one compiled ServiceGraph for svc-1", completed.Result)
	}
}

// A scenario the physics compiler rejects fails the job with its error
// recorded, rather than the POST itself failing — the caller already has a
// 202 and a job id by the time the compile runs.
func TestCompileScenarioFailsJobOnBadScenarioData(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableFixture()
	store.services[0].RouteID = "no-such-route"

	rec := postAs(t, handler.CompileScenario(store), "/api/scenarios/scenario-a/compile", "slug", "scenario-a",
		transit.User{ID: "user-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	completed := store.waitForCompletion(t)
	if completed.Status != transit.JobStatusFailed {
		t.Fatalf("completed status = %q, want failed", completed.Status)
	}
	if completed.Error == "" {
		t.Error("failed job has no error message")
	}
}

func TestCompileScenarioRequiresAuth(t *testing.T) {
	store := newFakeCompileStore()
	req := httptest.NewRequest(http.MethodPost, "/api/scenarios/scenario-a/compile", nil)
	req.SetPathValue("slug", "scenario-a")
	rec := httptest.NewRecorder()
	handler.CompileScenario(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCompileScenarioUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	rec := postAs(t, handler.CompileScenario(store), "/api/scenarios/no-such-scenario/compile", "slug", "no-such-scenario",
		transit.User{ID: "user-1"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCompileScenarioReportsStorageFailure(t *testing.T) {
	store := newFakeCompileStore()
	store.createJobErr = errors.New("database is down")

	rec := postAs(t, handler.CompileScenario(store), "/api/scenarios/scenario-a/compile", "slug", "scenario-a",
		transit.User{ID: "user-1"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}

func TestJobStatusReturnsJobForItsOwner(t *testing.T) {
	store := newFakeCompileStore()
	owner := "user-1"
	store.jobs["job-1"] = transit.Job{ID: "job-1", Kind: "compile", Status: transit.JobStatusRunning, OwnerID: &owner}

	user := transit.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.JobStatus(store), "/api/jobs/job-1", "id", "job-1", &user)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var got transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != transit.JobStatusRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
}

// A job that exists but belongs to another user must be indistinguishable
// from a job that doesn't exist, so a caller cannot enumerate job ids.
func TestJobStatusHidesOtherUsersJobsAsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	owner := "user-2"
	store.jobs["job-1"] = transit.Job{ID: "job-1", Kind: "compile", Status: transit.JobStatusRunning, OwnerID: &owner}

	user := transit.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.JobStatus(store), "/api/jobs/job-1", "id", "job-1", &user)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Admin power applies here: an admin can inspect any job, not just their own.
func TestJobStatusAdminCanViewAnyJob(t *testing.T) {
	store := newFakeCompileStore()
	owner := "user-2"
	store.jobs["job-1"] = transit.Job{ID: "job-1", Kind: "compile", Status: transit.JobStatusRunning, OwnerID: &owner}

	admin := transit.User{ID: "admin-1", IsAdmin: true}
	rec := getWithPathValueAs(t, handler.JobStatus(store), "/api/jobs/job-1", "id", "job-1", &admin)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestJobStatusUnknownIDIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	user := transit.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.JobStatus(store), "/api/jobs/no-such-job", "id", "no-such-job", &user)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestJobStatusRequiresAuth(t *testing.T) {
	store := newFakeCompileStore()
	rec := getWithPathValueAs(t, handler.JobStatus(store), "/api/jobs/job-1", "id", "job-1", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// The headline "retrievable by slug" criterion: once a compile job has
// succeeded, its result can be fetched by the scenario's slug with no job id
// in hand.
func TestScenarioGraphReturnsCompiledResult(t *testing.T) {
	store := newFakeCompileStore()
	scenarioID := "sc-a"
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: "compile", Status: transit.JobStatusSucceeded,
		ScenarioID: &scenarioID,
		Result:     &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "svc-1"}}},
	}

	rec := getWithPathValueAs(t, handler.ScenarioGraph(store), "/api/scenarios/scenario-a/graph", "slug", "scenario-a", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var got transit.TransitGraph
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Services) != 1 || got.Services[0].ServiceID != "svc-1" {
		t.Errorf("got = %+v, want the stored graph", got)
	}
}

func TestScenarioGraphNotYetCompiledIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	rec := getWithPathValueAs(t, handler.ScenarioGraph(store), "/api/scenarios/scenario-a/graph", "slug", "scenario-a", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestScenarioGraphReportsStorageFailure(t *testing.T) {
	store := newFakeCompileStore()
	store.getGraphErr = errors.New("database is down")

	rec := getWithPathValueAs(t, handler.ScenarioGraph(store), "/api/scenarios/scenario-a/graph", "slug", "scenario-a", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}
