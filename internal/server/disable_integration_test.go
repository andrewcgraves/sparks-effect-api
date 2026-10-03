package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_DisabledAccountLosesSignInAndKeepsPublication(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)

	const (
		email    = "publisher@example.com"
		password = "member-password"
	)
	owner := provisionMember(t, h, adminToken, email, password)

	routeID := mustUUID(t)
	geom := [][]float64{{-122, 37}, {-121, 37}}
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, Slug: "disable-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geom},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	body := `{
		"route_slug": "disable-route", "name": "Still Published",
		"subtext": "Electrified · Express", "description": "The draft.",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
	}`
	rec := request(t, h, http.MethodPost, "/api/services", owner, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var created transit.UserService
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	jobID := mustUUID(t)
	if err := repo.CreateJob(ctx, transit.Job{
		ID: jobID, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
		UserServiceID: &created.ID, OwnerID: &created.OwnerID,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := repo.CompleteJob(ctx, jobID, transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: created.ID, WaitPolicy: string(transit.BoardingWaitNone),
			Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: routeID}},
		}},
	}, []string{created.ID}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	rec = request(t, h, http.MethodPut, "/api/services/"+created.Slug+"/publication", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT publication: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = request(t, h, http.MethodGet, "/api/services/"+created.Slug+"/publication", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET publication: status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	published := rec.Body.String()
	if !strings.Contains(published, "Still Published") {
		t.Fatalf("publication body missing the service name: %s", published)
	}

	member, found, err := repo.GetUserByEmail(ctx, email)
	if err != nil || !found {
		t.Fatalf("GetUserByEmail: found=%v err=%v", found, err)
	}
	if err := repo.SetUserDisabled(ctx, member.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	if rec := request(t, h, http.MethodGet, "/api/auth/me", owner); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled bearer on /api/auth/me: status %d, want 401; body %s", rec.Code, rec.Body.String())
	}

	disabledLogin := postLogin(t, h, email, password)
	unknownLogin := postLogin(t, h, "nobody@example.com", password)
	if disabledLogin.Code != http.StatusUnauthorized || unknownLogin.Code != http.StatusUnauthorized {
		t.Fatalf("login status: disabled %d, unknown %d, want 401 both", disabledLogin.Code, unknownLogin.Code)
	}
	if disabledLogin.Body.String() != unknownLogin.Body.String() {
		t.Fatalf("login body: disabled %q, unknown %q", disabledLogin.Body.String(), unknownLogin.Body.String())
	}
	if !strings.Contains(disabledLogin.Body.String(), "invalid email or password") {
		t.Fatalf("login body = %q, want the unknown-email error", disabledLogin.Body.String())
	}

	rec = request(t, h, http.MethodGet, "/api/services/"+created.Slug+"/publication", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET publication after disable: status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != published {
		t.Fatalf("publication body changed after disable:\n  before %s\n  after  %s", published, rec.Body.String())
	}

	kept, found, err := repo.GetUserServiceByID(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID after disable: found=%v err=%v", found, err)
	}
	if kept.OwnerID != member.ID || kept.Name != "Still Published" {
		t.Fatalf("authored service = owner %s name %q", kept.OwnerID, kept.Name)
	}

	if err := repo.SetUserDisabled(ctx, member.ID, false); err != nil {
		t.Fatalf("SetUserDisabled re-enable: %v", err)
	}
	token, status := login(t, h, email, password)
	if status != http.StatusOK {
		t.Fatalf("login after re-enable: status %d, want 200", status)
	}
	rec = request(t, h, http.MethodGet, "/api/auth/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/auth/me after re-enable: status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func postLogin(t *testing.T, h http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
