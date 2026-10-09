package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/httpcache"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

var testTags = httpcache.NewTagger("test-build")

func conditionalPublicationRead(t *testing.T, store handler.ServicePublicationStore, slug, etag string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/services/{slug}/publication", handler.GetServicePublication(store, testTags))
	req := httptest.NewRequest(http.MethodGet, "/api/services/"+slug+"/publication", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func assertPublic(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != httpcache.Public {
		t.Errorf("Cache-Control = %q, want %q", got, httpcache.Public)
	}
	etag := rec.Header().Get("ETag")
	if len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("ETag = %q, want a strong, quoted tag", etag)
	}
	return etag
}

func assertNotPublic(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Header().Get("Cache-Control") == httpcache.Public || rec.Header().Get("ETag") != "" {
		t.Errorf("status %d was made cacheable: Cache-Control %q ETag %q", rec.Code,
			rec.Header().Get("Cache-Control"), rec.Header().Get("ETag"))
	}
}

func TestGetServicePublicationRevalidatesUntilRepublished(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}

	rec := readPublicationAs(t, store, svcStranger, svc.Slug)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	etag := assertPublic(t, rec)

	// The graph is the expensive half of the read, and a revalidation must
	// not need it: with it unreadable, a matching tag still answers 304.
	store.jobReadErr = errors.New("graph unreadable")
	again := conditionalPublicationRead(t, store, svc.Slug, etag)
	if again.Code != http.StatusNotModified {
		t.Fatalf("revalidation: status = %d, want 304; body %s", again.Code, again.Body.String())
	}
	if again.Body.Len() != 0 {
		t.Errorf("304 carried a body: %s", again.Body.String())
	}
	if got := assertPublic(t, again); got != etag {
		t.Errorf("304 ETag = %q, want %q", got, etag)
	}
	store.jobReadErr = nil

	// A republish pins a new compile and moves published_at: the old tag no
	// longer names what is served.
	store.jobs = append(store.jobs, succeededCompile(svc.ID, "job-2", at.Add(time.Hour), nil))
	if rec := publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("republish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	republished := conditionalPublicationRead(t, store, svc.Slug, etag)
	if republished.Code != http.StatusOK {
		t.Fatalf("after republish: status = %d, want 200", republished.Code)
	}
	if assertPublic(t, republished) == etag {
		t.Error("republish kept the old ETag")
	}
}

// The byline is read live from the account, not frozen at publish, so a
// rename changes the body without a republish.
func TestGetServicePublicationTagFollowsTheAuthorsName(t *testing.T) {
	store := newFakePublicationStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
	publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug)
	pub := store.pubs[svc.ID]
	pub.AuthorName = "Before"
	store.pubs[svc.ID] = pub

	etag := assertPublic(t, readPublicationAs(t, store, svcStranger, svc.Slug))

	pub.AuthorName = "After"
	store.pubs[svc.ID] = pub
	if rec := conditionalPublicationRead(t, store, svc.Slug, etag); rec.Code != http.StatusOK {
		t.Fatalf("after rename: status = %d, want 200", rec.Code)
	}
}

func conditionalPreRead(t *testing.T, store handler.PrerenderedStore, target, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	prerenderedMux(store).ServeHTTP(rec, req)
	return rec
}

func TestPrerenderedReadsRevalidateUntilTheyChange(t *testing.T) {
	for _, target := range []string{
		"/api/prerendered-isochrones/entry-1",
		"/api/scenarios/" + preScenarioSlug + "/prerendered-isochrones",
	} {
		t.Run(target, func(t *testing.T) {
			store := newFakePrerenderedStore()
			store.put(seededEntry("entry-1", "still current"))

			rec := preRequest(t, store, http.MethodGet, target, "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			etag := assertPublic(t, rec)

			again := conditionalPreRead(t, store, target, etag)
			if again.Code != http.StatusNotModified {
				t.Fatalf("revalidation: status = %d, want 304", again.Code)
			}
			if again.Body.Len() != 0 {
				t.Errorf("304 carried a body: %s", again.Body.String())
			}

			// outdated is computed on read from the scenario's membership, not
			// stored on the row, so a reseed that drops a member must change
			// the tag even though the row itself did not move.
			store.members["scenario-1"] = store.members["scenario-1"][:1]
			if rec := conditionalPreRead(t, store, target, etag); rec.Code != http.StatusOK {
				t.Fatalf("after membership change: status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestPrerenderedIsochroneTagFollowsTheRowsUpdatedAt(t *testing.T) {
	store := newFakePrerenderedStore()
	entry := seededEntry("entry-1", "v1")
	entry.UpdatedAt = preNow
	store.put(entry)
	etag := assertPublic(t, preRequest(t, store, http.MethodGet, "/api/prerendered-isochrones/entry-1", "", ""))

	entry.UpdatedAt = preNow.Add(time.Minute)
	store.entries[entry.ID] = entry
	if rec := conditionalPreRead(t, store, "/api/prerendered-isochrones/entry-1", etag); rec.Code != http.StatusOK {
		t.Fatalf("after an update: status = %d, want 200", rec.Code)
	}
}

func TestPrerenderedReadsDoNotCacheANotFound(t *testing.T) {
	store := newFakePrerenderedStore()
	assertNotPublic(t, preRequest(t, store, http.MethodGet, "/api/prerendered-isochrones/no-such-id", "", ""))
	assertNotPublic(t, preRequest(t, store, http.MethodGet, "/api/scenarios/no-such/prerendered-isochrones", "", ""))
}

func conditionalIndexRead(t *testing.T, store handler.PublishedServiceStore, query, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/published-services"+query, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	handler.PublishedServices(store, testTags).ServeHTTP(rec, req)
	return rec
}

func TestPublishedServicesRevalidatesUntilTheIndexChanges(t *testing.T) {
	for _, query := range []string{"", "?limit=10"} {
		t.Run("query "+query, func(t *testing.T) {
			at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			store := &fakePublishedServiceStore{items: []transit.PublishedServiceSummary{
				{Slug: "line-a", Name: "Line A", AuthorName: "Ada", PublishedAt: at},
			}}
			etag := assertPublic(t, getPublishedServices(t, store, query))

			if rec := conditionalIndexRead(t, store, query, etag); rec.Code != http.StatusNotModified {
				t.Fatalf("revalidation: status = %d, want 304", rec.Code)
			}

			store.items = append(store.items, transit.PublishedServiceSummary{
				Slug: "line-b", Name: "Line B", AuthorName: "Bo", PublishedAt: at.Add(time.Hour),
			})
			if rec := conditionalIndexRead(t, store, query, etag); rec.Code != http.StatusOK {
				t.Fatalf("after a publish: status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestPublishedServicesDoesNotCacheFailures(t *testing.T) {
	assertNotPublic(t, getPublishedServices(t, &fakePublishedServiceStore{}, "?limit=0"))
	assertNotPublic(t, getPublishedServices(t, &fakePublishedServiceStore{err: errors.New("down")}, ""))
}

func TestGetServicePublicationDoesNotCacheFailures(t *testing.T) {
	store := newFakePublicationStore()
	assertNotPublic(t, readPublicationAs(t, store, svcStranger, "no-such-service"))

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
	publishAs(t, store, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug)
	store.jobReadErr = errors.New("graph unreadable")
	rec := readPublicationAs(t, store, svcStranger, svc.Slug)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertNotPublic(t, rec)
}
