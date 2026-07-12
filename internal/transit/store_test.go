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

	if len(routes) != 1 {
		t.Fatalf("expected 1 active route (Phase 1; Brightline West deferred), got %d", len(routes))
	}

	for _, r := range routes {
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

	if len(stations) != 13 {
		t.Errorf("expected 13 Phase 1 stations (Brightline West deferred), got %d", len(stations))
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
		"burbank-airport", "los-angeles", "anaheim"}
	for _, slug := range required {
		if !slugsSeen[slug] {
			t.Errorf("missing required station slug %q", slug)
		}
	}
	for _, deferred := range []string{"victor-valley", "las-vegas"} {
		if slugsSeen[deferred] {
			t.Errorf("deferred Brightline West station %q should not be loaded", deferred)
		}
	}
}

func TestGetServicesByScenario(t *testing.T) {
	store := mustNewStore(t)
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	services := store.GetServicesByScenario(sc.ID)

	if len(services) != 2 {
		t.Fatalf("expected 2 active services (Express + Local; Brightline West deferred), got %d", len(services))
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
			if seg.Minutes != 15 {
				t.Errorf("sf→millbrae: want 15, got %d", seg.Minutes)
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

func TestTravelTimeBetween(t *testing.T) {
	store := mustNewStore(t)

	got, ok := store.TravelTimeBetween("ca-hsr", "sf", "millbrae")
	if !ok {
		t.Fatal("TravelTimeBetween: sf→millbrae not found")
	}
	if got != 15 {
		t.Errorf("sf→millbrae: want 15, got %d", got)
	}

	got, ok = store.TravelTimeBetween("ca-hsr", "sf", "san-jose")
	if !ok {
		t.Fatal("TravelTimeBetween: sf→san-jose not found")
	}
	if got != 54 {
		t.Errorf("sf→san-jose: want 54 (15+39), got %d", got)
	}

	got, ok = store.TravelTimeBetween("ca-hsr", "millbrae", "sf")
	if !ok {
		t.Fatal("TravelTimeBetween: millbrae→sf (reverse) not found")
	}
	if got != 15 {
		t.Errorf("millbrae→sf (reverse): want 15, got %d", got)
	}

	got, ok = store.TravelTimeBetween("ca-hsr", "sf", "sf")
	if !ok || got != 0 {
		t.Errorf("sf→sf: want (0, true), got (%d, %v)", got, ok)
	}

	_, ok = store.TravelTimeBetween("no-such-scenario", "sf", "millbrae")
	if ok {
		t.Error("expected false for unknown scenario slug")
	}

	_, ok = store.TravelTimeBetween("ca-hsr", "sf", "no-such-station")
	if ok {
		t.Error("expected false for unknown station slug")
	}
}
