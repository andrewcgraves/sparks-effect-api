package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_UserServiceReadIncludesRouteSlugAndName(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	token := provisionMember(t, h, adminToken, "route-read@example.com", "member-password")

	geom := [][]float64{{-122, 37}, {-121, 37}}
	for _, rt := range []transit.Route{
		{ID: mustUUID(t), Slug: "read-route-a", Name: "Alignment A", Mode: "rail", Bidirectional: true,
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geom}},
		{ID: mustUUID(t), Slug: "read-route-b", Name: "Alignment B", Mode: "rail", Bidirectional: true,
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geom}},
	} {
		if err := repo.CreateRoute(context.Background(), rt); err != nil {
			t.Fatalf("CreateRoute %s: %v", rt.Slug, err)
		}
	}

	body := func(routeSlug string) string {
		return `{
			"route_slug": "` + routeSlug + `", "name": "Read Line",
			"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
			"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
		}`
	}

	created := decodeUserService(t, request(t, h, http.MethodPost, "/api/services", token,
		body("read-route-a")), http.StatusCreated)
	assertRouteIdentity(t, created, "read-route-a", "Alignment A")
	if created.RouteID == "" {
		t.Fatal("create omitted route_id")
	}

	got := decodeUserService(t, request(t, h, http.MethodGet, "/api/services/"+created.Slug, token), http.StatusOK)
	assertRouteIdentity(t, got, "read-route-a", "Alignment A")
	if got.RouteID != created.RouteID {
		t.Fatalf("GET route_id = %s, want %s", got.RouteID, created.RouteID)
	}

	listRec := request(t, h, http.MethodGet, "/api/services", token)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/services: status %d, body %s", listRec.Code, listRec.Body.String())
	}
	var listed []transit.UserService
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, svc := range listed {
		if svc.ID != created.ID {
			continue
		}
		found = true
		assertRouteIdentity(t, svc, "read-route-a", "Alignment A")
		if svc.RouteID != created.RouteID {
			t.Errorf("list route_id = %s, want %s", svc.RouteID, created.RouteID)
		}
	}
	if !found {
		t.Fatalf("list did not include %s", created.ID)
	}

	updated := decodeUserService(t, request(t, h, http.MethodPut, "/api/services/"+created.Slug, token,
		body("read-route-b")), http.StatusOK)
	assertRouteIdentity(t, updated, "read-route-b", "Alignment B")
	if updated.RouteID == "" || updated.RouteID == created.RouteID {
		t.Fatalf("PUT route_id = %s, want the other route", updated.RouteID)
	}
}

func decodeUserService(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) transit.UserService {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status %d, want %d; body %s", rec.Code, wantStatus, rec.Body.String())
	}
	var svc transit.UserService
	if err := json.NewDecoder(rec.Body).Decode(&svc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return svc
}

func assertRouteIdentity(t *testing.T, svc transit.UserService, slug, name string) {
	t.Helper()
	if svc.RouteSlug != slug || svc.RouteName != name {
		t.Errorf("route slug/name = %q / %q, want %q / %q", svc.RouteSlug, svc.RouteName, slug, name)
	}
}
