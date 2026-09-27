package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_PublishedServicesIndexListsOnlyPublications(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "indexer@example.com", "member-password")

	routeID := mustUUID(t)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, Slug: "index-route", Name: "Index Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	create := func(name, subtext, description string) transit.UserService {
		t.Helper()
		rec := request(t, h, http.MethodPost, "/api/services", owner, `{
			"route_slug": "index-route", "name": "`+name+`",
			"subtext": "`+subtext+`", "description": "`+description+`",
			"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
			"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
		}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status %d, body %s", name, rec.Code, rec.Body.String())
		}
		var svc transit.UserService
		if err := json.Unmarshal(rec.Body.Bytes(), &svc); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return svc
	}
	compile := func(svc transit.UserService) {
		t.Helper()
		jobID := mustUUID(t)
		if err := repo.CreateJob(ctx, transit.Job{
			ID: jobID, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
			UserServiceID: &svc.ID, OwnerID: &svc.OwnerID,
		}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		if err := repo.CompleteJob(ctx, jobID, transit.TransitGraph{
			Services: []transit.ServiceGraph{{
				ServiceID: svc.ID, WaitPolicy: string(transit.BoardingWaitNone),
				Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: routeID}},
			}},
		}, []string{svc.ID}); err != nil {
			t.Fatalf("CompleteJob: %v", err)
		}
	}
	publish := func(svc transit.UserService) {
		t.Helper()
		if rec := request(t, h, http.MethodPut, "/api/services/"+svc.Slug+"/publication", owner); rec.Code != http.StatusOK {
			t.Fatalf("publish %s: status %d, body %s", svc.Slug, rec.Code, rec.Body.String())
		}
	}
	index := func(token string) (string, []map[string]any) {
		t.Helper()
		rec := request(t, h, http.MethodGet, "/api/published-services", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/published-services: status %d, body %s", rec.Code, rec.Body.String())
		}
		var items []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode index: %v", err)
		}
		return rec.Body.String(), items
	}
	slugs := func(items []map[string]any) []any {
		out := make([]any, 0, len(items))
		for _, it := range items {
			out = append(out, it["slug"])
		}
		return out
	}

	first := create("First Line", "Electrified · Express", "Published first.")
	second := create("Second Line", "Diesel", "Published second.")
	draft := create("Draft Line", "Never published", "Compiled, never published.")
	for _, svc := range []transit.UserService{first, second, draft} {
		compile(svc)
	}
	publish(first)

	anonBody, items := index("")
	if len(items) != 1 {
		t.Fatalf("index = %v, want only %s", slugs(items), first.Slug)
	}
	want := map[string]any{
		"slug": first.Slug, "name": "First Line",
		"subtext": "Electrified · Express", "description": "Published first.",
	}
	if len(items[0]) != len(want) {
		t.Fatalf("item = %+v, want exactly the keys of %+v", items[0], want)
	}
	for k, v := range want {
		if items[0][k] != v {
			t.Errorf("%s = %v, want %v", k, items[0][k], v)
		}
	}

	// The owner of an unpublished draft does not see it listed: the index is
	// the same for everyone who asks.
	if ownerBody, _ := index(owner); ownerBody != anonBody {
		t.Fatalf("owner's index = %s, want the anonymous %s", ownerBody, anonBody)
	}

	// Most recently published first.
	publish(second)
	if _, items := index(""); len(items) != 2 || items[0]["slug"] != second.Slug || items[1]["slug"] != first.Slug {
		t.Fatalf("index = %v, want [%s %s]", slugs(items), second.Slug, first.Slug)
	}

	// A draft edit does not reach the index until it is republished.
	edited, found, err := repo.GetUserServiceByID(ctx, first.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	edited.Name = "Renamed Draft"
	if err := repo.UpdateUserService(ctx, edited); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	if _, items := index(""); len(items) != 2 || items[1]["name"] != "First Line" {
		t.Fatalf("index after a draft edit = %+v, want the published name First Line", items)
	}

	if rec := request(t, h, http.MethodDelete, "/api/services/"+second.Slug+"/publication", owner); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}
	if _, items := index(""); len(items) != 1 || items[0]["slug"] != first.Slug {
		t.Fatalf("index after unpublish = %v, want [%s]", slugs(items), first.Slug)
	}

	if rec := request(t, h, http.MethodDelete, "/api/services/"+first.Slug, owner); rec.Code != http.StatusNoContent {
		t.Fatalf("delete service: status %d, body %s", rec.Code, rec.Body.String())
	}
	if body, items := index(""); len(items) != 0 || body != "[]\n" {
		t.Fatalf("index after deleting the last published service = %s, want []", body)
	}
}
