package transit

import (
	"testing"
)

func mustNewStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestNewStore(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store == nil {
		t.Fatal("NewStore returned nil")
	}
}

func TestGetScenarios(t *testing.T) {
	store := mustNewStore(t)
	scenarios := store.GetScenarios()
	if len(scenarios) == 0 {
		t.Fatal("expected at least one scenario")
	}
}

func TestGetScenarioBySlug_found(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	if sc.Slug != "ca-hsr" {
		t.Errorf("slug: want ca-hsr, got %s", sc.Slug)
	}
	if sc.Name == "" {
		t.Error("scenario name is empty")
	}
	if sc.Status != "published" {
		t.Errorf("status: want published, got %s", sc.Status)
	}
}

func TestGetScenarioBySlug_notFound(t *testing.T) {
	store := mustNewStore(t)
	_, ok := store.GetScenarioBySlug("does-not-exist")
	if ok {
		t.Error("expected not found for unknown slug")
	}
}

func TestGetRoutesByScenario(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	routes := store.GetRoutesByScenario(sc.ID)

	if len(routes) != 2 {
		t.Fatalf("expected 2 active routes (Phase 1 + Brightline West), got %d", len(routes))
	}

	for _, r := range routes {
		if r.Slug == "" {
			t.Errorf("route %q has empty slug", r.Name)
		}
		if r.Mode != "rail" {
			t.Errorf("route %q mode: want rail, got %s", r.Name, r.Mode)
		}
		if r.Geometry.Type != "LineString" {
			t.Errorf("route %q geometry type: want LineString, got %s", r.Name, r.Geometry.Type)
		}
		if len(r.Geometry.Coordinates) < 2 {
			t.Errorf("route %q has fewer than 2 coordinate points", r.Name)
		}
	}
}

func TestGetStationsByScenario(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	stations := store.GetStationsByScenario(sc.ID)

	if len(stations) != 15 {
		t.Errorf("expected 15 stations (13 Phase 1 + Victor Valley and Las Vegas), got %d", len(stations))
	}

	slugsSeen := make(map[string]bool)
	for _, st := range stations {
		if st.Name == "" {
			t.Errorf("station %s has empty name", st.ID)
		}
		if len(st.Location.Coordinates) != 2 {
			t.Errorf("station %s location has %d coordinates, want 2", st.Slug, len(st.Location.Coordinates))
		}
		if st.Location.Type != "Point" {
			t.Errorf("station %s location type: want Point, got %s", st.Slug, st.Location.Type)
		}
		if slugsSeen[st.Slug] {
			t.Errorf("duplicate station slug %q", st.Slug)
		}
		slugsSeen[st.Slug] = true
	}

	required := []string{"sf", "millbrae", "san-jose", "gilroy", "merced", "madera",
		"fresno", "kings-tulare", "bakersfield", "palmdale",
		"burbank-airport", "los-angeles", "anaheim",
		"victor-valley", "las-vegas"}
	for _, slug := range required {
		if !slugsSeen[slug] {
			t.Errorf("missing required station slug %q", slug)
		}
	}
}

func TestStationCoordinates(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	stations := store.GetStationsByScenario(sc.ID)

	bySlug := make(map[string][2]float64, len(stations))
	for _, st := range stations {
		bySlug[st.Slug] = [2]float64{st.Location.Coordinates[0], st.Location.Coordinates[1]}
	}

	want := map[string][2]float64{
		"sf":              {-122.397, 37.790},
		"millbrae":        {-122.387, 37.600},
		"san-jose":        {-121.903, 37.330},
		"gilroy":          {-121.567, 37.004},
		"merced":          {-120.491, 37.302},
		"madera":          {-119.986, 36.936},
		"fresno":          {-119.794, 36.733},
		"kings-tulare":    {-119.592, 36.335},
		"bakersfield":     {-119.022, 35.391},
		"palmdale":        {-118.119, 34.591},
		"burbank-airport": {-118.353, 34.202},
		"los-angeles":     {-118.235, 34.055},
		"anaheim":         {-117.878, 33.803},
	}

	for slug, wantCoords := range want {
		got, ok := bySlug[slug]
		if !ok {
			t.Errorf("station %q not found", slug)
			continue
		}
		if got[0] != wantCoords[0] || got[1] != wantCoords[1] {
			t.Errorf("station %q coordinates: want [%v, %v], got [%v, %v]",
				slug, wantCoords[0], wantCoords[1], got[0], got[1])
		}
	}
}

func TestGetServicesByScenario(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	services := store.GetServicesByScenario(sc.ID)

	if len(services) != 3 {
		t.Fatalf("expected 3 active services (Express + Local + Brightline West), got %d", len(services))
	}

	for _, svc := range services {
		if !svc.Active {
			t.Errorf("service %q has Active == false", svc.Name)
		}
		if len(svc.Stops) == 0 {
			t.Errorf("service %q has no stops", svc.Name)
		}
		if len(svc.FrequencyWindows) == 0 {
			t.Errorf("service %q has no frequency windows", svc.Name)
		}
		for i, fw := range svc.FrequencyWindows {
			if fw.HeadwayS <= 0 {
				t.Errorf("service %q frequency window %d has invalid headway_s %d", svc.Name, i, fw.HeadwayS)
			}
		}
	}
}

func TestGetVehicleTypeByID(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	services := store.GetServicesByScenario(sc.ID)

	if len(services) == 0 {
		t.Skip("no services loaded")
	}

	vt, ok := store.GetVehicleTypeByID(services[0].VehicleTypeID)
	if !ok {
		t.Fatal("vehicle type not found for first service")
	}
	if vt.MaxSpeedKMH <= 0 {
		t.Error("vehicle type max_speed_kmh must be positive")
	}
	if vt.Propulsion == "" {
		t.Error("vehicle type propulsion is empty")
	}
}

func TestGetTravelTimes(t *testing.T) {
	store := mustNewStore(t)

	tt, ok := store.GetTravelTimes("ca-hsr")
	if !ok {
		t.Fatal("travel times not found for ca-hsr")
	}
	if tt.ScenarioSlug != "ca-hsr" {
		t.Errorf("scenario_slug: want ca-hsr, got %s", tt.ScenarioSlug)
	}
	if len(tt.Segments) == 0 {
		t.Error("segments is empty")
	}

	found := false
	for _, seg := range tt.Segments {
		if seg.FromSlug == "sf" && seg.ToSlug == "millbrae" {
			if seg.RunSeconds != 760 {
				t.Errorf("sf→millbrae: want 760 run_seconds, got %d", seg.RunSeconds)
			}
			found = true
		}
	}
	if !found {
		t.Error("sf→millbrae segment not found")
	}

	_, ok = store.GetTravelTimes("no-such-scenario")
	if ok {
		t.Error("expected false for unknown scenario slug")
	}
}

// Every seeded segment names the route it is a span of, so a client can group
// "time between stations" by corridor once a scenario carries more than one.
func TestGetTravelTimesSegmentsCarryKnownRouteID(t *testing.T) {
	store := mustNewStore(t)

	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	known := make(map[string]bool)
	for _, rt := range store.GetRoutesByScenario(sc.ID) {
		known[rt.ID] = true
	}

	tt, ok := store.GetTravelTimes("ca-hsr")
	if !ok {
		t.Fatal("travel times not found for ca-hsr")
	}
	for _, seg := range tt.Segments {
		if !known[seg.RouteID] {
			t.Errorf("segment %s→%s: route_id %q is not a route of ca-hsr", seg.FromSlug, seg.ToSlug, seg.RouteID)
		}
	}
}

func TestTravelTimeBetween(t *testing.T) {
	store := mustNewStore(t)

	got, _, svcID, ok := store.TravelTimeBetween("ca-hsr", "sf", "millbrae")
	if !ok {
		t.Fatal("TravelTimeBetween: sf→millbrae not found")
	}
	if got != 850 {
		t.Errorf("sf→millbrae: want 850 (run_seconds 760 + dwell 90), got %d", got)
	}
	if svcID == "" {
		t.Error("sf→millbrae: serviceID must be non-empty")
	}

	got, _, _, ok = store.TravelTimeBetween("ca-hsr", "sf", "san-jose")
	if !ok {
		t.Fatal("TravelTimeBetween: sf→san-jose not found")
	}
	if got != 3050 {
		t.Errorf("sf→san-jose: want 3050 (run_seconds 760+2110 + 2×dwell 90), got %d", got)
	}

	got, _, _, ok = store.TravelTimeBetween("ca-hsr", "san-jose", "sf")
	if !ok {
		t.Fatal("TravelTimeBetween: san-jose→sf (reverse) not found")
	}
	if got != 3050 {
		t.Errorf("san-jose→sf (reverse): want 3050 (symmetry), got %d", got)
	}

	got, _, _, ok = store.TravelTimeBetween("ca-hsr", "sf", "sf")
	if !ok || got != 0 {
		t.Errorf("sf→sf: want (0, true), got (%d, %v)", got, ok)
	}

	_, _, _, ok = store.TravelTimeBetween("no-such-scenario", "sf", "millbrae")
	if ok {
		t.Error("expected false for unknown scenario slug")
	}

	_, _, _, ok = store.TravelTimeBetween("ca-hsr", "sf", "no-such-station")
	if ok {
		t.Error("expected false for unknown station slug")
	}
}

func TestLocalSFToAnaheim_compiledTime_approx306min(t *testing.T) {
	// Table 3-4, 2026 Business Plan: all-stop SF→Anaheim = 306 min.
	// Compiled Local = run sum 17280 s + 12×90 s dwell = 18360 s = 306.0 min exactly.
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not found")
	}

	const localSvcID = "00000000-0000-4004-8001-000000000002"
	var localSG *ServiceGraph
	for i := range g.Services {
		if g.Services[i].ServiceID == localSvcID {
			localSG = &g.Services[i]
			break
		}
	}
	if localSG == nil {
		t.Fatal("HSR Local service graph not found")
	}

	adj := map[string]int{}
	for _, e := range localSG.Edges {
		adj[e.FromSlug+"→"+e.ToSlug] = e.Seconds
	}

	allStops := []string{
		"sf", "millbrae", "san-jose", "gilroy", "merced", "madera",
		"fresno", "kings-tulare", "bakersfield", "palmdale",
		"burbank-airport", "los-angeles", "anaheim",
	}
	total := 0
	for i := 0; i+1 < len(allStops); i++ {
		key := allStops[i] + "→" + allStops[i+1]
		secs, found := adj[key]
		if !found {
			t.Fatalf("edge %q not in HSR Local graph", key)
		}
		total += secs
	}

	const (
		wantMin = 18240
		wantMax = 18480
	)
	if total < wantMin || total > wantMax {
		t.Errorf("Local SF→Anaheim: got %d s (%d min), want %d–%d s (306 min ±120 s)",
			total, total/60, wantMin, wantMax)
	}
}

// The seeded travel-time set is what feeds the isochrone "Time between
// stations" table, which groups by route. Brightline West run times are only a
// distinct group there if they carry a route id of their own.
func TestSeededTravelTimes_brightlineWestIsADistinctRouteGroup(t *testing.T) {
	store := mustNewStore(t)
	tt, ok := store.GetTravelTimes("ca-hsr")
	if !ok {
		t.Fatal("travel times not found for ca-hsr")
	}

	const (
		phase1RouteID = "00000000-0000-4002-8001-000000000001"
		bwRouteID     = "00000000-0000-4002-8001-000000000002"
	)
	byRoute := map[string][]string{}
	for _, seg := range tt.Segments {
		byRoute[seg.RouteID] = append(byRoute[seg.RouteID], seg.FromSlug+"→"+seg.ToSlug)
	}

	if len(byRoute) != 2 {
		t.Errorf("want segments grouped under 2 routes, got %d: %v", len(byRoute), byRoute)
	}
	if got := len(byRoute[phase1RouteID]); got != 12 {
		t.Errorf("Phase 1 route: want 12 segments, got %d", got)
	}
	want := []string{"palmdale→victor-valley", "victor-valley→las-vegas"}
	got := byRoute[bwRouteID]
	if len(got) != len(want) {
		t.Fatalf("Brightline West route: want segments %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Brightline West segment %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestBrightlineWest_compiledPalmdaleToLasVegas(t *testing.T) {
	// Run-only 1050 + 5310 = 6360 s, plus one 90 s dwell at the intermediate
	// Victor Valley stop = 6450 s (107.5 min) end to end.
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not found")
	}

	const bwSvcID = "00000000-0000-4004-8001-000000000003"
	var bwSG *ServiceGraph
	for i := range g.Services {
		if g.Services[i].ServiceID == bwSvcID {
			bwSG = &g.Services[i]
			break
		}
	}
	if bwSG == nil {
		t.Fatal("Brightline West service graph not found")
	}

	adj := map[string]int{}
	for _, e := range bwSG.Edges {
		adj[e.FromSlug+"→"+e.ToSlug] = e.Seconds
	}

	total := 0
	for _, key := range []string{"palmdale→victor-valley", "victor-valley→las-vegas"} {
		secs, found := adj[key]
		if !found {
			t.Fatalf("edge %q not in Brightline West graph", key)
		}
		total += secs
	}

	const (
		wantMin = 6330
		wantMax = 6570
	)
	if total < wantMin || total > wantMax {
		t.Errorf("Brightline West Palmdale→Las Vegas: got %d s (%d min), want %d–%d s",
			total, total/60, wantMin, wantMax)
	}

	// The spur is a dead end off Palmdale, so it must not shortcut any Phase 1 pair.
	if secs, _, _, ok := store.TravelTimeBetween("ca-hsr", "sf", "las-vegas"); !ok {
		t.Error("sf→las-vegas should be reachable via Palmdale interchange")
	} else if secs <= 6450 {
		t.Errorf("sf→las-vegas: got %d s, want more than the spur alone (6450 s)", secs)
	}
}
