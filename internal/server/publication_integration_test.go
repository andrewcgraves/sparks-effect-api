package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_PublishFreezesSnapshotAndDraftReadStaysLive(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "publisher@example.com", "member-password")
	stranger := provisionMember(t, h, adminToken, "stranger-pub@example.com", "member-password")

	routeA := mustUUID(t)
	routeB := mustUUID(t)
	geomA := [][]float64{{-122, 37}, {-121, 37}}
	geomB := [][]float64{{-120, 36}, {-119, 36}}
	for _, rt := range []transit.Route{
		{ID: routeA, Slug: "pub-route-a", Name: "Alignment A", Mode: "rail", Bidirectional: true,
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geomA}},
		{ID: routeB, Slug: "pub-route-b", Name: "Alignment B", Mode: "rail", Bidirectional: true,
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geomB}},
	} {
		if err := repo.CreateRoute(ctx, rt); err != nil {
			t.Fatalf("CreateRoute %s: %v", rt.Slug, err)
		}
	}

	body := func(routeSlug, name, subtext, description string) string {
		return `{
			"route_slug": "` + routeSlug + `", "name": "` + name + `",
			"subtext": "` + subtext + `", "description": "` + description + `",
			"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
			"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
		}`
	}
	rec := request(t, h, http.MethodPost, "/api/services", owner,
		body("pub-route-a", "Published Line", "Electrified · Express", "The draft."))
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
	// The graph names alignment B. The draft still points at A, which is what
	// a publication must not copy.
	if err := repo.CompleteJob(ctx, jobID, transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: created.ID, WaitPolicy: string(transit.BoardingWaitNone),
			Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: routeB}},
		}},
	}, []string{created.ID}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	rec = request(t, h, http.MethodPut, "/api/services/"+created.Slug+"/publication", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT publication: status %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "publish_failed") {
		t.Fatalf("publish body contains publish_failed: %s", rec.Body.String())
	}
	var published transit.ServicePublication
	if err := json.Unmarshal(rec.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode publication: %v", err)
	}
	if published.CompileJobID != jobID || published.Name != "Published Line" ||
		published.Subtext != "Electrified · Express" || published.Description != "The draft." {
		t.Fatalf("publication = %+v", published)
	}
	if len(published.Routes) != 1 || published.Routes[0].ID != routeB {
		t.Fatalf("routes = %+v, want edge route %s", published.Routes, routeB)
	}
	if !floatCoordsEqual(published.Routes[0].Geometry.Coordinates, geomB) {
		t.Fatalf("route geometry = %v, want %v", published.Routes[0].Geometry.Coordinates, geomB)
	}

	if rec := request(t, h, http.MethodPut, "/api/services/"+created.Slug+"/publication", stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger publish: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}

	stored, found, err := repo.GetUserServiceByID(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("reload draft: found=%v err=%v", found, err)
	}
	stored.Name = "Edited draft"
	stored.Subtext = "Edited subtext"
	stored.Description = "Edited description"
	stored.RouteID = routeB
	if err := repo.UpdateUserService(ctx, stored); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	liveB, found, err := repo.GetRouteBySlug(ctx, "pub-route-b")
	if err != nil || !found {
		t.Fatalf("GetRouteBySlug: found=%v err=%v", found, err)
	}
	liveB.Geometry.Coordinates = [][]float64{{-10, 2}, {-9, 2}}
	if err := repo.UpdateRoute(ctx, liveB); err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}

	rec = request(t, h, http.MethodGet, "/api/services/"+created.Slug, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET draft: status %d, body %s", rec.Code, rec.Body.String())
	}
	var draft transit.UserService
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Name != "Edited draft" || draft.Subtext != "Edited subtext" || draft.Description != "Edited description" {
		t.Fatalf("GET draft returned the snapshot prose: %+v", draft)
	}
	if draft.RouteID != routeB {
		t.Fatalf("GET draft route_id = %s, want the re-pointed draft %s", draft.RouteID, routeB)
	}

	frozen, found, err := repo.GetServicePublication(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("GetServicePublication: found=%v err=%v", found, err)
	}
	if frozen.Name != "Published Line" || frozen.Subtext != "Electrified · Express" || frozen.Description != "The draft." {
		t.Fatalf("frozen prose = %q / %q / %q", frozen.Name, frozen.Subtext, frozen.Description)
	}
	if len(frozen.Routes) != 1 || !floatCoordsEqual(frozen.Routes[0].Geometry.Coordinates, geomB) {
		t.Fatalf("frozen geometry = %+v, want %v", frozen.Routes, geomB)
	}

	rec = request(t, h, http.MethodGet, "/api/services/"+created.Slug+"/publication", owner)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET publication: status %d, want 405; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "application/json") ||
		strings.Contains(rec.Body.String(), "compile_job_id") {
		t.Fatalf("GET publication returned a handler body: %s", rec.Body.String())
	}

	rec = request(t, h, http.MethodPut, "/api/services/"+created.Slug+"/publication", owner)
	if rec.Code != http.StatusConflict {
		t.Fatalf("republish of a stale draft: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var stale map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode stale: %v", err)
	}
	if stale["code"] != "stale_graph" || strings.Contains(rec.Body.String(), "publish_failed") {
		t.Fatalf("stale body = %s", rec.Body.String())
	}

	kept, found, err := repo.GetServicePublication(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("publication after stale republish: found=%v err=%v", found, err)
	}
	if kept.CompileJobID != frozen.CompileJobID || kept.Name != frozen.Name ||
		kept.Subtext != frozen.Subtext || kept.Description != frozen.Description ||
		!kept.PublishedAt.Equal(frozen.PublishedAt) {
		t.Fatalf("stale republish replaced the snapshot: got %+v, want %+v", kept, frozen)
	}
	if len(kept.Routes) != len(frozen.Routes) {
		t.Fatalf("stale republish routes = %+v, want %+v", kept.Routes, frozen.Routes)
	}
	for i := range frozen.Routes {
		if kept.Routes[i].ID != frozen.Routes[i].ID ||
			!floatCoordsEqual(kept.Routes[i].Geometry.Coordinates, frozen.Routes[i].Geometry.Coordinates) {
			t.Fatalf("stale republish route %d = %+v, want %+v", i, kept.Routes[i], frozen.Routes[i])
		}
	}

	if rec := request(t, h, http.MethodDelete, "/api/services/"+created.Slug+"/publication", owner); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}
	if _, found, err := repo.GetServicePublication(ctx, created.ID); err != nil || found {
		t.Fatalf("publication after unpublish: found=%v err=%v", found, err)
	}
	if rec := request(t, h, http.MethodDelete, "/api/services/"+created.Slug+"/publication", owner); rec.Code != http.StatusNoContent {
		t.Fatalf("second unpublish: status %d, want 204; body %s", rec.Code, rec.Body.String())
	}

	freshJob := mustUUID(t)
	if err := repo.CreateJob(ctx, transit.Job{
		ID: freshJob, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
		UserServiceID: &created.ID, OwnerID: &created.OwnerID,
	}); err != nil {
		t.Fatalf("CreateJob fresh: %v", err)
	}
	if err := repo.CompleteJob(ctx, freshJob, transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: created.ID, WaitPolicy: string(transit.BoardingWaitNone),
			Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: routeB}},
		}},
	}, []string{created.ID}); err != nil {
		t.Fatalf("CompleteJob fresh: %v", err)
	}
	rec = request(t, h, http.MethodPut, "/api/services/"+created.Slug+"/publication", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("republish: status %d, body %s", rec.Code, rec.Body.String())
	}
	var again transit.ServicePublication
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatalf("decode republish: %v", err)
	}
	if again.CompileJobID != freshJob || again.Name != "Edited draft" || again.Subtext != "Edited subtext" {
		t.Fatalf("republish snapshot = %+v", again)
	}
	if !again.PublishedAt.After(published.PublishedAt) {
		t.Fatalf("republish published_at = %s, want after %s", again.PublishedAt, published.PublishedAt)
	}

	// The live route was mutated after the first publish. The republish copies
	// whatever the routes table holds now; the first snapshot is gone with the
	// unpublish. GET draft still returns the edited prose, not a frozen one.
	rec = request(t, h, http.MethodGet, "/api/services/"+created.Slug, owner)
	var stillDraft transit.UserService
	if rec.Code != http.StatusOK {
		t.Fatalf("GET draft after republish: status %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stillDraft); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stillDraft.Name != "Edited draft" {
		t.Fatalf("draft name = %q, want the live prose", stillDraft.Name)
	}
}

func floatCoordsEqual(a, b [][]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
