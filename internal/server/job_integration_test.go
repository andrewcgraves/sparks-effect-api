package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// seedCompilableScenario writes a scenario with one route, two stations, one
// vehicle type, and one active service straight through the repository —
// standing in for whatever future ticket (SPA-80/81) lets a user author this
// data over HTTP. What matters here is only that it is real, physics-compilable
// data sitting in Postgres for the job to compile.
func seedCompilableScenario(t *testing.T, repo interface {
	CreateScenario(ctx context.Context, sc transit.Scenario) error
	CreateRoute(ctx context.Context, r transit.Route) error
	CreateStation(ctx context.Context, st transit.Station) error
	CreateVehicleType(ctx context.Context, vt transit.VehicleType) error
	CreateService(ctx context.Context, svc transit.Service) error
}, slug string) transit.Scenario {
	t.Helper()
	ctx := context.Background()

	sc := transit.Scenario{ID: mustUUID(t), Slug: slug, Name: "Compilable Scenario"}
	if err := repo.CreateScenario(ctx, sc); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}

	routeID := mustUUID(t)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, ScenarioID: &sc.ID, Slug: slug + "-route", Name: "Route", Mode: "rail",
		Geometry:      transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 0}}},
		Bidirectional: true,
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	stationA := transit.Station{ID: mustUUID(t), ScenarioID: sc.ID, Slug: "a", Name: "A",
		Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{0, 0}}, PlatformHeight: "low"}
	stationB := transit.Station{ID: mustUUID(t), ScenarioID: sc.ID, Slug: "b", Name: "B",
		Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{1, 0}}, PlatformHeight: "high"}
	for _, st := range []transit.Station{stationA, stationB} {
		if err := repo.CreateStation(ctx, st); err != nil {
			t.Fatalf("CreateStation %s: %v", st.Slug, err)
		}
	}

	vt := transit.VehicleType{
		ID: mustUUID(t), Name: "Test EMU", MaxSpeedKMH: 36,
		AccelerationMS2: 1, DecelerationMS2: 1, FloorHeight: "high",
		DwellLevelS: 30, DwellStepS: 60,
	}
	if err := repo.CreateVehicleType(ctx, vt); err != nil {
		t.Fatalf("CreateVehicleType: %v", err)
	}

	if err := repo.CreateService(ctx, transit.Service{
		ID: mustUUID(t), ScenarioID: sc.ID, RouteID: routeID, VehicleTypeID: vt.ID,
		Name: "Main Service", Active: true, Provenance: transit.ProvenanceComputed,
		Stops: []transit.ServiceStop{
			{StationID: stationA.ID, Sequence: 1},
			{StationID: stationB.ID, Sequence: 2},
		},
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return sc
}

// pollJob polls GET /api/jobs/{id} until it leaves queued/running or the
// timeout elapses, returning the final observed job.
func pollJob(t *testing.T, h http.Handler, token, jobID string) transit.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := request(t, h, http.MethodGet, "/api/jobs/"+jobID, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/jobs/%s: status %d; body %s", jobID, rec.Code, rec.Body.String())
		}
		var job transit.Job
		if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		if job.Status == transit.JobStatusSucceeded || job.Status == transit.JobStatusFailed {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not reach a terminal state in time")
	return transit.Job{}
}

// The whole SPA-82 acceptance surface, end to end against a real database and
// mux: POST kicks off a job, GET polls it through to succeeded, and the
// compiled graph is retrievable by the scenario's slug — with no job id.
func TestIntegration_AsyncCompileJobLifecycle(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	token, status := login(t, h, "admin@example.com", "admin-password")
	if status != http.StatusOK {
		t.Fatalf("login: status %d", status)
	}

	sc := seedCompilableScenario(t, repo, "compile-me")

	rec := request(t, h, http.MethodPost, "/api/scenarios/"+sc.Slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Status != transit.JobStatusQueued {
		t.Fatalf("initial status = %q, want queued", created.Status)
	}

	final := pollJob(t, h, token, created.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("final status = %q, want succeeded (error: %s)", final.Status, final.Error)
	}
	if final.Result == nil || len(final.Result.Services) != 1 {
		t.Fatalf("job result = %+v, want one compiled service", final.Result)
	}

	// Retrievable by slug, with no job id involved and no auth required.
	graphRec := request(t, h, http.MethodGet, "/api/scenarios/"+sc.Slug+"/graph", "")
	if graphRec.Code != http.StatusOK {
		t.Fatalf("GET graph: status %d, want 200; body %s", graphRec.Code, graphRec.Body.String())
	}
	var graph transit.TransitGraph
	if err := json.NewDecoder(graphRec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(graph.Services) != 1 || len(graph.Services[0].Edges) != 2 {
		t.Errorf("graph = %+v, want one service with 2 edges", graph)
	}

	// The existing synchronous isochrone path is untouched by any of this.
	isoRec := request(t, h, http.MethodPost, "/api/isochrone", token)
	if isoRec.Code == http.StatusNotFound {
		t.Error("POST /api/isochrone: route disappeared, want it still registered")
	}
}

// A scenario whose service data the physics compiler rejects fails the job
// with an error, rather than hanging or panicking the background goroutine.
func TestIntegration_CompileJobSurfacesFailure(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	token, _ := login(t, h, "admin@example.com", "admin-password")

	// A route with a single geometry point — too short to project stops onto
	// (the FK constraints on services.route_id/vehicle_type_id mean a
	// dangling reference can never actually reach Postgres, so this is the
	// realistic way real scenario data fails a physics compile).
	broken := transit.Scenario{ID: mustUUID(t), Slug: "broken-scenario", Name: "Broken"}
	if err := repo.CreateScenario(context.Background(), broken); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	routeID := mustUUID(t)
	if err := repo.CreateRoute(context.Background(), transit.Route{
		ID: routeID, ScenarioID: &broken.ID, Slug: "broken-route", Name: "Too Short",
		Geometry:      transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}}},
		Bidirectional: true,
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	stationA := transit.Station{ID: mustUUID(t), ScenarioID: broken.ID, Slug: "x",
		Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{0, 0}}}
	stationB := transit.Station{ID: mustUUID(t), ScenarioID: broken.ID, Slug: "y",
		Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{1, 0}}}
	for _, st := range []transit.Station{stationA, stationB} {
		if err := repo.CreateStation(context.Background(), st); err != nil {
			t.Fatalf("CreateStation: %v", err)
		}
	}
	vt := transit.VehicleType{ID: mustUUID(t), Name: "VT", MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1}
	if err := repo.CreateVehicleType(context.Background(), vt); err != nil {
		t.Fatalf("CreateVehicleType: %v", err)
	}
	if err := repo.CreateService(context.Background(), transit.Service{
		ID: mustUUID(t), ScenarioID: broken.ID, RouteID: routeID, VehicleTypeID: vt.ID,
		Name: "Unprojectable", Active: true,
		Stops: []transit.ServiceStop{
			{StationID: stationA.ID, Sequence: 1},
			{StationID: stationB.ID, Sequence: 2},
		},
	}); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	rec := request(t, h, http.MethodPost, "/api/scenarios/"+broken.Slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	final := pollJob(t, h, token, created.ID)
	if final.Status != transit.JobStatusFailed {
		t.Fatalf("final status = %q, want failed", final.Status)
	}
	if final.Error == "" {
		t.Error("failed job has no error message")
	}

	// No graph was ever produced for this scenario.
	graphRec := request(t, h, http.MethodGet, "/api/scenarios/"+broken.Slug+"/graph", "")
	if graphRec.Code != http.StatusNotFound {
		t.Errorf("GET graph after a failed compile: status %d, want 404", graphRec.Code)
	}
}
