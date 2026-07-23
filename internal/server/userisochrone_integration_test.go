package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// compileUserScenarioAndWait triggers POST /api/user-scenarios/{slug}/compile
// and polls it to completion, failing the test unless it succeeds.
func compileUserScenarioAndWait(t *testing.T, h http.Handler, token, slug string) transit.Job {
	t.Helper()
	rec := request(t, h, http.MethodPost, "/api/user-scenarios/"+slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	final := pollJob(t, h, token, created.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("compile: final status = %q, want succeeded (error: %s)", final.Status, final.Error)
	}
	return final
}

const isoRequestBody = `{"lat":37.0,"lng":-121.8,"budget_mins":90,"mode":"walk"}`

// A user compiles their scenario, then computes an isochrone over it: the
// live analogue of the seeded POST /api/isochrone, but owner-scoped and
// sourced from the compiled graph rather than the embedded store.
func TestIntegration_UserScenarioIsochrone_FreshGraph(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "iso-fresh@example.com", "owner-password")
	stranger := provisionMember(t, h, adminToken, "iso-fresh-stranger@example.com", "stranger-password")

	ingestCompileRoute(t, repo, "iso-fresh-route")
	svc := createUserServiceOverAPI(t, h, owner, "iso-fresh-route", "Line")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner, `{"name":"Trip","service_ids":["`+svc+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// No compile yet: the graph does not exist.
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody); r.Code != http.StatusNotFound {
		t.Fatalf("pre-compile isochrone: status %d, want 404; body %s", r.Code, r.Body.String())
	}

	compileUserScenarioAndWait(t, h, owner, scenario.Slug)

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("isochrone: status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["type"] != "FeatureCollection" {
		t.Errorf("type: want FeatureCollection, got %v", body["type"])
	}

	// A stranger cannot reach it — 404, not 403.
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", stranger, isoRequestBody); r.Code != http.StatusNotFound {
		t.Errorf("stranger isochrone: status %d, want 404", r.Code)
	}

	// Invalid mode still 400s ahead of any graph work.
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner,
		`{"lat":37.0,"lng":-121.8,"budget_mins":90,"mode":"fly"}`); r.Code != http.StatusBadRequest {
		t.Errorf("invalid mode: status %d, want 400", r.Code)
	}
}

// The central SPA-116 acceptance criterion: compile a two-service scenario,
// delete one of the member services, and the isochrone must answer 409 with
// the distinct stale error code rather than 200 with a graph that still
// references the deleted service.
func TestIntegration_UserScenarioIsochrone_DeletedMember_409(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "iso-deleted@example.com", "owner-password")

	ingestCompileRoute(t, repo, "iso-deleted-route")
	svc1 := createUserServiceOverAPI(t, h, owner, "iso-deleted-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, owner, "iso-deleted-route", "Local")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner,
		`{"name":"Trip","service_ids":["`+svc1+`","`+svc2+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	compileUserScenarioAndWait(t, h, owner, scenario.Slug)

	// Fresh immediately after compile.
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody); r.Code != http.StatusOK {
		t.Fatalf("pre-delete isochrone: status %d, want 200; body %s", r.Code, r.Body.String())
	}

	// Delete one member service. The join row cascades away in Postgres; the
	// scenario's updated_at is never touched by this — the exact blind spot
	// SPA-116 fixes.
	svc2Row := getUserServiceByID(t, h, owner, svc2)
	if r := request(t, h, http.MethodDelete, "/api/services/"+svc2Row.Slug, owner); r.Code != http.StatusNoContent {
		t.Fatalf("delete service: status %d, body %s", r.Code, r.Body.String())
	}

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("post-delete isochrone: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code: want %q, got %q", handler.StaleGraphErrorCode, body["code"])
	}

	// The 409 must never leak the stale graph itself.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if _, ok := raw["features"]; ok {
		t.Error("409 response leaks graph features")
	}

	// Recompiling clears the staleness: membership now matches what compiled.
	compileUserScenarioAndWait(t, h, owner, scenario.Slug)
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody); r.Code != http.StatusOK {
		t.Fatalf("post-recompile isochrone: status %d, want 200; body %s", r.Code, r.Body.String())
	}
}

// Removing a member from a scenario without deleting the underlying service
// must also be caught — this path does bump user_scenarios.updated_at, but
// the membership-set comparison catches it uniformly with the deletion case
// rather than depending on that timestamp.
func TestIntegration_UserScenarioIsochrone_RemovedMemberWithoutDeletion_409(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "iso-removed@example.com", "owner-password")

	ingestCompileRoute(t, repo, "iso-removed-route")
	svc1 := createUserServiceOverAPI(t, h, owner, "iso-removed-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, owner, "iso-removed-route", "Local")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner,
		`{"name":"Trip","service_ids":["`+svc1+`","`+svc2+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	compileUserScenarioAndWait(t, h, owner, scenario.Slug)

	// Update membership down to one member; svc2 still exists, just no longer
	// curated into this scenario.
	rec = request(t, h, http.MethodPut, "/api/user-scenarios/"+scenario.Slug, owner,
		`{"name":"Trip","service_ids":["`+svc1+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update scenario: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("isochrone after membership removal: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
}

// Adding a member must also invalidate the compiled graph. The membership-set
// comparison catches this the same way it catches removal, uniformly —
// verified rather than assumed, per the acceptance criteria.
func TestIntegration_UserScenarioIsochrone_AddedMember_409(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "iso-added@example.com", "owner-password")

	ingestCompileRoute(t, repo, "iso-added-route")
	svc1 := createUserServiceOverAPI(t, h, owner, "iso-added-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, owner, "iso-added-route", "Local")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner,
		`{"name":"Trip","service_ids":["`+svc1+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	compileUserScenarioAndWait(t, h, owner, scenario.Slug)

	rec = request(t, h, http.MethodPut, "/api/user-scenarios/"+scenario.Slug, owner,
		`{"name":"Trip","service_ids":["`+svc1+`","`+svc2+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update scenario: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("isochrone after membership addition: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
}

// Deleting a service that anchors a co-located-stop cluster (SPA-109) must
// not let any reference to its now-nonexistent stop slug escape to a client:
// the 409 path returns only the small error envelope, never the stale graph
// whose nodes may be keyed on the deleted anchor.
func TestIntegration_UserScenarioIsochrone_DeletedClusterAnchor_NoStaleSlugLeak(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "iso-anchor@example.com", "owner-password")

	// Both services share the same route and identical stop coordinates, so
	// their stops co-locate into shared clusters at compile time (SPA-109);
	// the cluster key is the lexicographically smallest member stop slug.
	ingestCompileRoute(t, repo, "iso-anchor-route")
	svc1 := createUserServiceOverAPI(t, h, owner, "iso-anchor-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, owner, "iso-anchor-route", "Local")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner,
		`{"name":"Trip","service_ids":["`+svc1+`","`+svc2+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	final := compileUserScenarioAndWait(t, h, owner, scenario.Slug)
	if len(final.Result.Merge.Clusters) == 0 {
		t.Fatalf("expected co-located stops to merge into at least one cluster; merge = %+v", final.Result.Merge)
	}

	// Delete svc1 (Express) — whichever of the two anchors a cluster, its stop
	// slugs stop existing anywhere the moment this succeeds.
	svc1Row := getUserServiceByID(t, h, owner, svc1)
	if r := request(t, h, http.MethodDelete, "/api/services/"+svc1Row.Slug, owner); r.Code != http.StatusNoContent {
		t.Fatalf("delete service: status %d, body %s", r.Code, r.Body.String())
	}

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("isochrone after anchor deletion: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}

	// The response is exactly the error envelope: no nodes, no services, no
	// stop slugs of any kind — stale or otherwise.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, leaky := range []string{"nodes", "services", "features", "reachable_stations", "merge"} {
		if _, ok := raw[leaky]; ok {
			t.Errorf("409 response leaks %q", leaky)
		}
	}
}
