package transit

import "testing"

func userServiceFixture(id, slug string) UserService {
	return UserService{
		ID:      id,
		Slug:    slug,
		RouteID: "rt-1",
		Vehicle: VehicleParams{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []ServiceStopPoint{
			{Name: "A", Lat: 0, Lng: 0, Seq: 0, Slug: slug + "--a"},
			{Name: "B", Lat: 0, Lng: 1, Seq: 1, Slug: slug + "--b"},
		},
	}
}

func TestCompileUserScenario_compilesMemberServices(t *testing.T) {
	routes := []Route{adapterRoute()}
	services := []UserService{userServiceFixture("svc-1", "line-a")}

	got, err := CompileUserScenario(routes, services, nil, nil, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileUserScenario(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
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
	// SPA-111: every graph key is carried as a node. A lone service's stops are
	// singleton clusters, so there is one node per stop.
	if len(got.Nodes) != 2 {
		t.Errorf("len(Nodes) = %d, want 2 (one per stop)", len(got.Nodes))
	}
}

func TestCompileUserScenario_compilesMultipleMembers(t *testing.T) {
	routes := []Route{adapterRoute()}
	services := []UserService{
		userServiceFixture("svc-1", "line-a"),
		userServiceFixture("svc-2", "line-b"),
	}

	got, err := CompileUserScenario(routes, services, nil, nil, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileUserScenario(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(got.Services))
	}
}

func TestCompileUserScenario_mergesColocatedStopsAcrossMembers(t *testing.T) {
	routes := []Route{adapterRoute()}
	services := []UserService{
		userServiceFixture("svc-1", "line-a"),
		userServiceFixture("svc-2", "line-b"),
	}

	got, err := CompileUserScenario(routes, services, nil, nil, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileUserScenario(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	// The two members share stop coordinates (0,0) and (0,1), so both stops
	// cluster across services: two realised clusters, two graph nodes.
	if len(got.Merge.Clusters) != 2 {
		t.Fatalf("len(Merge.Clusters) = %d, want 2 co-located clusters; report = %+v", len(got.Merge.Clusters), got.Merge)
	}
	if len(got.Nodes) != 2 {
		t.Errorf("len(Nodes) = %d, want 2 merged nodes", len(got.Nodes))
	}
}

func TestCompileUserScenario_errorsOnUnknownRoute(t *testing.T) {
	services := []UserService{userServiceFixture("svc-1", "line-a")}

	if _, err := CompileUserScenario(nil, services, nil, nil, DefaultBoardingWaitPolicy()); err == nil {
		t.Error("CompileUserScenario(DefaultBoardingWaitPolicy()) error = nil, want an error for an unknown route id")
	}
}

func TestCompileUserScenario_emptyScenarioCompilesToEmptyGraph(t *testing.T) {
	got, err := CompileUserScenario(nil, nil, nil, nil, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileUserScenario(DefaultBoardingWaitPolicy()) error = %v, want nil", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("Services = %+v, want none", got.Services)
	}
}
