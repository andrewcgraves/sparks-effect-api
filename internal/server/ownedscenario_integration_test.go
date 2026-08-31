package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The load-bearing test for the whole feature: a member builds a complete
// scenario of their own — scenario, route, stations, segment times, service —
// compiles it, and plots over it, while the curated ca-hsr baseline is
// untouched and nothing they authored reaches a public surface.
//
// Both halves matter. Without the first, owned scenarios are a name and a
// description; without the second, authoring one publishes it.

// ownedScenarioFixture builds the whole thing and returns the member's token
// and the scenario's slug.
func ownedScenarioFixture(t *testing.T, h http.Handler, token string) string {
	t.Helper()

	rec := authedRequest(t, h, token, http.MethodPost, "/api/me/scenarios",
		`{"name":"Bay Area Rail","description":"my network","status":"draft"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var sc transit.Scenario
	if err := json.NewDecoder(rec.Body).Decode(&sc); err != nil {
		t.Fatalf("decoding scenario: %v", err)
	}

	// A route inside the scenario, running west to east along lat 37.
	rec = authedRequest(t, h, token, http.MethodPost, "/api/me/routes",
		`{"type":"LineString","coordinates":[[-121.9,37.0],[-121.3,37.0]],
		  "properties":{"name":"Bay Spine","scenario_slug":"`+sc.Slug+`","mode":"rail"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating route: status %d, body %s", rec.Code, rec.Body.String())
	}
	var rt transit.Route
	if err := json.NewDecoder(rec.Body).Decode(&rt); err != nil {
		t.Fatalf("decoding route: %v", err)
	}

	// Two stations on that alignment.
	for _, st := range []struct {
		name string
		lng  float64
	}{
		{"West End", -121.9},
		{"East End", -121.3},
	} {
		body := fmt.Sprintf(`{"name":%q,"lat":37.0,"lng":%v,"platform_height":"high"}`, st.name, st.lng)
		rec = authedRequest(t, h, token, http.MethodPost,
			"/api/me/scenarios/"+sc.Slug+"/stations", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("creating station %s: status %d, body %s", st.name, rec.Code, rec.Body.String())
		}
	}

	// The run time between them, which is what gives the compiler a path to
	// place the service's stops on.
	rec = authedRequest(t, h, token, http.MethodPut,
		"/api/me/scenarios/"+sc.Slug+"/travel-times",
		`{"provenance":"authored","source":"me","segments":[
			{"from":"west-end","to":"east-end","run_seconds":600,"route_slug":"`+rt.Slug+`"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("writing travel times: status %d, body %s", rec.Code, rec.Body.String())
	}

	return sc.Slug
}

// createOwnedServiceIn adds a service to an owned scenario, using the shared
// vehicle-type catalog the seed provides.
func createOwnedServiceIn(t *testing.T, h http.Handler, token, scenarioSlug, routeSlug, vehicleTypeID, name string) transit.Service {
	t.Helper()
	body := `{
		"scenario_slug":"` + scenarioSlug + `","route_slug":"` + routeSlug + `",
		"vehicle_type_id":"` + vehicleTypeID + `","name":"` + name + `","direction":"eastbound",
		"stops":[{"station_slug":"west-end","sequence":1},{"station_slug":"east-end","sequence":2}],
		"frequency_windows":[{"start_time":"06:00","end_time":"22:00","headway_s":1800}]
	}`
	rec := authedRequest(t, h, token, http.MethodPost, "/api/me/services", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating service %s: status %d, body %s", name, rec.Code, rec.Body.String())
	}
	var svc transit.Service
	if err := json.NewDecoder(rec.Body).Decode(&svc); err != nil {
		t.Fatalf("decoding service: %v", err)
	}
	return svc
}

// firstVehicleTypeID returns a vehicle type from the shared catalog the ca-hsr
// seed writes. Vehicle types are global and unowned, so any authored service
// may reference one.
func firstVehicleTypeID(t *testing.T, repo interface {
	ListVehicleTypes(context.Context) ([]transit.VehicleType, error)
}) string {
	t.Helper()
	vts, err := repo.ListVehicleTypes(context.Background())
	if err != nil {
		t.Fatalf("ListVehicleTypes: %v", err)
	}
	if len(vts) == 0 {
		t.Fatal("no vehicle types seeded; the fixture needs one to author a service")
	}
	return vts[0].ID
}

// AC: an owned scenario is a real, compilable thing — not a name and a
// description.
func TestIntegration_OwnedScenarioCompilesForItsOwner(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	if _, err := transit.SeedIfEmpty(context.Background(), repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	slug := ownedScenarioFixture(t, h, memberToken)
	createOwnedServiceIn(t, h, memberToken, slug, "bay-spine",
		firstVehicleTypeID(t, repo), "Bay Local")

	rec := authedRequest(t, h, memberToken, http.MethodPost, "/api/scenarios/"+slug+"/compile", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("compiling owned scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var job transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decoding job: %v", err)
	}

	final := pollJob(t, h, memberToken, job.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("compile status = %s, error %q; want succeeded", final.Status, final.Error)
	}
	if final.Result == nil || len(final.Result.Nodes) != 2 {
		t.Fatalf("compiled graph = %+v, want the scenario's two stations as nodes", final.Result)
	}
}

// The containment half: nothing the member authored reaches a public surface,
// and a stranger cannot reach it either.
func TestIntegration_OwnedScenarioIsInvisibleToThePublicAndToStrangers(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")
	strangerToken := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password")

	if _, err := transit.SeedIfEmpty(context.Background(), repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	slug := ownedScenarioFixture(t, h, memberToken)

	// The public scenario collection still offers exactly the curated baseline.
	rec := authedRequest(t, h, "", http.MethodGet, "/api/scenarios", "")
	var public []transit.Scenario
	if err := json.NewDecoder(rec.Body).Decode(&public); err != nil {
		t.Fatalf("decoding public scenarios: %v", err)
	}
	for _, sc := range public {
		if sc.Slug == slug {
			t.Fatalf("the owned scenario %q is listed publicly", slug)
		}
	}

	// And every public read of it 404s, anonymously and as a stranger alike.
	for _, target := range []string{
		"/api/scenarios/" + slug,
		"/api/scenarios/" + slug + "/routes",
		"/api/scenarios/" + slug + "/stations",
		"/api/scenarios/" + slug + "/services",
		"/api/scenarios/" + slug + "/graph",
	} {
		if rec := authedRequest(t, h, "", http.MethodGet, target, ""); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous GET %s: status %d, want 404", target, rec.Code)
		}
	}

	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/me/scenarios/" + slug, ""},
		{http.MethodPut, "/api/me/scenarios/" + slug, `{"name":"Hijacked"}`},
		{http.MethodDelete, "/api/me/scenarios/" + slug, ""},
		{http.MethodGet, "/api/me/scenarios/" + slug + "/stations", ""},
		{http.MethodPost, "/api/scenarios/" + slug + "/compile", ""},
		{http.MethodGet, "/api/scenarios/" + slug + "/graph", ""},
	} {
		rec := authedRequest(t, h, strangerToken, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("stranger %s %s: status %d, want 404", tc.method, tc.target, rec.Code)
		}
	}
}

// The curated baseline must be entirely unaffected by anything a member
// authors. This is the property the ownership filter exists for, and the one
// that would fail silently rather than loudly.
func TestIntegration_AuthoringAnOwnedScenarioLeavesTheCuratedBaselineAlone(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	memberToken := provisionMember(t, h, adminToken, "member@example.com", "member-password")

	// Seed *and* compile, so the database is in the state a booted deployment
	// is actually in. Without the compile the assertion below would be reading
	// ca-hsr's first compile rather than a drift-triggered recompile.
	if _, err := transit.SeedIfEmpty(context.Background(), repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if _, err := transit.CompileSeededIfNeeded(context.Background(), repo,
		transit.DefaultBoardingWaitPolicy()); err != nil {
		t.Fatalf("CompileSeededIfNeeded (initial): %v", err)
	}

	before, err := repo.ListCuratedScenarios(context.Background())
	if err != nil {
		t.Fatalf("ListCuratedScenarios: %v", err)
	}

	slug := ownedScenarioFixture(t, h, memberToken)
	createOwnedServiceIn(t, h, memberToken, slug, "bay-spine",
		firstVehicleTypeID(t, repo), "Bay Local")

	// The curated list is unchanged: the member's scenario is not in it.
	after, err := repo.ListCuratedScenarios(context.Background())
	if err != nil {
		t.Fatalf("ListCuratedScenarios: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("curated scenarios went from %d to %d; authoring must not change it", len(before), len(after))
	}

	// The boot-time store compiles only curated scenarios, so it must not have
	// picked up the member's — and must still build at all, which is the
	// failure mode a malformed owned scenario would otherwise cause.
	store, err := transit.LoadStore(context.Background(), repo, transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("LoadStore after a member authored a scenario: %v", err)
	}
	if _, ok := store.GetScenarioBySlug(slug); ok {
		t.Errorf("the owned scenario %q reached the compiled public store", slug)
	}

	// And a later boot is still not a recompile: an owned scenario must not make
	// CompileSeededIfNeeded think the curated graph has drifted. Without the
	// ownership filter this would recompile ca-hsr on every boot forever, and
	// invalidate the isochrone cache with it.
	compiled, err := transit.CompileSeededIfNeeded(context.Background(), repo, transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	if compiled != 0 {
		t.Errorf("CompileSeededIfNeeded recompiled %d scenario(s); authoring must not cause drift", compiled)
	}
}
