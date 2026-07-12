package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func mustNewStore(t *testing.T) *transit.Store {
	t.Helper()
	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("transit.NewStore: %v", err)
	}
	return store
}

func TestScenarios_list(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios", nil)
	rec := httptest.NewRecorder()

	Scenarios(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected at least one scenario")
	}

	found := false
	for _, sc := range body {
		if sc["slug"] == "ca-hsr" {
			found = true
			if sc["name"] == nil || sc["name"] == "" {
				t.Error("ca-hsr scenario missing name")
			}
			if sc["status"] == nil || sc["status"] == "" {
				t.Error("ca-hsr scenario missing status")
			}
		}
	}
	if !found {
		t.Error("ca-hsr scenario not in list")
	}
}

func TestScenarioBySlug_found(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()

	ScenarioBySlug(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body["slug"] != "ca-hsr" {
		t.Errorf("expected slug ca-hsr, got %v", body["slug"])
	}

	routes, ok := body["routes"].([]any)
	if !ok || len(routes) == 0 {
		t.Error("expected non-empty routes")
	}

	stations, ok := body["stations"].([]any)
	if !ok || len(stations) < 13 {
		t.Errorf("expected at least 13 Phase 1 stations, got %d", len(stations))
	}

	services, ok := body["services"].([]any)
	if !ok || len(services) < 2 {
		t.Errorf("expected at least 2 services, got %d", len(services))
	}

	for _, rawSvc := range services {
		svc, ok := rawSvc.(map[string]any)
		if !ok {
			t.Fatal("service is not an object")
		}
		vt, ok := svc["vehicle_type"].(map[string]any)
		if !ok {
			t.Errorf("service %v missing vehicle_type", svc["name"])
		} else if vt["propulsion"] == nil {
			t.Errorf("vehicle_type for %v missing propulsion", svc["name"])
		}
	}
}

func TestScenarioBySlug_notFound(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/does-not-exist", nil)
	req.SetPathValue("slug", "does-not-exist")
	rec := httptest.NewRecorder()

	ScenarioBySlug(store)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestScenarioBySlug_notFound_contentType(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/does-not-exist", nil)
	req.SetPathValue("slug", "does-not-exist")
	rec := httptest.NewRecorder()

	ScenarioBySlug(store)(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error field")
	}
}

func TestScenarioRoutes_found(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/routes", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()

	ScenarioRoutes(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var routes []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least one route")
	}
	for _, r := range routes {
		geom, ok := r["geometry"].(map[string]any)
		if !ok {
			t.Errorf("route %v missing geometry", r["name"])
			continue
		}
		if geom["type"] != "LineString" {
			t.Errorf("route geometry type: want LineString, got %v", geom["type"])
		}
		coords, ok := geom["coordinates"].([]any)
		if !ok || len(coords) < 2 {
			t.Errorf("route %v geometry has too few coordinates", r["name"])
		}
	}
}

func TestScenarioRoutes_notFound(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/routes", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioRoutes(store)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestScenarioRoutes_notFound_contentType(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/routes", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioRoutes(store)(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error field")
	}
}

func TestScenarioServices_found(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/services", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()

	ScenarioServices(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var services []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &services); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(services) < 2 {
		t.Fatalf("expected at least 2 services, got %d", len(services))
	}
	for _, svc := range services {
		if svc["id"] == nil || svc["id"] == "" {
			t.Error("service missing id")
		}
		stops, ok := svc["stops"].([]any)
		if !ok || len(stops) == 0 {
			t.Errorf("service %v has no stops", svc["name"])
		}
		if svc["active"] != true {
			t.Errorf("service %v has active != true", svc["name"])
		}
	}
}

func TestScenarioServices_notFound(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/services", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioServices(store)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestScenarioServices_notFound_contentType(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/services", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioServices(store)(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error field")
	}
}

func TestScenarioStations_found(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/stations", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()

	ScenarioStations(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var stations []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stations); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(stations) < 13 {
		t.Fatalf("expected at least 13 Phase 1 stations, got %d", len(stations))
	}
	for _, st := range stations {
		if st["slug"] == nil || st["slug"] == "" {
			t.Errorf("station missing slug: %v", st["id"])
		}
		loc, ok := st["location"].(map[string]any)
		if !ok || loc["type"] != "Point" {
			t.Errorf("station %v location.type: want Point, got %v", st["slug"], loc["type"])
		}
	}
}

func TestScenarioStations_notFound(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/stations", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioStations(store)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
}

func TestScenarioTravelTimes_found(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/travel-times", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()

	ScenarioTravelTimes(store)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["scenario_slug"] != "ca-hsr" {
		t.Errorf("scenario_slug: want ca-hsr, got %v", body["scenario_slug"])
	}
	segments, ok := body["segments"].([]any)
	if !ok || len(segments) == 0 {
		t.Error("expected non-empty segments")
	}
}

func TestScenarioTravelTimes_notFound(t *testing.T) {
	store := mustNewStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/nope/travel-times", nil)
	req.SetPathValue("slug", "nope")
	rec := httptest.NewRecorder()

	ScenarioTravelTimes(store)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "scenario not found" {
		t.Errorf("error: want 'scenario not found', got %q", body["error"])
	}
}
