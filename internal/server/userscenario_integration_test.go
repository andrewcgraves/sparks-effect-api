package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func provisionAdminAndLogin(t *testing.T, h http.Handler, repo *postgres.Repo) string {
	t.Helper()
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	token, status := login(t, h, "admin@example.com", "admin-password")
	if status != http.StatusOK {
		t.Fatalf("admin login: status %d", status)
	}
	return token
}

func provisionMember(t *testing.T, h http.Handler, adminToken, email, password string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("provisioning %s: status %d, body %s", email, rec.Code, rec.Body.String())
	}

	token, status := login(t, h, email, password)
	if status != http.StatusOK {
		t.Fatalf("login %s: status %d", email, status)
	}
	return token
}

func createUserServiceOverAPI(t *testing.T, h http.Handler, token, routeSlug, name string) string {
	t.Helper()
	body := `{
		"route_slug": "` + routeSlug + `", "name": "` + name + `",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating service %s: status %d, body %s", name, rec.Code, rec.Body.String())
	}
	var svc transit.UserService
	if err := json.NewDecoder(rec.Body).Decode(&svc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return svc.ID
}

func TestIntegration_UserScenarioAssembleAndReadBack(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	routeID := mustUUID(t)
	if err := repo.CreateRoute(context.Background(), transit.Route{
		ID: routeID, Slug: "usn-int-route", Name: "Route", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	svc1 := createUserServiceOverAPI(t, h, memberToken, "usn-int-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, memberToken, "usn-int-route", "Local")

	body := `{"name":"Weekend Getaway","service_ids":["` + svc1 + `","` + svc2 + `"]}`
	rec := request(t, h, http.MethodPost, "/api/user-scenarios", memberToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var created transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.ServiceIDs) != 2 {
		t.Fatalf("service_ids: want 2, got %v", created.ServiceIDs)
	}

	rec = request(t, h, http.MethodGet, "/api/user-scenarios/"+created.Slug, memberToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read back: status %d", rec.Code)
	}
	var got transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID || len(got.ServiceIDs) != 2 {
		t.Fatalf("read back mismatch: %+v", got)
	}
}

func TestIntegration_UserScenarioOnlyOwnerCanMutate(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	ownerToken := provisionMember(t, h, adminToken, "owner@example.com", "owner-password")
	strangerToken := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password")

	routeID := mustUUID(t)
	if err := repo.CreateRoute(context.Background(), transit.Route{
		ID: routeID, Slug: "usn-int-route-2", Name: "Route", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svc := createUserServiceOverAPI(t, h, ownerToken, "usn-int-route-2", "Owner Service")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", ownerToken,
		`{"name":"Owner Scenario","service_ids":["`+svc+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var created transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A stranger cannot read, update, or delete it.
	if rec := request(t, h, http.MethodGet, "/api/user-scenarios/"+created.Slug, strangerToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("stranger read: status %d, want 404", rec.Code)
	}
	if rec := request(t, h, http.MethodPut, "/api/user-scenarios/"+created.Slug, strangerToken,
		`{"name":"Hijacked","service_ids":[]}`); rec.Code != http.StatusNotFound {
		t.Errorf("stranger update: status %d, want 404", rec.Code)
	}
	if rec := request(t, h, http.MethodDelete, "/api/user-scenarios/"+created.Slug, strangerToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("stranger delete: status %d, want 404", rec.Code)
	}

	// The owner can update membership down to zero and then delete.
	rec = request(t, h, http.MethodPut, "/api/user-scenarios/"+created.Slug, ownerToken,
		`{"name":"Owner Scenario","service_ids":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update: status %d, body %s", rec.Code, rec.Body.String())
	}
	var updated transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(updated.ServiceIDs) != 0 {
		t.Errorf("membership: want empty after update, got %v", updated.ServiceIDs)
	}

	if rec := request(t, h, http.MethodDelete, "/api/user-scenarios/"+created.Slug, ownerToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete: status %d", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/api/user-scenarios/"+created.Slug, ownerToken, ""); rec.Code != http.StatusNotFound {
		t.Errorf("after delete: status %d, want 404", rec.Code)
	}
}

func TestIntegration_UserScenarioCannotCurateAnotherUsersService(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	ownerToken := provisionMember(t, h, adminToken, "owner2@example.com", "owner-password")
	strangerToken := provisionMember(t, h, adminToken, "stranger2@example.com", "stranger-password")

	routeID := mustUUID(t)
	if err := repo.CreateRoute(context.Background(), transit.Route{
		ID: routeID, Slug: "usn-int-route-3", Name: "Route", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	strangersSvc := createUserServiceOverAPI(t, h, strangerToken, "usn-int-route-3", "Stranger Service")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", ownerToken,
		`{"name":"Reach For It","service_ids":["`+strangersSvc+`"]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rec.Code, rec.Body.String())
	}
}
