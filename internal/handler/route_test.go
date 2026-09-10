package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type fakeRouteStore struct {
	routes    map[string]transit.Route
	scenarios map[string]transit.Scenario
	createErr error
	getErr    error
	listErr   error
}

func newFakeRouteStore() *fakeRouteStore {
	return &fakeRouteStore{
		routes: map[string]transit.Route{},
		scenarios: map[string]transit.Scenario{
			"ca-hsr": {ID: "00000000-0000-4001-8001-000000000001", Slug: "ca-hsr", Name: "CA HSR"},
		},
	}
}

func (f *fakeRouteStore) CreateRoute(_ context.Context, rt transit.Route) error {
	if f.createErr != nil {
		return f.createErr
	}
	if _, exists := f.routes[rt.Slug]; exists {
		return fmt.Errorf("duplicate slug %q", rt.Slug)
	}
	f.routes[rt.Slug] = rt
	return nil
}

func (f *fakeRouteStore) GetRouteBySlug(_ context.Context, slug string) (transit.Route, bool, error) {
	if f.getErr != nil {
		return transit.Route{}, false, f.getErr
	}
	rt, ok := f.routes[slug]
	return rt, ok, nil
}

func (f *fakeRouteStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

func (f *fakeRouteStore) ListCuratedRouteSummaries(_ context.Context) ([]transit.RouteSummary, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	slugs := make([]string, 0, len(f.routes))
	for slug := range f.routes {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	out := make([]transit.RouteSummary, 0, len(slugs))
	for _, slug := range slugs {
		rt := f.routes[slug]
		out = append(out, transit.RouteSummary{Slug: rt.Slug, Name: rt.Name, Mode: rt.Mode})
	}
	return out, nil
}

const validRoute = `{
  "type": "LineString",
  "coordinates": [[-122.4, 37.79], [-122.3, 37.70], [-122.2, 37.60]],
  "properties": {
    "name": "Test Alignment",
    "mode": "rail",
    "segments": [
      {"cant_mm": 150, "curve_radius_m": 1200, "grade_pct": 1.2},
      {"cant_mm": 0, "curve_radius_m": 0, "grade_pct": -0.8}
    ]
  }
}`

func TestCreateRouteReturnsASlug(t *testing.T) {
	store := newFakeRouteStore()
	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	var got transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Slug != "test-alignment" {
		t.Errorf("slug = %q, want %q (derived from the name)", got.Slug, "test-alignment")
	}
	if got.ID == "" {
		t.Error("created route has no ID")
	}
	// Routes carry no stops, and an ingested route belongs to no scenario.
	if got.ScenarioID != nil {
		t.Errorf("scenario_id = %v, want nil for a standalone ingested route", *got.ScenarioID)
	}
	// Bidirectional defaults to true when the caller omits it.
	if !got.Bidirectional {
		t.Error("bidirectional should default to true")
	}
}

func TestCreateRoutePersistsGeometryAndPhysics(t *testing.T) {
	store := newFakeRouteStore()
	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	stored, ok, err := store.GetRouteBySlug(context.Background(), "test-alignment")
	if err != nil || !ok {
		t.Fatalf("GetRouteBySlug: ok=%v err=%v", ok, err)
	}

	if stored.Geometry.Type != "LineString" || len(stored.Geometry.Coordinates) != 3 {
		t.Fatalf("geometry did not persist: %+v", stored.Geometry)
	}
	if got, want := stored.Geometry.Coordinates[0], []float64{-122.4, 37.79}; got[0] != want[0] || got[1] != want[1] {
		t.Errorf("first coordinate = %v, want %v", got, want)
	}

	if len(stored.Segments) != 2 {
		t.Fatalf("segments = %d, want 2 for a 3-point route", len(stored.Segments))
	}
	if want := (transit.RouteSegment{CantMM: 150, CurveRadiusM: 1200, GradePct: 1.2}); stored.Segments[0] != want {
		t.Errorf("segment 0 = %+v, want %+v", stored.Segments[0], want)
	}
	if want := (transit.RouteSegment{GradePct: -0.8}); stored.Segments[1] != want {
		t.Errorf("segment 1 = %+v, want %+v", stored.Segments[1], want)
	}
}

func TestCreateRouteRejectsInvalidPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{"type":`, http.StatusBadRequest},
		{"not a linestring", `{"type":"Point","coordinates":[[-122,37]],"properties":{"name":"X"}}`, http.StatusUnprocessableEntity},
		{"too few coordinates", `{"type":"LineString","coordinates":[[-122,37]],"properties":{"name":"X"}}`, http.StatusUnprocessableEntity},
		{"latitude out of range", `{"type":"LineString","coordinates":[[-122,91],[-121,37]],"properties":{"name":"X"}}`, http.StatusUnprocessableEntity},
		{"missing name", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],"properties":{}}`, http.StatusUnprocessableEntity},
		{"segment count mismatch", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","segments":[{"cant_mm":0},{"cant_mm":0}]}}`, http.StatusUnprocessableEntity},
		{"cant out of range", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","segments":[{"cant_mm":9999}]}}`, http.StatusUnprocessableEntity},
		{"curve radius out of range", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","segments":[{"curve_radius_m":1}]}}`, http.StatusUnprocessableEntity},
		{"grade out of range", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","segments":[{"grade_pct":45}]}}`, http.StatusUnprocessableEntity},
		{"name with no sluggable characters", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"!!!"}}`, http.StatusBadRequest},
		{"unknown scenario", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","scenario_slug":"no-such-scenario"}}`, http.StatusBadRequest},
		{"repeated coordinate", `{"type":"LineString","coordinates":[[-122,37],[-122,37]],
			"properties":{"name":"X"}}`, http.StatusUnprocessableEntity},
		// A misspelled physics key must be refused outright, not silently
		// decoded as a zero-valued (tangent, level) segment.
		{"misspelled physics key", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X","segments":[{"cant__mm":150}]}}`, http.StatusBadRequest},
		{"unknown top-level field", `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
			"properties":{"name":"X"},"nonsense":true}`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeRouteStore()
			rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", tc.body)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if len(store.routes) != 0 {
				t.Error("a rejected route must not be persisted")
			}
		})
	}
}

func TestCreateRouteValidationRejectionCarriesStructuredDetail(t *testing.T) {
	body := `{"type":"LineString","coordinates":[[-122,91],[-121,-91]],"properties":{"name":"X"}}`
	rec := postJSON(t, handler.CreateRoute(newFakeRouteStore()), "/api/admin/routes", body)
	got := decodeValidationFault(t, rec)
	if got.Code != handler.ValidationErrorCode {
		t.Errorf("code = %q, want %q", got.Code, handler.ValidationErrorCode)
	}
	if len(got.Detail.Faults) != 2 {
		t.Fatalf("got %d faults, want 2: %+v", len(got.Detail.Faults), got.Detail.Faults)
	}
	if got.Detail.Faults[0].Index == nil || *got.Detail.Faults[0].Index != 0 {
		t.Errorf("fault 0 index = %v, want 0", got.Detail.Faults[0].Index)
	}
	if got.Detail.Faults[1].Index == nil || *got.Detail.Faults[1].Index != 1 {
		t.Errorf("fault 1 index = %v, want 1", got.Detail.Faults[1].Index)
	}
}

func TestCreateRouteAttachesToScenarioBySlug(t *testing.T) {
	store := newFakeRouteStore()
	body := `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
		"properties":{"name":"Attached","scenario_slug":"ca-hsr"}}`
	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	var got transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := store.scenarios["ca-hsr"].ID
	if got.ScenarioID == nil || *got.ScenarioID != want {
		t.Errorf("scenario_id = %v, want %s", got.ScenarioID, want)
	}
}

func TestCreateRouteRejectsDuplicateSlug(t *testing.T) {
	store := newFakeRouteStore()
	if rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute); rec.Code != http.StatusCreated {
		t.Fatalf("first create: status %d", rec.Code)
	}

	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate slug: status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if len(store.routes) != 1 {
		t.Errorf("routes stored = %d, want 1", len(store.routes))
	}
}

func TestCreateRouteHonoursExplicitSlug(t *testing.T) {
	store := newFakeRouteStore()
	body := `{"type":"LineString","coordinates":[[-122,37],[-121,37]],
		"properties":{"name":"Some Long Display Name","slug":"short-slug"}}`
	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	if _, ok, _ := store.GetRouteBySlug(context.Background(), "short-slug"); !ok {
		t.Error("route was not stored under its explicit slug")
	}
}

func TestCreateRouteReportsStorageFailure(t *testing.T) {
	store := newFakeRouteStore()
	store.createErr = errors.New("database is down")

	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The client must not be handed the underlying database error.
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}

func TestCreateRouteAcceptsGeoJSONBBox(t *testing.T) {
	store := newFakeRouteStore()
	body := `{"type":"LineString","bbox":[-122,37,-121,38],
		"coordinates":[[-122,37],[-121,37]],"properties":{"name":"Boxed"}}`
	rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
}

func getRoute(t *testing.T, h http.Handler, slug string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/routes/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouteBySlugReturnsGeometryPhysicsAndMetadata(t *testing.T) {
	store := newFakeRouteStore()
	if rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: status %d", rec.Code)
	}

	rec := getRoute(t, handler.RouteBySlug(store), "test-alignment")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	var got transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Slug != "test-alignment" || got.Name != "Test Alignment" || got.Mode != "rail" {
		t.Errorf("metadata = %+v, want slug/name/mode from the ingested route", got)
	}
	if !got.Bidirectional {
		t.Error("bidirectional should default to true")
	}
	if len(got.Geometry.Coordinates) != 3 {
		t.Fatalf("geometry did not round-trip: %+v", got.Geometry)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(got.Segments))
	}
	if want := (transit.RouteSegment{CantMM: 150, CurveRadiusM: 1200, GradePct: 1.2}); got.Segments[0] != want {
		t.Errorf("segment 0 = %+v, want %+v", got.Segments[0], want)
	}
}

func TestRouteBySlugUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeRouteStore()
	rec := getRoute(t, handler.RouteBySlug(store), "no-such-route")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

func TestRouteBySlugReportsStorageFailure(t *testing.T) {
	store := newFakeRouteStore()
	store.getErr = errors.New("database is down")

	rec := getRoute(t, handler.RouteBySlug(store), "whatever")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}

func listRoutes(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/routes", nil))
	return rec
}

func TestRoutesListsIngestedRoutesForAPicker(t *testing.T) {
	store := newFakeRouteStore()
	if rec := postJSON(t, handler.CreateRoute(store), "/api/admin/routes", validRoute); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: status %d", rec.Code)
	}

	rec := listRoutes(t, handler.Routes(store))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	var got []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("routes = %d, want 1", len(got))
	}
	item := got[0]
	if item["slug"] != "test-alignment" || item["name"] != "Test Alignment" || item["mode"] != "rail" {
		t.Errorf("item = %+v, want slug/name/mode of the ingested route", item)
	}
	for _, absent := range []string{"id", "geometry", "segments", "scenario_id", "bidirectional"} {
		if _, present := item[absent]; present {
			t.Errorf("%q must not be sent in the picker list: %+v", absent, item)
		}
	}
}

func TestRoutesReturnsAnEmptyListWhenNothingIsIngested(t *testing.T) {
	rec := listRoutes(t, handler.Routes(newFakeRouteStore()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

func TestRoutesReportsStorageFailure(t *testing.T) {
	store := newFakeRouteStore()
	store.listErr = errors.New("database is down")

	rec := listRoutes(t, handler.Routes(store))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}
