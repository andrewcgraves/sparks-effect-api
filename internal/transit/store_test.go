package transit

import (
	"testing"
)

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
	store, _ := NewStore()
	scenarios := store.GetScenarios()
	if len(scenarios) == 0 {
		t.Fatal("expected at least one scenario")
	}
}

func TestGetScenarioBySlug_found(t *testing.T) {
	store, _ := NewStore()
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
	store, _ := NewStore()
	_, ok := store.GetScenarioBySlug("does-not-exist")
	if ok {
		t.Error("expected not found for unknown slug")
	}
}

func TestGetRoutesByScenario(t *testing.T) {
	store, _ := NewStore()
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	routes := store.GetRoutesByScenario(sc.ID)

	if len(routes) < 2 {
		t.Fatalf("expected at least 2 routes (Phase 1 + Desert Xpress), got %d", len(routes))
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
	store, _ := NewStore()
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	stations := store.GetStationsByScenario(sc.ID)

	if len(stations) != 15 {
		t.Errorf("expected 15 stations, got %d", len(stations))
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
		"fresno", "kings-tulare", "bakersfield", "palmdale", "victor-valley",
		"burbank-airport", "los-angeles", "anaheim", "las-vegas"}
	for _, slug := range required {
		if !slugsSeen[slug] {
			t.Errorf("missing required station slug %q", slug)
		}
	}
}

func TestGetServicesByScenario(t *testing.T) {
	store, _ := NewStore()
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	services := store.GetServicesByScenario(sc.ID)

	if len(services) < 3 {
		t.Fatalf("expected at least 3 services, got %d", len(services))
	}

	for _, svc := range services {
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
	store, _ := NewStore()
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
	store, _ := NewStore()

	tt, ok := store.GetTravelTimes("ca-hsr")
	if !ok {
		t.Fatal("travel times not found for ca-hsr")
	}
	if tt.ScenarioSlug != "ca-hsr" {
		t.Errorf("scenario_slug: want ca-hsr, got %s", tt.ScenarioSlug)
	}
	if len(tt.MinutesMatrix) == 0 {
		t.Error("minutes_matrix is empty")
	}

	sfRow, ok := tt.MinutesMatrix["sf"]
	if !ok {
		t.Fatal("sf row missing from minutes_matrix")
	}
	if sfRow["millbrae"] != 15 {
		t.Errorf("sf→millbrae: want 15, got %d", sfRow["millbrae"])
	}
	if sfRow["los-angeles"] != 269 {
		t.Errorf("sf→los-angeles: want 269, got %d", sfRow["los-angeles"])
	}

	_, ok = store.GetTravelTimes("no-such-scenario")
	if ok {
		t.Error("expected false for unknown scenario slug")
	}
}
