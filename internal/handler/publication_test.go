package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	pubDraftRoute = "route-draft"
	pubEdgeRoute  = "route-from-edge"
	pubOtherRoute = "route-other-edge"
)

func TestPublishServicePinsFreshCompileAndSnapshotsEdgeRoutes(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Name = "Local"
	svc.Subtext = "Electrified · Express"
	svc.Description = "Peak local"
	svc.RouteID = pubDraftRoute
	store.services[svc.ID] = svc

	job := succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: pubOtherRoute},
		{FromSlug: "b", ToSlug: "c", RouteID: pubEdgeRoute},
		{FromSlug: "c", ToSlug: "d", RouteID: pubOtherRoute},
	})
	// A later failure must not hide the succeeded compile the pin uses.
	failed := job
	failed.ID = "job-failed"
	failed.Status = transit.JobStatusFailed
	failed.Result = nil
	failed.CreatedAt = at.Add(time.Hour)
	store.jobs = []transit.Job{failed, job}

	rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "publish_failed") {
		t.Fatalf("success body contains publish_failed: %s", rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["route_id"]; ok {
		t.Fatalf("publication body includes the draft route_id: %s", rec.Body.String())
	}

	pub := decodePublication(t, rec)
	if pub.UserServiceID != svc.ID || pub.CompileJobID != "job-1" {
		t.Fatalf("pin = service %q job %q, want %q / job-1", pub.UserServiceID, pub.CompileJobID, svc.ID)
	}
	if pub.Name != "Local" || pub.Subtext != "Electrified · Express" || pub.Description != "Peak local" {
		t.Fatalf("prose = %q / %q / %q", pub.Name, pub.Subtext, pub.Description)
	}
	if pub.PublishedAt.IsZero() {
		t.Fatal("published_at is zero")
	}
	if len(pub.Routes) != 2 {
		t.Fatalf("routes = %+v, want the two edge routes", pub.Routes)
	}
	if pub.Routes[0].ID != pubOtherRoute || pub.Routes[0].Geometry.Coordinates[0][0] != -2 {
		t.Fatalf("first route = %+v, want %s in edge order", pub.Routes[0], pubOtherRoute)
	}
	if pub.Routes[1].ID != pubEdgeRoute || pub.Routes[1].Geometry.Coordinates[0][0] != -1 {
		t.Fatalf("second route = %+v, want %s", pub.Routes[1], pubEdgeRoute)
	}
	for _, rt := range pub.Routes {
		if rt.ID == pubDraftRoute {
			t.Fatalf("snapshot includes the draft route %s", pubDraftRoute)
		}
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d, want 1", store.writes)
	}
}

func TestPublishServiceEmptyProseRoundTrips(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Subtext = ""
	svc.Description = ""
	store.services[svc.ID] = svc
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}

	rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"subtext"`) || strings.Contains(body, `"description"`) {
		t.Fatalf("empty prose should omit the keys, matching UserService; body %s", body)
	}
	pub := decodePublication(t, rec)
	if pub.Subtext != "" || pub.Description != "" {
		t.Fatalf("decoded prose = %q / %q, want empty strings", pub.Subtext, pub.Description)
	}
	stored := store.pubs[svc.ID]
	if stored.Subtext != "" || stored.Description != "" {
		t.Fatalf("stored prose = %q / %q, want empty strings", stored.Subtext, stored.Description)
	}
}

func TestPublishServiceEmptyEdgesSnapshotAsEmptyArray(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{})}

	rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"routes":[]`) {
		t.Fatalf("body = %s, want an empty routes array", rec.Body.String())
	}
	pub := decodePublication(t, rec)
	if pub.Routes == nil || len(pub.Routes) != 0 {
		t.Fatalf("routes = %#v, want an empty slice", pub.Routes)
	}
	if store.pubs[svc.ID].Routes == nil {
		t.Fatal("stored routes is nil, want an empty slice")
	}
}

func TestPublishServiceRejectsMissingFailedAndStaleCompiles(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	t.Run("never compiled", func(t *testing.T) {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
		assertStaleNoWrite(t, store, svc.Slug)
	})

	t.Run("failed only", func(t *testing.T) {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
		job := succeededCompile(svc.ID, "job-1", at, nil)
		job.Status = transit.JobStatusFailed
		job.Result = nil
		store.jobs = []transit.Job{job}
		assertStaleNoWrite(t, store, svc.Slug)
	})

	t.Run("edited after compile", func(t *testing.T) {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at.Add(time.Minute))
		store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
		assertStaleNoWrite(t, store, svc.Slug)
	})
}

func TestPublishServiceReplacesSnapshot(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Name = "Local"
	store.services[svc.ID] = svc
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: pubEdgeRoute},
	})}

	first := decodePublication(t, publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug))
	if first.CompileJobID != "job-1" || first.Name != "Local" {
		t.Fatalf("first = job %q name %q", first.CompileJobID, first.Name)
	}

	second := decodePublication(t, publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug))
	if second.CompileJobID != "job-1" || second.Name != "Local" {
		t.Fatalf("republish of an unchanged draft = job %q name %q, want job-1 / Local", second.CompileJobID, second.Name)
	}
	if !second.PublishedAt.After(first.PublishedAt) {
		t.Fatalf("published_at = %s, want after %s", second.PublishedAt, first.PublishedAt)
	}

	edited := at.Add(time.Hour)
	svc.Name = "Express"
	svc.Subtext = "Diesel"
	svc.Description = "The second snapshot"
	svc.UpdatedAt = edited
	store.services[svc.ID] = svc
	store.jobs = append(store.jobs, succeededCompile(svc.ID, "job-2", edited, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: pubOtherRoute},
	}))

	third := decodePublication(t, publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug))
	if third.CompileJobID != "job-2" || third.Name != "Express" || third.Subtext != "Diesel" || third.Description != "The second snapshot" {
		t.Fatalf("replaced snapshot = %+v", third)
	}
	if len(third.Routes) != 1 || third.Routes[0].ID != pubOtherRoute {
		t.Fatalf("replaced routes = %+v, want %s", third.Routes, pubOtherRoute)
	}
	if !third.PublishedAt.After(second.PublishedAt) {
		t.Fatalf("replaced published_at = %s, want after %s", third.PublishedAt, second.PublishedAt)
	}
	if store.writes != 3 {
		t.Fatalf("writes = %d, want 3", store.writes)
	}
	if store.pubs[svc.ID].CompileJobID != "job-2" {
		t.Fatalf("stored pin = %s, want job-2", store.pubs[svc.ID].CompileJobID)
	}
}

func TestStaleRepublishLeavesSnapshotUnchanged(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Name = "Local"
	svc.Subtext = "Electrified · Express"
	svc.Description = "Peak local"
	store.services[svc.ID] = svc
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: pubEdgeRoute},
	})}

	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	saved := store.pubs[svc.ID]

	edited := svc
	edited.Name = "Express"
	edited.Subtext = "Diesel"
	edited.Description = "Edited after publish"
	edited.UpdatedAt = at.Add(time.Hour)
	store.services[svc.ID] = edited

	assertStaleNoWrite(t, store, svc.Slug)
	assertStoredPublicationUnchanged(t, store.pubs[svc.ID], saved)
}

func TestPublishMissingEdgeRouteIsStaleAndKeepsSnapshot(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Name = "Local"
	svc.Subtext = "Electrified · Express"
	svc.Description = "Peak local"
	store.services[svc.ID] = svc
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: pubEdgeRoute},
	})}

	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	saved := store.pubs[svc.ID]

	// The replacement graph is current against the draft, but it names a route
	// ListRoutesByIDs will not return. That is stale_graph, and the stored
	// snapshot stays the one from the first publish.
	store.jobs = append(store.jobs, succeededCompile(svc.ID, "job-2", at.Add(time.Second), []transit.Edge{
		{FromSlug: "a", ToSlug: "b", RouteID: "route-missing"},
	}))

	assertStaleNoWrite(t, store, svc.Slug)
	assertStoredPublicationUnchanged(t, store.pubs[svc.ID], saved)
}

func TestPublishServiceHonorsResolvedBoardingWait(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	half := transit.BoardingWaitPolicy{Kind: transit.BoardingWaitHalfHeadway}

	t.Run("global policy change", func(t *testing.T) {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
		store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
		assertStaleNoWriteWith(t, store, half, svc.Slug)
	})

	t.Run("service override still matches the graph", func(t *testing.T) {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
		svc.BoardingWait = &transit.BoardingWaitOverride{Policy: transit.BoardingWaitNone}
		store.services[svc.ID] = svc
		store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}

		rec := publishAs(t, store, half, svcOwner, svc.Slug)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
		}
		if decodePublication(t, rec).CompileJobID != "job-1" {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestUnpublishServiceIsIdempotent(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)

	rec := unpublishAs(t, store, svcOwner, svc.Slug)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unpublished delete: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}

	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.pubs[svc.ID]; !ok {
		t.Fatal("publish did not store a publication")
	}

	rec = unpublishAs(t, store, svcOwner, svc.Slug)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("published delete: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.pubs[svc.ID]; ok {
		t.Fatal("unpublish left the publication row")
	}
}

func TestPublishServiceHidesMissingAndStrangers(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}

	rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcStranger, svc.Slug)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger publish: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if store.writes != 0 {
		t.Fatalf("stranger publish wrote %d times", store.writes)
	}

	rec = publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, "no-such-service")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing publish: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}

	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("owner publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	rec = unpublishAs(t, store, svcStranger, svc.Slug)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger unpublish: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.pubs[svc.ID]; !ok {
		t.Fatal("stranger unpublish removed the publication")
	}

	rec = unpublishAs(t, store, svcOwner, "no-such-service")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing unpublish: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

var publicationReaders = []struct {
	name string
	user account.User
}{
	{"anonymous", account.User{}},
	{"stranger", svcStranger},
	{"owner", svcOwner},
	{"admin", svcAdmin},
}

type publicationBody struct {
	transit.ServicePublication
	transit.TransitGraph
}

func TestGetServicePublicationServesTheSnapshotToEveryCaller(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	svc.Name = "Published name"
	svc.Subtext = "Published subtext"
	svc.Description = "Published description"
	store.services[svc.ID] = svc
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: pubEdgeRoute},
	})}
	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}

	// The draft moves on after publishing: new prose, another alignment, and a
	// newer compile with a different graph. None of it may reach the read.
	svc.Name = "Draft name"
	svc.Subtext = "Draft subtext"
	svc.RouteID = pubOtherRoute
	svc.UpdatedAt = at.Add(time.Hour)
	store.services[svc.ID] = svc
	store.jobs = append(store.jobs, succeededCompile(svc.ID, "job-2", at.Add(2*time.Hour), []transit.Edge{
		{FromSlug: "x", ToSlug: "y", Seconds: 90, RouteID: pubOtherRoute},
	}))

	var first string
	for _, reader := range publicationReaders {
		rec := readPublicationAs(t, store, reader.user, svc.Slug)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body %s", reader.name, rec.Code, rec.Body.String())
		}
		if first == "" {
			first = rec.Body.String()
			continue
		}
		if rec.Body.String() != first {
			t.Fatalf("%s read a different body than anonymous:\n got %s\nwant %s", reader.name, rec.Body.String(), first)
		}
	}

	var got publicationBody
	if err := json.Unmarshal([]byte(first), &got); err != nil {
		t.Fatalf("decode: %v; body %s", err, first)
	}
	if got.UserServiceID != svc.ID || got.CompileJobID != "job-1" {
		t.Fatalf("pin = service %q job %q, want %q / job-1", got.UserServiceID, got.CompileJobID, svc.ID)
	}
	if got.Name != "Published name" || got.Subtext != "Published subtext" || got.Description != "Published description" {
		t.Fatalf("prose = %q / %q / %q, want the snapshot", got.Name, got.Subtext, got.Description)
	}
	if got.PublishedAt.IsZero() {
		t.Fatal("published_at is zero")
	}
	if len(got.Services) != 1 || len(got.Services[0].Edges) != 1 || got.Services[0].Edges[0].FromSlug != "a" {
		t.Fatalf("graph = %+v, want job-1's", got.Services)
	}
	if len(got.Routes) != 1 || got.Routes[0].ID != pubEdgeRoute {
		t.Fatalf("routes = %+v, want the snapshot's %s", got.Routes, pubEdgeRoute)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(first), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"services", "routes", "published_at", "compile_job_id"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("body has no %q key: %s", key, first)
		}
	}
	for _, key := range []string{"owner_id", "route_id", "slug", "vehicle", "stops"} {
		if _, ok := raw[key]; ok {
			t.Errorf("body carries the draft's %q: %s", key, first)
		}
	}
}

func TestGetServicePublicationAnswersUnpublishedAsUnknown(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}

	unknown := readPublicationAs(t, store, account.User{}, "no-such-service")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: status = %d, want 404; body %s", unknown.Code, unknown.Body.String())
	}
	want := unknown.Body.String()

	// Owner and admin included: the publication resource holds nothing until
	// the service is published, whoever asks. The draft is a different URL.
	assertHidden := func(stage string) {
		t.Helper()
		for _, reader := range publicationReaders {
			rec := readPublicationAs(t, store, reader.user, svc.Slug)
			if rec.Code != http.StatusNotFound || rec.Body.String() != want {
				t.Fatalf("%s, %s: status %d body %s; want 404 %s", stage, reader.name, rec.Code, rec.Body.String(), want)
			}
		}
	}

	assertHidden("never published")

	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	if rec := readPublicationAs(t, store, account.User{}, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("published read: status = %d; body %s", rec.Code, rec.Body.String())
	}
	if rec := unpublishAs(t, store, svcOwner, svc.Slug); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status = %d; body %s", rec.Code, rec.Body.String())
	}

	assertHidden("unpublished")
}

func TestGetServicePublicationWithoutItsPinnedJob(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pinnedToNothing := func() *fakePublicationStore {
		store := newFakePublicationStore()
		svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
		store.pubs[svc.ID] = transit.ServicePublication{
			UserServiceID: svc.ID, CompileJobID: "job-1", Name: "Published", Routes: []transit.Route{}, PublishedAt: at,
		}
		return store
	}

	t.Run("job gone", func(t *testing.T) {
		store := pinnedToNothing()
		want := readPublicationAs(t, store, account.User{}, "no-such-service").Body.String()
		rec := readPublicationAs(t, store, account.User{}, "line-a")
		if rec.Code != http.StatusNotFound || rec.Body.String() != want {
			t.Fatalf("status %d body %s; want 404 %s", rec.Code, rec.Body.String(), want)
		}
	})

	t.Run("job without a graph", func(t *testing.T) {
		store := pinnedToNothing()
		job := succeededCompile("svc-1", "job-1", at, nil)
		job.Result = nil
		store.jobs = []transit.Job{job}
		rec := readPublicationAs(t, store, account.User{}, "line-a")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "job-1") {
			t.Fatalf("500 body leaks the job id: %s", rec.Body.String())
		}
	})
}

func TestGetServicePublicationStoreFailures(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tt := range []struct {
		name string
		fail func(*fakePublicationStore)
	}{
		{"publication read", func(f *fakePublicationStore) { f.pubReadErr = context.DeadlineExceeded }},
		{"pinned job read", func(f *fakePublicationStore) { f.jobReadErr = context.DeadlineExceeded }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakePublicationStore()
			svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
			store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
			if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
				t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
			}
			tt.fail(store)
			rec := readPublicationAs(t, store, account.User{}, svc.Slug)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

type fakePublicationStore struct {
	services   map[string]transit.UserService
	jobs       []transit.Job
	routes     map[string]transit.Route
	pubs       map[string]transit.ServicePublication
	writes     int
	now        time.Time
	pubReadErr error
	jobReadErr error
}

func newFakePublicationStore() *fakePublicationStore {
	return &fakePublicationStore{
		services: map[string]transit.UserService{},
		routes: map[string]transit.Route{
			pubDraftRoute: {
				ID: pubDraftRoute, Slug: "draft", Name: "Draft alignment",
				Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 1}}},
			},
			pubEdgeRoute: {
				ID: pubEdgeRoute, Slug: "edge", Name: "Edge alignment",
				Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-1, 37}, {-1.5, 37.5}}},
			},
			pubOtherRoute: {
				ID: pubOtherRoute, Slug: "other", Name: "Other alignment",
				Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-2, 38}, {-2.5, 38.5}}},
			},
		},
		pubs: map[string]transit.ServicePublication{},
		now:  time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC),
	}
}

func (f *fakePublicationStore) GetUserServiceBySlug(_ context.Context, slug string) (transit.UserService, bool, error) {
	for _, svc := range f.services {
		if svc.Slug == slug {
			return svc, true, nil
		}
	}
	return transit.UserService{}, false, nil
}

func (f *fakePublicationStore) PublishUserService(ctx context.Context, id string, decide handler.PublicationDecide) (transit.ServicePublication, error) {
	svc, ok := f.services[id]
	if !ok {
		return transit.ServicePublication{}, context.Canceled
	}
	pub, err := decide(ctx, svc, f)
	if err != nil {
		return transit.ServicePublication{}, err
	}
	if pub.Routes == nil {
		pub.Routes = []transit.Route{}
	}
	pub.PublishedAt = f.now
	f.now = f.now.Add(time.Second)
	f.pubs[id] = pub
	f.writes++
	return pub, nil
}

func (f *fakePublicationStore) UnpublishUserService(_ context.Context, id string) error {
	delete(f.pubs, id)
	return nil
}

func (f *fakePublicationStore) GetServicePublicationBySlug(_ context.Context, slug string) (transit.ServicePublication, bool, error) {
	if f.pubReadErr != nil {
		return transit.ServicePublication{}, false, f.pubReadErr
	}
	for _, svc := range f.services {
		if svc.Slug == slug {
			pub, ok := f.pubs[svc.ID]
			return pub, ok, nil
		}
	}
	return transit.ServicePublication{}, false, nil
}

func (f *fakePublicationStore) GetSucceededCompileJob(_ context.Context, id string) (transit.Job, bool, error) {
	if f.jobReadErr != nil {
		return transit.Job{}, false, f.jobReadErr
	}
	for _, j := range f.jobs {
		if j.ID == id && j.Kind == transit.JobKindCompileUserService && j.Status == transit.JobStatusSucceeded {
			return j, true, nil
		}
	}
	return transit.Job{}, false, nil
}

func (f *fakePublicationStore) LatestSucceededCompileJob(_ context.Context, serviceID string) (transit.Job, bool, error) {
	var latest transit.Job
	found := false
	for _, j := range f.jobs {
		if j.UserServiceID == nil || *j.UserServiceID != serviceID {
			continue
		}
		if j.Kind != transit.JobKindCompileUserService || j.Status != transit.JobStatusSucceeded {
			continue
		}
		if !found || j.CreatedAt.After(latest.CreatedAt) {
			latest = j
			found = true
		}
	}
	return latest, found, nil
}

func (f *fakePublicationStore) ListRoutesByIDs(_ context.Context, ids []string) ([]transit.Route, error) {
	out := make([]transit.Route, 0, len(ids))
	for _, id := range ids {
		if rt, ok := f.routes[id]; ok {
			out = append(out, rt)
		}
	}
	return out, nil
}

func seedPublicationService(f *fakePublicationStore, id, slug, owner string, updatedAt time.Time) transit.UserService {
	svc := transit.UserService{
		ID: id, Slug: slug, OwnerID: owner, RouteID: pubDraftRoute,
		Name: "Seeded", UpdatedAt: updatedAt,
	}
	f.services[id] = svc
	return svc
}

func succeededCompile(serviceID, jobID string, created time.Time, edges []transit.Edge) transit.Job {
	if edges == nil {
		edges = []transit.Edge{{FromSlug: "a", ToSlug: "b", RouteID: pubEdgeRoute}}
	}
	return transit.Job{
		ID: jobID, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded,
		UserServiceID: &serviceID, CreatedAt: created,
		CompiledServiceIDs: []string{serviceID},
		Result: &transit.TransitGraph{
			Services: []transit.ServiceGraph{{
				ServiceID: serviceID, WaitPolicy: string(transit.BoardingWaitNone), Edges: edges,
			}},
		},
	}
}

func publicationMux(store handler.PublicationStore, policy transit.BoardingWaitPolicy) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/services/{slug}/publication", handler.PublishService(store, policy))
	mux.HandleFunc("DELETE /api/services/{slug}/publication", handler.UnpublishService(store))
	return mux
}

func readPublicationAs(t *testing.T, store handler.PublishedServiceStore, user account.User, slug string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/{slug}/publication", handler.GetServicePublication(store))
	return publicationRequest(t, mux, http.MethodGet, "/api/services/"+slug+"/publication", user)
}

func publishAs(t *testing.T, store handler.PublicationStore, policy transit.BoardingWaitPolicy, user account.User, slug string) *httptest.ResponseRecorder {
	t.Helper()
	return publicationRequest(t, publicationMux(store, policy), http.MethodPut, "/api/services/"+slug+"/publication", user)
}

func unpublishAs(t *testing.T, store handler.PublicationStore, user account.User, slug string) *httptest.ResponseRecorder {
	t.Helper()
	return publicationRequest(t, publicationMux(store, transit.DefaultBoardingWaitPolicy()), http.MethodDelete, "/api/services/"+slug+"/publication", user)
}

func publicationRequest(t *testing.T, h http.Handler, method, target string, user account.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if user.ID != "" {
		req = req.WithContext(auth.WithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodePublication(t *testing.T, rec *httptest.ResponseRecorder) transit.ServicePublication {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var pub transit.ServicePublication
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatalf("decode: %v; body %s", err, rec.Body.String())
	}
	return pub
}

func assertStoredPublicationUnchanged(t *testing.T, got, want transit.ServicePublication) {
	t.Helper()
	if got.CompileJobID != want.CompileJobID || got.Name != want.Name || got.Subtext != want.Subtext ||
		got.Description != want.Description || !got.PublishedAt.Equal(want.PublishedAt) {
		t.Fatalf("stored publication = %+v, want %+v", got, want)
	}
	if len(got.Routes) != len(want.Routes) {
		t.Fatalf("stored routes = %+v, want %+v", got.Routes, want.Routes)
	}
	for i := range want.Routes {
		if got.Routes[i].ID != want.Routes[i].ID ||
			got.Routes[i].Geometry.Coordinates[0][0] != want.Routes[i].Geometry.Coordinates[0][0] {
			t.Fatalf("stored route %d = %+v, want %+v", i, got.Routes[i], want.Routes[i])
		}
	}
}

func assertStaleNoWrite(t *testing.T, store *fakePublicationStore, slug string) {
	t.Helper()
	assertStaleNoWriteWith(t, store, transit.DefaultBoardingWaitPolicy(), slug)
}

func assertStaleNoWriteWith(t *testing.T, store *fakePublicationStore, policy transit.BoardingWaitPolicy, slug string) {
	t.Helper()
	before := store.writes
	rec := publishAs(t, store, policy, svcOwner, slug)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code = %q, want %q", body["code"], handler.StaleGraphErrorCode)
	}
	if body["code"] == "publish_failed" || strings.Contains(rec.Body.String(), "publish_failed") {
		t.Fatalf("body answers publish_failed: %s", rec.Body.String())
	}
	if store.writes != before {
		t.Fatalf("writes = %d, want %d (no publication row)", store.writes, before)
	}
}
