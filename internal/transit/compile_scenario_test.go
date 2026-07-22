package transit

import "testing"

// scenarioFixture builds a two-station, one-route, one-service scenario
// reusing physicsTestVehicle's clean kinematics, so CompileScenario's edge
// values can be checked against the same hand-worked motion time as
// TestCompileServicePhysics_twoStopStraightLine.
func scenarioFixture() ([]Route, []Station, []Service, []VehicleType) {
	route := Route{
		ID:   "rt-1",
		Slug: "rt-1",
		Geometry: GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{0, 0}, {1, 0}},
		},
	}
	stations := []Station{
		{ID: "st-a", Slug: "a", Location: GeoPoint{Coordinates: []float64{0, 0}}, PlatformHeight: "low"},
		{ID: "st-b", Slug: "b", Location: GeoPoint{Coordinates: []float64{1, 0}}, PlatformHeight: "high"},
	}
	svc := Service{
		ID:            "svc-1",
		RouteID:       "rt-1",
		VehicleTypeID: "vt-physics",
		Active:        true,
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
	}
	return []Route{route}, stations, []Service{svc}, []VehicleType{physicsTestVehicle()}
}

// The headline behaviour: an active service compiles into a ServiceGraph with
// the same edges CompileServicePhysics itself produces — CompileScenario is
// just the per-scenario fan-out over it.
func TestCompileScenario_compilesActiveServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()

	got, err := CompileScenario(routes, stations, services, vehicleTypes)
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(got.Services))
	}
	if got.Services[0].ServiceID != "svc-1" {
		t.Errorf("ServiceID = %q, want svc-1", got.Services[0].ServiceID)
	}
	if len(got.Services[0].Edges) != 2 {
		t.Errorf("len(Edges) = %d, want 2", len(got.Services[0].Edges))
	}
}

// An inactive service contributes nothing, matching Compile's own convention.
//
// This is now the only place Active is honoured on the physics path: the
// compiler takes a CompilableService and never sees a Service, so scenario
// assembly is where membership is decided.
func TestCompileScenario_skipsInactiveServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].Active = false

	got, err := CompileScenario(routes, stations, services, vehicleTypes)
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services = %+v, want none for an inactive service", got.Services)
	}
}

// Multiple active services each get their own ServiceGraph.
func TestCompileScenario_compilesMultipleServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	second := services[0]
	second.ID = "svc-2"
	services = append(services, second)

	got, err := CompileScenario(routes, stations, services, vehicleTypes)
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(got.Services))
	}
}

// A service referencing a route absent from the supplied slice is a caller
// error (an id from a different scenario, say), not a silent skip.
func TestCompileScenario_errorsOnUnknownRoute(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].RouteID = "no-such-route"

	if _, err := CompileScenario(routes, stations, services, vehicleTypes); err == nil {
		t.Error("CompileScenario() error = nil, want an error for an unknown route id")
	}
}

// Same for an unknown vehicle type id.
func TestCompileScenario_errorsOnUnknownVehicleType(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].VehicleTypeID = "no-such-vehicle"

	if _, err := CompileScenario(routes, stations, services, vehicleTypes); err == nil {
		t.Error("CompileScenario() error = nil, want an error for an unknown vehicle type id")
	}
}

// An empty scenario (no services) compiles to an empty graph rather than an
// error — the boundary an async job hits for a freshly-created scenario.
func TestCompileScenario_emptyScenarioCompilesToEmptyGraph(t *testing.T) {
	got, err := CompileScenario(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CompileScenario() error = %v, want nil", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services = %+v, want none", got.Services)
	}
}
