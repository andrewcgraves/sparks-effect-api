package transit

import "testing"

func TestNewStore_caHSRDefaultBoardingWaitIsNone(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not found")
	}

	const (
		localID      = "00000000-0000-4004-8001-000000000002"
		brightlineID = "00000000-0000-4004-8001-000000000003"
	)
	byID := map[string]ServiceGraph{}
	for _, sg := range g.Services {
		byID[sg.ServiceID] = sg
	}
	for _, id := range []string{localID, brightlineID} {
		sg, ok := byID[id]
		if !ok {
			t.Fatalf("service %s missing from compiled graph", id)
		}
		if sg.WaitSecs != 0 {
			t.Errorf("%s WaitSecs: want 0 (default none), got %d", id, sg.WaitSecs)
		}
		if sg.WaitPolicy != string(BoardingWaitNone) {
			t.Errorf("%s WaitPolicy: want %q, got %q", id, BoardingWaitNone, sg.WaitPolicy)
		}
	}
}

func TestCompile_halfHeadwayPreservesCAHSRWaits(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}
	g, err := Compile(
		sc,
		store.GetRoutesByScenario(sc.ID),
		store.GetStationsByScenario(sc.ID),
		store.GetServicesByScenario(sc.ID),
		append([]VehicleType(nil), store.vehicleTypes...),
		mustTravelTimes(t, store, "ca-hsr"),
		BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway},
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	want := map[string]int{
		"00000000-0000-4004-8001-000000000002": 1800, // HSR Local
		"00000000-0000-4004-8001-000000000003": 3600, // Brightline West
	}
	for _, sg := range g.Services {
		if w, ok := want[sg.ServiceID]; ok && sg.WaitSecs != w {
			t.Errorf("%s WaitSecs: want %d, got %d", sg.ServiceID, w, sg.WaitSecs)
		}
	}
}

func TestCompileParity_bothCompilersAgreeOnWaitSecs(t *testing.T) {
	windows := []FrequencyWindow{{HeadwayS: 3600}, {HeadwayS: 5400}}
	policies := []BoardingWaitPolicy{
		{Kind: BoardingWaitNone},
		{Kind: BoardingWaitHalfHeadway},
		{Kind: BoardingWaitFullHeadway},
		{Kind: BoardingWaitFixed, FixedSecs: 120},
	}

	sc := Scenario{ID: "sc-1", Slug: "test"}
	seededSvc := Service{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
		FrequencyWindows: windows,
	}

	route := Route{
		ID: "rt-1",
		Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{
			{-122.4, 37.7}, {-122.5, 37.8},
		}},
	}
	cs := CompilableService{
		ID: "svc-1", Route: route,
		Vehicle: Kinematics{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1},
		Stops: []CompilableStop{
			{Slug: "a", Lat: 37.7, Lng: -122.4, DwellS: 30},
			{Slug: "b", Lat: 37.8, Lng: -122.5, DwellS: 30},
		},
		Windows: windows,
	}

	for _, policy := range policies {
		t.Run(string(policy.Kind), func(t *testing.T) {
			seeded, err := Compile(sc, nil, testStations(), []Service{seededSvc}, []VehicleType{testVehicle()}, testSegments(), policy)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			physics, err := CompileServicePhysics(cs, policy)
			if err != nil {
				t.Fatalf("CompileServicePhysics: %v", err)
			}
			if seeded.Services[0].WaitSecs != physics.WaitSecs {
				t.Errorf("WaitSecs mismatch: seeded=%d physics=%d", seeded.Services[0].WaitSecs, physics.WaitSecs)
			}
			if seeded.Services[0].WaitPolicy != physics.WaitPolicy {
				t.Errorf("WaitPolicy mismatch: seeded=%q physics=%q", seeded.Services[0].WaitPolicy, physics.WaitPolicy)
			}
		})
	}
}

func TestCompileParity_overridesAgreeOnWaitSecs(t *testing.T) {
	windows := []FrequencyWindow{{HeadwayS: 3600}, {HeadwayS: 5400}}
	override := &BoardingWaitOverride{Policy: BoardingWaitFixed, Secs: intPtr(45)}
	global := BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway}

	sc := Scenario{ID: "sc-1", Slug: "test"}
	seededSvc := Service{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
		FrequencyWindows: windows,
		BoardingWait:     override,
	}
	cs := CompilableService{
		ID: "svc-1", Route: Route{
			ID: "rt-1",
			Geometry: GeoLineString{Type: "LineString", Coordinates: [][]float64{
				{-122.4, 37.7}, {-122.5, 37.8},
			}},
		},
		Vehicle: Kinematics{MaxSpeedKMH: 36, AccelerationMS2: 1, DecelerationMS2: 1},
		Stops: []CompilableStop{
			{Slug: "a", Lat: 37.7, Lng: -122.4, DwellS: 30},
			{Slug: "b", Lat: 37.8, Lng: -122.5, DwellS: 30},
		},
		Windows:      windows,
		BoardingWait: override,
	}

	seeded, err := Compile(sc, nil, testStations(), []Service{seededSvc}, []VehicleType{testVehicle()}, testSegments(), global)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	physics, err := CompileServicePhysics(cs, global)
	if err != nil {
		t.Fatalf("CompileServicePhysics: %v", err)
	}
	if seeded.Services[0].WaitSecs != 45 || physics.WaitSecs != 45 {
		t.Errorf("WaitSecs: seeded=%d physics=%d, want 45 (service override, not half_headway 1800)",
			seeded.Services[0].WaitSecs, physics.WaitSecs)
	}
	if seeded.Services[0].WaitPolicy != string(BoardingWaitFixed) || physics.WaitPolicy != string(BoardingWaitFixed) {
		t.Errorf("WaitPolicy: seeded=%q physics=%q, want fixed", seeded.Services[0].WaitPolicy, physics.WaitPolicy)
	}
}

func TestCompile_changingOnlyOverrideMovesWaitSecs(t *testing.T) {
	windows := []FrequencyWindow{{HeadwayS: 3600}}
	svc := Service{
		ID: "svc-1", Active: true, VehicleTypeID: "vt-1",
		Stops: []ServiceStop{
			{StationID: "st-a", Sequence: 1},
			{StationID: "st-b", Sequence: 2},
		},
		FrequencyWindows: windows,
	}
	compile := func(override *BoardingWaitOverride) int {
		t.Helper()
		svc.BoardingWait = override
		g, err := Compile(Scenario{ID: "sc-1"}, nil, testStations(), []Service{svc}, []VehicleType{testVehicle()}, testSegments(), DefaultBoardingWaitPolicy())
		if err != nil {
			t.Fatalf("Compile: %v", err)
		}
		return g.Services[0].WaitSecs
	}

	if got := compile(nil); got != 0 {
		t.Errorf("inherit none: WaitSecs = %d, want 0", got)
	}
	if got := compile(&BoardingWaitOverride{Policy: BoardingWaitHalfHeadway}); got != 1800 {
		t.Errorf("half_headway override: WaitSecs = %d, want 1800", got)
	}
	if got := compile(&BoardingWaitOverride{Policy: BoardingWaitFixed, Secs: intPtr(12)}); got != 12 {
		t.Errorf("fixed override: WaitSecs = %d, want 12", got)
	}
}

func TestCompileUserScenario_scenarioOverrideAppliesToMembersWithoutOwn(t *testing.T) {
	routes := []Route{adapterRoute()}
	plain := userServiceFixture("svc-1", "line-a")
	plain.FrequencyWindows = []FrequencyWindow{{HeadwayS: 3600}}
	own := userServiceFixture("svc-2", "line-b")
	own.FrequencyWindows = []FrequencyWindow{{HeadwayS: 3600}}
	own.BoardingWait = &BoardingWaitOverride{Policy: BoardingWaitFixed, Secs: intPtr(7)}

	scenarioWait := &BoardingWaitOverride{Policy: BoardingWaitHalfHeadway}
	got, err := CompileUserScenario(routes, []UserService{plain, own}, nil, scenarioWait, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileUserScenario: %v", err)
	}
	byID := map[string]ServiceGraph{}
	for _, sg := range got.Services {
		byID[sg.ServiceID] = sg
	}
	if byID["svc-1"].WaitSecs != 1800 || byID["svc-1"].WaitPolicy != string(BoardingWaitHalfHeadway) {
		t.Errorf("svc-1 (inherit scenario): WaitSecs=%d WaitPolicy=%q, want 1800 half_headway",
			byID["svc-1"].WaitSecs, byID["svc-1"].WaitPolicy)
	}
	if byID["svc-2"].WaitSecs != 7 || byID["svc-2"].WaitPolicy != string(BoardingWaitFixed) {
		t.Errorf("svc-2 (own override): WaitSecs=%d WaitPolicy=%q, want 7 fixed",
			byID["svc-2"].WaitSecs, byID["svc-2"].WaitPolicy)
	}
}

func TestCompileUserScenario_sharedServiceGetsEachScenariosOverride(t *testing.T) {
	routes := []Route{adapterRoute()}
	shared := userServiceFixture("svc-shared", "shared")
	shared.FrequencyWindows = []FrequencyWindow{{HeadwayS: 2400}}

	half := &BoardingWaitOverride{Policy: BoardingWaitHalfHeadway}
	full := &BoardingWaitOverride{Policy: BoardingWaitFullHeadway}

	a, err := CompileUserScenario(routes, []UserService{shared}, nil, half, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("scenario A: %v", err)
	}
	b, err := CompileUserScenario(routes, []UserService{shared}, nil, full, DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("scenario B: %v", err)
	}
	if a.Services[0].WaitSecs != 1200 {
		t.Errorf("scenario A WaitSecs = %d, want 1200 (half of 2400)", a.Services[0].WaitSecs)
	}
	if b.Services[0].WaitSecs != 2400 {
		t.Errorf("scenario B WaitSecs = %d, want 2400 (full headway)", b.Services[0].WaitSecs)
	}
}

func mustTravelTimes(t *testing.T, store *Store, slug string) TravelTimes {
	t.Helper()
	tt, ok := store.GetTravelTimes(slug)
	if !ok {
		t.Fatalf("travel times for %q not found", slug)
	}
	return tt
}
