package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_UserServiceProseRoundTrip(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	token := provisionMember(t, h, adminToken, "prose@example.com", "member-password")
	ingestCompileRoute(t, repo, "prose-route")

	body := func(subtext, description string) string {
		return `{
			"route_slug": "prose-route", "name": "Prose Line",
			"subtext": "` + subtext + `", "description": "` + description + `",
			"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
			"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
		}`
	}
	read := func(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) transit.UserService {
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

	created := read(t, request(t, h, http.MethodPost, "/api/services", token,
		body("Electrified · High-speed rail", "The first draft.")), http.StatusCreated)

	got := read(t, request(t, h, http.MethodGet, "/api/services/"+created.Slug, token), http.StatusOK)
	if got.Subtext != "Electrified · High-speed rail" || got.Description != "The first draft." {
		t.Fatalf("read after create: prose = %q / %q", got.Subtext, got.Description)
	}

	read(t, request(t, h, http.MethodPut, "/api/services/"+created.Slug, token,
		body("Diesel · Regional", "The second draft.")), http.StatusOK)

	got = read(t, request(t, h, http.MethodGet, "/api/services/"+created.Slug, token), http.StatusOK)
	if got.Subtext != "Diesel · Regional" || got.Description != "The second draft." {
		t.Fatalf("read after update: prose = %q / %q", got.Subtext, got.Description)
	}
}
