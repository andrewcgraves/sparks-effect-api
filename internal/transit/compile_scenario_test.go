package transit

import "testing"

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

func TestCompileSeededPhysics_compilesActiveServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()

	got, err := compileSeededPhysics(routes, stations, services, vehicleTypes, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
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

func TestCompileSeededPhysics_skipsInactiveServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].Active = false

	got, err := compileSeededPhysics(routes, stations, services, vehicleTypes, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services = %+v, want none for an inactive service", got.Services)
	}
}

func TestCompileSeededPhysics_compilesMultipleServices(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	second := services[0]
	second.ID = "svc-2"
	services = append(services, second)

	got, err := compileSeededPhysics(routes, stations, services, vehicleTypes, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(got.Services))
	}
}

func TestCompileSeededPhysics_errorsOnUnknownRoute(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].RouteID = "no-such-route"

	if _, err := compileSeededPhysics(routes, stations, services, vehicleTypes, DefaultBoardingWaitPolicy()); err == nil {
		t.Error("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = nil, want an error for an unknown route id")
	}
}

func TestCompileSeededPhysics_errorsOnUnknownVehicleType(t *testing.T) {
	routes, stations, services, vehicleTypes := scenarioFixture()
	services[0].VehicleTypeID = "no-such-vehicle"

	if _, err := compileSeededPhysics(routes, stations, services, vehicleTypes, DefaultBoardingWaitPolicy()); err == nil {
		t.Error("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = nil, want an error for an unknown vehicle type id")
	}
}

func TestCompileSeededPhysics_emptyScenarioCompilesToEmptyGraph(t *testing.T) {
	got, err := compileSeededPhysics(nil, nil, nil, nil, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("compileSeededPhysics(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services = %+v, want none", got.Services)
	}
}
