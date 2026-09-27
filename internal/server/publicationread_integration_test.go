package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type publicationReadBody struct {
	transit.ServicePublication
	transit.TransitGraph
}

type draftGraphBody struct {
	transit.TransitGraph
	Routes []transit.Route `json:"routes"`
}

func TestIntegration_PublicationIsPublicAndTheDraftIsNot(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	admin := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, admin, "public-owner@example.com", "member-password")
	stranger := provisionMember(t, h, admin, "public-stranger@example.com", "member-password")

	publishedGeom := [][]float64{{-122, 37}, {-121, 37}}
	routeID := mustUUID(t)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, Slug: "public-read-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: publishedGeom},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	serviceBody := func(name, subtext string) string {
		return `{
			"route_slug": "public-read-route", "name": "` + name + `", "subtext": "` + subtext + `",
			"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
			"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
		}`
	}
	rec := request(t, h, http.MethodPost, "/api/services", owner, serviceBody("Public Line", "As published"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var svc transit.UserService
	if err := json.Unmarshal(rec.Body.Bytes(), &svc); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	draftPath := "/api/services/" + svc.Slug
	publicPath := draftPath + "/publication"

	compile := func(seconds int) string {
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
				Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: seconds, RouteID: routeID}},
			}},
		}, []string{svc.ID}); err != nil {
			t.Fatalf("CompleteJob: %v", err)
		}
		return id
	}

	rec = request(t, h, http.MethodGet, "/api/services/no-such-service/publication", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	unknown := rec.Body.String()

	readers := []struct{ name, token string }{
		{"anonymous", ""}, {"stranger", stranger}, {"owner", owner}, {"admin", admin},
	}
	// The publication resource answers the same to everyone. Unpublished, that
	// is the unknown slug's 404 to the letter — so neither a stranger nor an
	// anonymous caller learns a draft exists by guessing its slug.
	assertHidden := func(stage string) {
		t.Helper()
		for _, reader := range readers {
			rec := request(t, h, http.MethodGet, publicPath, reader.token)
			if rec.Code != http.StatusNotFound || rec.Body.String() != unknown {
				t.Fatalf("%s, %s: status %d body %s; want 404 %s", stage, reader.name, rec.Code, rec.Body.String(), unknown)
			}
		}
	}

	// Compiled but never published: a fresh graph is not a publication.
	publishedJob := compile(60)
	assertHidden("never published")

	if rec := request(t, h, http.MethodPut, publicPath, owner); rec.Code != http.StatusOK {
		t.Fatalf("publish: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = request(t, h, http.MethodGet, publicPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous read: status %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	published := rec.Body.String()
	for _, reader := range readers[1:] {
		rec := request(t, h, http.MethodGet, publicPath, reader.token)
		if rec.Code != http.StatusOK || rec.Body.String() != published {
			t.Fatalf("%s read: status %d body %s; want anonymous's %s", reader.name, rec.Code, rec.Body.String(), published)
		}
	}

	// The owner edits and recompiles without republishing. Their draft moves;
	// the publication must not.
	if rec := request(t, h, http.MethodPut, draftPath, owner, serviceBody("Edited Line", "Draft only")); rec.Code != http.StatusOK {
		t.Fatalf("edit draft: status %d, body %s", rec.Code, rec.Body.String())
	}
	liveRoute, found, err := repo.GetRouteBySlug(ctx, "public-read-route")
	if err != nil || !found {
		t.Fatalf("GetRouteBySlug: found=%v err=%v", found, err)
	}
	liveGeom := [][]float64{{-10, 2}, {-9, 2}}
	liveRoute.Geometry.Coordinates = liveGeom
	if err := repo.UpdateRoute(ctx, liveRoute); err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	draftJob := compile(90)

	rec = request(t, h, http.MethodGet, draftPath, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner draft read: status %d, body %s", rec.Code, rec.Body.String())
	}
	var draft transit.UserService
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	rec = request(t, h, http.MethodGet, draftPath+"/graph", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner graph read: status %d, body %s", rec.Code, rec.Body.String())
	}
	var draftGraph draftGraphBody
	if err := json.Unmarshal(rec.Body.Bytes(), &draftGraph); err != nil {
		t.Fatalf("decode draft graph: %v", err)
	}

	rec = request(t, h, http.MethodGet, publicPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous read after edit: status %d, body %s", rec.Code, rec.Body.String())
	}
	var public publicationReadBody
	if err := json.Unmarshal(rec.Body.Bytes(), &public); err != nil {
		t.Fatalf("decode publication: %v", err)
	}

	if draft.Name != "Edited Line" || draft.Subtext != "Draft only" {
		t.Fatalf("owner draft prose = %q / %q, want the live edit", draft.Name, draft.Subtext)
	}
	if public.Name != "Public Line" || public.Subtext != "As published" {
		t.Fatalf("public prose = %q / %q, want the snapshot", public.Name, public.Subtext)
	}
	if got := edgeSeconds(draftGraph.TransitGraph); got != 90 {
		t.Fatalf("owner graph edge = %ds, want the recompiled 90s", got)
	}
	if public.CompileJobID != publishedJob || public.CompileJobID == draftJob {
		t.Fatalf("public pin = %s, want %s (not the draft's %s)", public.CompileJobID, publishedJob, draftJob)
	}
	if got := edgeSeconds(public.TransitGraph); got != 60 {
		t.Fatalf("public graph edge = %ds, want the pinned 60s", got)
	}
	if len(draftGraph.Routes) != 1 || !floatCoordsEqual(draftGraph.Routes[0].Geometry.Coordinates, liveGeom) {
		t.Fatalf("owner graph geometry = %+v, want the live %v", draftGraph.Routes, liveGeom)
	}
	if len(public.Routes) != 1 || !floatCoordsEqual(public.Routes[0].Geometry.Coordinates, publishedGeom) {
		t.Fatalf("public geometry = %+v, want the frozen %v", public.Routes, publishedGeom)
	}

	// Publishing opened one resource. Every draft read and every write on a
	// published service is still refused: 401 to an anonymous caller, the
	// usual 404 to a signed-in stranger.
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, draftPath, ""},
		{http.MethodPut, draftPath, serviceBody("Hijacked", "")},
		{http.MethodDelete, draftPath, ""},
		{http.MethodGet, draftPath + "/graph", ""},
		{http.MethodPost, draftPath + "/compile", ""},
		{http.MethodPut, publicPath, ""},
		{http.MethodDelete, publicPath, ""},
	} {
		if rec := request(t, h, call.method, call.path, "", call.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: status %d, want 401; body %s", call.method, call.path, rec.Code, rec.Body.String())
		}
		if rec := request(t, h, call.method, call.path, stranger, call.body); rec.Code != http.StatusNotFound {
			t.Errorf("stranger %s %s: status %d, want 404; body %s", call.method, call.path, rec.Code, rec.Body.String())
		}
	}
	if rec := request(t, h, http.MethodPost, draftPath+"/isochrone", "",
		`{"lat": 37, "lng": -121.8, "budget_mins": 30, "mode": "walk"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous isochrone: status %d, want 401; body %s", rec.Code, rec.Body.String())
	}

	// None of those refusals changed anything: the draft, its latest compile
	// and the publication are all as the owner left them.
	stored, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found || stored.Name != "Edited Line" {
		t.Fatalf("draft after refusals: found=%v err=%v name=%q", found, err, stored.Name)
	}
	latest, found, err := repo.GetLatestSucceededUserServiceJob(ctx, svc.Slug)
	if err != nil || !found || latest.ID != draftJob {
		t.Fatalf("latest compile after refusals: found=%v err=%v id=%s, want %s", found, err, latest.ID, draftJob)
	}
	if rec := request(t, h, http.MethodGet, publicPath, ""); rec.Code != http.StatusOK || rec.Body.String() != published {
		t.Fatalf("publication after refusals: status %d body %s; want %s", rec.Code, rec.Body.String(), published)
	}

	if rec := request(t, h, http.MethodDelete, publicPath, owner); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}
	assertHidden("unpublished")

	// Unpublishing hides the publication, not the draft: the owner keeps it.
	if rec := request(t, h, http.MethodGet, draftPath, owner); rec.Code != http.StatusOK {
		t.Fatalf("owner draft after unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}
}

func edgeSeconds(g transit.TransitGraph) int {
	if len(g.Services) != 1 || len(g.Services[0].Edges) != 1 {
		return -1
	}
	return g.Services[0].Edges[0].Seconds
}
