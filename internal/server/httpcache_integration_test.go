package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// A publication over a real curated alignment, read the way a browser reads
// it: compressed, then revalidated.
func TestIntegration_PublicationIsCompressedAndRevalidates(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	admin := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, admin, "cache-owner@example.com", "member-password")

	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sc, _ := store.GetScenarioBySlug("ca-hsr")
	var geom [][]float64
	for _, rt := range store.GetRoutesByScenario(sc.ID) {
		if len(rt.Geometry.Coordinates) > len(geom) {
			geom = rt.Geometry.Coordinates
		}
	}
	routeID := mustUUID(t)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, Slug: "cache-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: geom},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	first, last := geom[0], geom[len(geom)-1]
	rec := request(t, h, http.MethodPost, "/api/services", owner, fmt.Sprintf(`{
		"route_slug": "cache-route", "name": "Cached Line",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": %v, "lng": %v}, {"name": "B", "lat": %v, "lng": %v}]
	}`, first[1], first[0], last[1], last[0]))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var svc transit.UserService
	if err := json.Unmarshal(rec.Body.Bytes(), &svc); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	publicPath := "/api/services/" + svc.Slug + "/publication"

	compile := func() {
		t.Helper()
		id := mustUUID(t)
		if err := repo.CreateJob(ctx, transit.Job{
			ID: id, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
			UserServiceID: &svc.ID, OwnerID: &svc.OwnerID,
		}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		if err := repo.CompleteJob(ctx, id, transit.TransitGraph{
			Services: []transit.ServiceGraph{{
				ServiceID: svc.ID, WaitPolicy: string(transit.BoardingWaitNone),
				Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 600, RouteID: routeID}},
			}},
		}, []string{svc.ID}); err != nil {
			t.Fatalf("CompleteJob: %v", err)
		}
		if rec := request(t, h, http.MethodPut, publicPath, owner); rec.Code != http.StatusOK {
			t.Fatalf("publish: status %d, body %s", rec.Code, rec.Body.String())
		}
	}
	compile()

	plain := getWith(t, h, publicPath, nil)
	if plain.Code != http.StatusOK {
		t.Fatalf("read: status %d, body %s", plain.Code, plain.Body.String())
	}
	zipped := getWith(t, h, publicPath, map[string]string{"Accept-Encoding": "gzip"})
	if zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", zipped.Header().Get("Content-Encoding"))
	}
	ratio := float64(plain.Body.Len()) / float64(zipped.Body.Len())
	t.Logf("publication: %d bytes plain, %d gzipped (%.1fx)", plain.Body.Len(), zipped.Body.Len(), ratio)
	if ratio < 3 {
		t.Errorf("gzip ratio %.1fx, want at least 3x", ratio)
	}

	etag := zipped.Header().Get("ETag")
	if zipped.Header().Get("Cache-Control") != wantPublicCacheControl || etag == "" {
		t.Fatalf("Cache-Control %q ETag %q; want public with a tag", zipped.Header().Get("Cache-Control"), etag)
	}
	again := getWith(t, h, publicPath, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": etag})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("revalidation: status %d, %d bytes; want an empty 304", again.Code, again.Body.Len())
	}

	compile()
	if rec := getWith(t, h, publicPath, map[string]string{"If-None-Match": etag}); rec.Code != http.StatusOK {
		t.Fatalf("after republish: status %d, want 200", rec.Code)
	}

	// The author's name is read live, so a rename must reach a reader holding
	// the old tag without a republish.
	etag = getWith(t, h, publicPath, nil).Header().Get("ETag")
	if rec := request(t, h, http.MethodPatch, "/api/auth/me", owner, `{"name":"Renamed Author"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := getWith(t, h, publicPath, map[string]string{"If-None-Match": etag}); rec.Code != http.StatusOK {
		t.Fatalf("after rename: status %d, want 200", rec.Code)
	}
}
