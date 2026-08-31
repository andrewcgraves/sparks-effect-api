package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Exercises owned routes end to end against a real database and the real mux:
// a member authors an alignment of their own, edits its name and description,
// and finds it everywhere it should be and nowhere it should not — while a
// stranger can do none of that.
//
// The containment half is the point. A route with an owner must never reach the
// public picker or the by-slug read, because those are the surfaces an
// anonymous caller sees.

// authedRequest drives the real mux with a bearer token.
func authedRequest(t *testing.T, h http.Handler, token, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ownedAlignment is a two-point line at lat 37 running west to east, the same
// geometry the user-service helpers snap their stops onto.
func ownedAlignment(name, description string) string {
	return `{"type":"LineString","coordinates":[[-121.9,37.0],[-121.3,37.0]],
		"properties":{"name":"` + name + `","description":"` + description + `","mode":"rail"}}`
}

func createOwnedRoute(t *testing.T, h http.Handler, token, name, description string) transit.Route {
	t.Helper()
	rec := authedRequest(t, h, token, http.MethodPost, "/api/me/routes", ownedAlignment(name, description))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating owned route %s: status %d, body %s", name, rec.Code, rec.Body.String())
	}
	var rt transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&rt); err != nil {
		t.Fatalf("decoding created route: %v", err)
	}
	return rt
}

// AC: a member authors a route, owns it, and can change its name and
// description afterwards — the whole point of the feature.
func TestIntegration_OwnedRouteIsAuthoredAndEditableByItsOwner(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	created := createOwnedRoute(t, h, memberToken, "Bay Link", "first draft")
	if created.OwnerID == nil {
		t.Fatal("created route has no owner")
	}
	if created.Slug != "bay-link" {
		t.Errorf("slug = %q, want bay-link", created.Slug)
	}

	// The edit the feature exists for.
	rec := authedRequest(t, h, memberToken, http.MethodPut, "/api/me/routes/bay-link",
		ownedAlignment("Bay Link Extension", "second draft, now longer"))
	if rec.Code != http.StatusOK {
		t.Fatalf("updating owned route: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = authedRequest(t, h, memberToken, http.MethodGet, "/api/me/routes/bay-link", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading owned route back: status %d", rec.Code)
	}
	var reread transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&reread); err != nil {
		t.Fatalf("decoding re-read route: %v", err)
	}
	if reread.Name != "Bay Link Extension" {
		t.Errorf("name = %q, want the updated one", reread.Name)
	}
	if reread.Description != "second draft, now longer" {
		t.Errorf("description = %q, want the updated one", reread.Description)
	}
	// The slug is the address, so renaming must not move it.
	if reread.Slug != "bay-link" {
		t.Errorf("slug = %q, want it unchanged after a rename", reread.Slug)
	}
}

// The containment criterion: an owned route is absent from every public
// surface, and a stranger cannot reach it by guessing its slug.
func TestIntegration_OwnedRouteIsInvisibleToThePublicAndToStrangers(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")
	strangerToken := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password")

	createOwnedRoute(t, h, memberToken, "Bay Link", "private draft")

	// The public picker is curated-only.
	if got := getRouteList(t, h); len(got) != 0 {
		t.Errorf("unauthenticated route list = %+v, want it to exclude the owned route", got)
	}

	// So is the unauthenticated by-slug read.
	if rec := authedRequest(t, h, "", http.MethodGet, "/api/routes/bay-link", ""); rec.Code != http.StatusNotFound {
		t.Errorf("anonymous GET /api/routes/bay-link: status %d, want 404", rec.Code)
	}

	// A stranger holding a valid token fares no better, and gets 404 rather
	// than 403 so they cannot confirm the slug exists.
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/routes/bay-link", ""},
		{http.MethodGet, "/api/me/routes/bay-link", ""},
		{http.MethodPut, "/api/me/routes/bay-link", ownedAlignment("Hijacked", "")},
		{http.MethodDelete, "/api/me/routes/bay-link", ""},
	} {
		rec := authedRequest(t, h, strangerToken, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("stranger %s %s: status %d, want 404", tc.method, tc.target, rec.Code)
		}
	}

	// The owner still sees it in their own list.
	rec := authedRequest(t, h, memberToken, http.MethodGet, "/api/me/routes", "")
	var mine []transit.RouteSummary
	if err := json.NewDecoder(rec.Body).Decode(&mine); err != nil {
		t.Fatalf("decoding my routes: %v", err)
	}
	if len(mine) != 1 || mine[0].Slug != "bay-link" {
		t.Errorf("my routes = %+v, want exactly the owned route", mine)
	}
	if mine[0].Description != "private draft" {
		t.Errorf("summary description = %q, want it carried", mine[0].Description)
	}

	// And the stranger's own list is empty rather than everyone's.
	rec = authedRequest(t, h, strangerToken, http.MethodGet, "/api/me/routes", "")
	var theirs []transit.RouteSummary
	if err := json.NewDecoder(rec.Body).Decode(&theirs); err != nil {
		t.Fatalf("decoding stranger's routes: %v", err)
	}
	if len(theirs) != 0 {
		t.Errorf("stranger's routes = %+v, want empty", theirs)
	}
}

// An admin-ingested alignment stays curated: unowned, and therefore still in
// the public picker. Stamping the admin as its owner would quietly empty
// GET /api/routes, which is what the website derives its scenario list from.
func TestIntegration_AdminIngestedRoutesStayCurated(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)

	rec := authedRequest(t, h, adminToken, http.MethodPost, "/api/admin/routes",
		ownedAlignment("Peninsula Line", "curated"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingesting route: status %d, body %s", rec.Code, rec.Body.String())
	}
	var ingested transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&ingested); err != nil {
		t.Fatalf("decoding ingested route: %v", err)
	}
	if ingested.OwnerID != nil {
		t.Errorf("owner = %v, want an admin-ingested route to stay unowned", ingested.OwnerID)
	}

	if got := getRouteList(t, h); len(got) != 1 {
		t.Errorf("public route list = %+v, want the curated route to still be listed", got)
	}
	// And it is readable anonymously, which is what curated means.
	if rec := authedRequest(t, h, "", http.MethodGet, "/api/routes/peninsula-line", ""); rec.Code != http.StatusOK {
		t.Errorf("anonymous read of a curated route: status %d, want 200", rec.Code)
	}
}

// Deleting a route that a saved service is built on must be refused, not
// cascaded: user_services.route_id is ON DELETE CASCADE, so the alternative is
// silently destroying the service.
func TestIntegration_OwnedRouteDeleteIsRefusedWhileAServiceUsesIt(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	createOwnedRoute(t, h, memberToken, "Bay Link", "")
	createUserServiceOverAPI(t, h, memberToken, "bay-link", "Local")

	rec := authedRequest(t, h, memberToken, http.MethodDelete, "/api/me/routes/bay-link", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting a route in use: status %d, body %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Code   string                  `json:"code"`
		Detail transit.RouteDependents `json:"detail"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding conflict body: %v", err)
	}
	if body.Code != "route_in_use" {
		t.Errorf("code = %q, want route_in_use", body.Code)
	}
	if body.Detail.UserServices != 1 {
		t.Errorf("detail.user_services = %d, want 1", body.Detail.UserServices)
	}

	// The route — and therefore the service built on it — survived.
	if rec := authedRequest(t, h, memberToken, http.MethodGet, "/api/me/routes/bay-link", ""); rec.Code != http.StatusOK {
		t.Errorf("route after a refused delete: status %d, want it to still be there", rec.Code)
	}
}

// A user must not be able to build a service on someone else's private
// alignment — reachable before ownership existed only because no route had an
// owner to check.
func TestIntegration_AServiceCannotBeBuiltOnSomeoneElsesPrivateRoute(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")
	strangerToken := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password")

	createOwnedRoute(t, h, memberToken, "Bay Link", "")

	body := `{
		"route_slug": "bay-link", "name": "Poached",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
	}`
	rec := authedRequest(t, h, strangerToken, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("service on a stranger's private route: status %d, want 422", rec.Code)
	}

	// The owner may of course build on their own.
	rec = authedRequest(t, h, memberToken, http.MethodPost, "/api/services", body)
	if rec.Code != http.StatusCreated {
		t.Errorf("service on the caller's own route: status %d, want 201", rec.Code)
	}
}
