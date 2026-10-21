package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type handoverList struct {
	Incoming []struct {
		ID          string `json:"id"`
		ServiceSlug string `json:"service_slug"`
		Status      string `json:"status"`
	} `json:"incoming"`
	Outgoing []struct {
		ID          string `json:"id"`
		ServiceSlug string `json:"service_slug"`
		Status      string `json:"status"`
	} `json:"outgoing"`
}

func listHandovers(t *testing.T, h http.Handler, token string) handoverList {
	t.Helper()
	rec := request(t, h, http.MethodGet, "/api/me/handovers", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me/handovers: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out handoverList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode handovers: %v", err)
	}
	return out
}

func TestIntegration_HandoverOfferListCancelDecline(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "owner@example.com", "owner-password-1")
	recipient := provisionMember(t, h, adminToken, "recipient@example.com", "recipient-password-1")
	stranger := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password-1")

	if err := repo.CreateRoute(ctx, transit.Route{
		ID: mustUUID(t), Slug: "handover-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svc, found, err := repo.GetUserServiceByID(ctx, createUserServiceOverAPI(t, h, owner, "handover-route", "Handed Over"))
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	slug := svc.Slug
	offerPath := "/api/services/" + slug + "/handovers"

	// Enumeration: a real account and an unknown address answer alike.
	real := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`)
	if real.Code != http.StatusAccepted {
		t.Fatalf("offer: status %d, body %s", real.Code, real.Body.String())
	}
	if rec := request(t, h, http.MethodPost, offerPath, stranger, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger offer: status %d, want 404", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second pending offer: status %d, want 409", rec.Code)
	}

	out := listHandovers(t, h, owner)
	if len(out.Outgoing) != 1 || len(out.Incoming) != 0 || out.Outgoing[0].ServiceSlug != slug {
		t.Fatalf("owner list = %+v", out)
	}
	id := out.Outgoing[0].ID
	in := listHandovers(t, h, recipient)
	if len(in.Incoming) != 1 || in.Incoming[0].ID != id {
		t.Fatalf("recipient list = %+v", in)
	}

	for _, tc := range []struct{ token, action string }{
		{stranger, "cancel"}, {stranger, "decline"}, {adminToken, "cancel"}, {recipient, "cancel"}, {owner, "decline"},
	} {
		if rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/"+tc.action, tc.token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s by a non-party: status %d, want 404", tc.action, rec.Code)
		}
	}

	rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/decline", recipient)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"declined"`) {
		t.Fatalf("decline: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/cancel", owner); rec.Code != http.StatusConflict {
		t.Fatalf("cancel after decline: status %d, want 409", rec.Code)
	}

	unknown := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"nobody@example.com"}`)
	if unknown.Code != real.Code ||
		unknown.Body.String() != strings.Replace(real.Body.String(), "recipient@example.com", "nobody@example.com", 1) {
		t.Fatalf("unknown address: status %d body %s; real account: status %d body %s",
			unknown.Code, unknown.Body.String(), real.Code, real.Body.String())
	}
	if out := listHandovers(t, h, owner); len(out.Outgoing) != 0 {
		t.Fatalf("an offer to an unknown address was recorded: %+v", out)
	}

	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"owner@example.com"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("offer to self: status %d, want 422", rec.Code)
	}

	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("re-offer after decline: status %d, body %s", rec.Code, rec.Body.String())
	}
	id = listHandovers(t, h, owner).Outgoing[0].ID
	rec = request(t, h, http.MethodPost, "/api/handovers/"+id+"/cancel", owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: status %d, body %s", rec.Code, rec.Body.String())
	}
	if in := listHandovers(t, h, recipient); len(in.Incoming) != 0 {
		t.Fatalf("recipient still sees a cancelled offer: %+v", in)
	}
}

func userIDByEmail(t *testing.T, repo *postgres.Repo, email string) string {
	t.Helper()
	u, found, err := repo.GetUserByEmail(context.Background(), email)
	if err != nil || !found {
		t.Fatalf("GetUserByEmail %s: found=%v err=%v", email, found, err)
	}
	return u.ID
}

func offerAndFindIncoming(t *testing.T, h http.Handler, ownerToken, recipientToken, slug, toEmail string) string {
	t.Helper()
	rec := request(t, h, http.MethodPost, "/api/services/"+slug+"/handovers", ownerToken, `{"to_email":"`+toEmail+`"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("offer: status %d, body %s", rec.Code, rec.Body.String())
	}
	in := listHandovers(t, h, recipientToken)
	if len(in.Incoming) != 1 {
		t.Fatalf("recipient incoming = %+v, want the one offer", in)
	}
	return in.Incoming[0].ID
}

func serviceWriteBody(routeSlug, name string) string {
	return `{
		"route_slug": "` + routeSlug + `", "name": "` + name + `",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
	}`
}

func compileService(t *testing.T, h http.Handler, token, slug string) transit.Job {
	t.Helper()
	rec := request(t, h, http.MethodPost, "/api/services/"+slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("compile %s: status %d, body %s", slug, rec.Code, rec.Body.String())
	}
	var job transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode compile job: %v", err)
	}
	final := pollJob(t, h, token, job.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("compile %s: final status %q (error: %s)", slug, final.Status, final.Error)
	}
	return final
}

func TestIntegration_HandoverAcceptMovesTheServiceItsHistoryAndItsPublication(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "ho-owner@example.com", "owner-password-1")
	recipient := provisionMember(t, h, adminToken, "ho-recipient@example.com", "recipient-password-1")
	ownerID := userIDByEmail(t, repo, "ho-owner@example.com")
	recipientID := userIDByEmail(t, repo, "ho-recipient@example.com")

	// The service sits on an alignment the sender owns, which the recipient
	// could not reference: the accept must leave them on a copy of their own.
	privateRouteID := mustUUID(t)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: privateRouteID, OwnerID: &ownerID, Slug: "ho-private-route", Name: "Private Alignment",
		Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
		Segments: []transit.RouteSegment{{CantMM: 50, CurveRadiusM: 3000, GradePct: 0.5}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svcID := createUserServiceOverAPI(t, h, owner, "ho-private-route", "Handed Over")
	slug := getUserServiceByID(t, h, owner, svcID).Slug
	draftPath := "/api/services/" + slug
	publicationPath := draftPath + "/publication"

	compiled := compileService(t, h, owner, slug)
	if rec := request(t, h, http.MethodPut, publicationPath, owner); rec.Code != http.StatusOK {
		t.Fatalf("publish: status %d, body %s", rec.Code, rec.Body.String())
	}
	publicBefore := request(t, h, http.MethodGet, publicationPath, "")
	if publicBefore.Code != http.StatusOK {
		t.Fatalf("public read before: status %d", publicBefore.Code)
	}

	id := offerAndFindIncoming(t, h, owner, recipient, slug, "ho-recipient@example.com")
	rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/accept", recipient)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"accepted"`) {
		t.Fatalf("accept: status %d, body %s", rec.Code, rec.Body.String())
	}

	// The public page answers identically before and after.
	publicAfter := request(t, h, http.MethodGet, publicationPath, "")
	if publicAfter.Code != http.StatusOK || publicAfter.Body.String() != publicBefore.Body.String() {
		t.Fatalf("public read changed across the accept:\n before %s\n after  %s", publicBefore.Body.String(), publicAfter.Body.String())
	}

	// The sender gets 404 on everything the owner could do.
	after := getUserServiceByID(t, h, recipient, svcID)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, draftPath, ""},
		{http.MethodPut, draftPath, serviceWriteBody(after.RouteSlug, "Sender edit")},
		{http.MethodPost, draftPath + "/compile", ""},
		{http.MethodPut, publicationPath, ""},
		{http.MethodDelete, publicationPath, ""},
		{http.MethodGet, "/api/jobs/" + compiled.ID, ""},
	} {
		if rec := request(t, h, tc.method, tc.path, owner, tc.body); rec.Code != http.StatusNotFound {
			t.Fatalf("sender %s %s: status %d, want 404; body %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if list := request(t, h, http.MethodGet, "/api/services", owner); strings.Contains(list.Body.String(), svcID) {
		t.Fatalf("the sender still lists the service: %s", list.Body.String())
	}

	// The recipient owns it, with its compile history, on a route of their own.
	if after.OwnerID != recipientID {
		t.Fatalf("owner_id = %s, want the recipient %s", after.OwnerID, recipientID)
	}
	if after.RouteID == privateRouteID || after.RouteSlug == "ho-private-route" {
		t.Fatalf("service still on the sender's route: %+v", after)
	}
	routes, err := repo.ListRoutesByIDs(ctx, []string{privateRouteID, after.RouteID})
	if err != nil || len(routes) != 2 {
		t.Fatalf("ListRoutesByIDs: %d routes, err %v", len(routes), err)
	}
	for _, rt := range routes {
		switch rt.ID {
		case privateRouteID:
			if rt.OwnerID == nil || *rt.OwnerID != ownerID || rt.Slug != "ho-private-route" {
				t.Fatalf("sender's route changed: %+v", rt)
			}
		default:
			if rt.OwnerID == nil || *rt.OwnerID != recipientID || len(rt.Segments) != 1 ||
				len(rt.Geometry.Coordinates) != 2 {
				t.Fatalf("copy = %+v, want recipient-owned with the same geometry and segments", rt)
			}
		}
	}
	if rec := request(t, h, http.MethodGet, "/api/jobs/"+compiled.ID, recipient); rec.Code != http.StatusOK {
		t.Fatalf("recipient polls the old compile: status %d, body %s", rec.Code, rec.Body.String())
	}

	// And can go on working with it: edit on the copy, compile, republish, unpublish.
	if rec := request(t, h, http.MethodPut, draftPath, recipient, serviceWriteBody(after.RouteSlug, "Recipient edit")); rec.Code != http.StatusOK {
		t.Fatalf("recipient edit: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodPut, draftPath, recipient, serviceWriteBody("ho-private-route", "On the sender's route")); rec.Code == http.StatusOK {
		t.Fatalf("recipient could point the service back at the sender's private route")
	}
	compileService(t, h, recipient, slug)
	if rec := request(t, h, http.MethodPut, publicationPath, recipient); rec.Code != http.StatusOK {
		t.Fatalf("recipient republish: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodDelete, publicationPath, recipient); rec.Code != http.StatusNoContent {
		t.Fatalf("recipient unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}

	for _, token := range []string{owner, recipient} {
		if out := listHandovers(t, h, token); len(out.Incoming)+len(out.Outgoing) != 0 {
			t.Fatalf("an accepted offer is still listed: %+v", out)
		}
	}
}

func TestIntegration_HandoverAcceptIsRefusedWhileTheSenderScenariosListTheService(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "scn-ho-owner@example.com", "owner-password-1")
	recipient := provisionMember(t, h, adminToken, "scn-ho-recipient@example.com", "recipient-password-1")
	ingestCompileRoute(t, repo, "scn-ho-route")
	svcID := createUserServiceOverAPI(t, h, owner, "scn-ho-route", "In A Scenario")
	slug := getUserServiceByID(t, h, owner, svcID).Slug

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner, `{"name":"Weekend Trips","service_ids":["`+svcID+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode scenario: %v", err)
	}

	id := offerAndFindIncoming(t, h, owner, recipient, slug, "scn-ho-recipient@example.com")
	rec = request(t, h, http.MethodPost, "/api/handovers/"+id+"/accept", recipient)
	if rec.Code != http.StatusConflict {
		t.Fatalf("accept while in a scenario: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var refused struct {
		Code   string `json:"code"`
		Detail struct {
			Scenarios []string `json:"scenarios"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refused.Code != "service_in_scenarios" || len(refused.Detail.Scenarios) != 1 || refused.Detail.Scenarios[0] != scenario.Slug {
		t.Fatalf("refusal = %s, want code service_in_scenarios naming %s", rec.Body.String(), scenario.Slug)
	}
	if got := getUserServiceByID(t, h, owner, svcID); got.ID != svcID {
		t.Fatalf("the sender lost the service on a refused accept")
	}
	if in := listHandovers(t, h, recipient); len(in.Incoming) != 1 || in.Incoming[0].Status != "pending" {
		t.Fatalf("offer after refusal = %+v, want still pending", in)
	}

	// The sender takes it out of the scenario; the same offer then goes through.
	if rec := request(t, h, http.MethodDelete, "/api/user-scenarios/"+scenario.Slug, owner); rec.Code != http.StatusNoContent {
		t.Fatalf("delete scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/accept", recipient); rec.Code != http.StatusOK {
		t.Fatalf("accept after the scenario is gone: status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := getUserServiceByID(t, h, recipient, svcID); got.ID != svcID {
		t.Fatalf("the recipient does not list the service after accept")
	}
}

func TestIntegration_ConcurrentAcceptsOfOneHandoverSucceedExactlyOnce(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "race-owner@example.com", "owner-password-1")
	recipient := provisionMember(t, h, adminToken, "race-recipient@example.com", "recipient-password-1")
	ingestCompileRoute(t, repo, "race-route")
	svcID := createUserServiceOverAPI(t, h, owner, "race-route", "Raced")
	slug := getUserServiceByID(t, h, owner, svcID).Slug
	id := offerAndFindIncoming(t, h, owner, recipient, slug, "race-recipient@example.com")

	const attempts = 8
	codes := make([]int, attempts)
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)
	for i := range codes {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/handovers/"+id+"/accept", http.NoBody)
			req.Header.Set("Authorization", "Bearer "+recipient)
			rec := httptest.NewRecorder()
			start.Wait()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	start.Done()
	done.Wait()

	var ok, conflict int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("codes = %v: an accept answered %d", codes, code)
		}
	}
	if ok != 1 || conflict != attempts-1 {
		t.Fatalf("codes = %v, want exactly one 200 and the rest 409", codes)
	}
	if got := getUserServiceByID(t, h, recipient, svcID); got.OwnerID != userIDByEmail(t, repo, "race-recipient@example.com") {
		t.Fatalf("owner after the race = %s, want the recipient", got.OwnerID)
	}
}
