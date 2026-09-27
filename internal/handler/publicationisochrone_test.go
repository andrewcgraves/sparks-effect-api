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
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type fakePublicationIsochroneStore struct {
	*fakePublicationStore
	fakeRoutingStore
}

func newFakePublicationIsochroneStore() *fakePublicationIsochroneStore {
	return &fakePublicationIsochroneStore{fakePublicationStore: newFakePublicationStore()}
}

func plotPublicationAs(t *testing.T, store handler.PublicationIsochroneStore, pub routing.Publisher, user account.User, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/services/{slug}/publication/isochrone", handler.PublicationIsochrone(store, pub, logger.Discard()))
	req := httptest.NewRequest(http.MethodPost, "/api/services/"+slug+"/publication/isochrone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user.ID != "" {
		req = req.WithContext(auth.WithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// publishedThenEdited publishes job-1 and then moves the draft on: a later
// edit and a newer compile, job-2. The draft's own isochrone would plot job-2
// or refuse as stale; the publication's must plot job-1 regardless.
func publishedThenEdited(t *testing.T) (*fakePublicationIsochroneStore, transit.UserService) {
	t.Helper()
	store := newFakePublicationIsochroneStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store.fakePublicationStore, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, []transit.Edge{
		{FromSlug: "a", ToSlug: "b", Seconds: 60, RouteID: pubEdgeRoute},
	})}
	if rec := publishAs(t, store.fakePublicationStore, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}

	svc.UpdatedAt = at.Add(3 * time.Hour)
	store.services[svc.ID] = svc
	store.jobs = append(store.jobs, succeededCompile(svc.ID, "job-2", at.Add(2*time.Hour), []transit.Edge{
		{FromSlug: "x", ToSlug: "y", Seconds: 90, RouteID: pubOtherRoute},
	}))
	return store, svc
}

func TestPublicationIsochroneEnqueuesOwnerlessOverThePinWhoeverAsks(t *testing.T) {
	for _, reader := range publicationReaders {
		t.Run(reader.name, func(t *testing.T) {
			store, svc := publishedThenEdited(t)
			jobsBefore, writesBefore := len(store.jobs), store.writes
			pub := &routing.FakePublisher{}

			rec := plotPublicationAs(t, store, pub, reader.user, svc.Slug, isoValidBody)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
			}
			job := decodeRoutingJob(t, rec)
			if job.OwnerID != nil {
				t.Errorf("owner_id = %q, want none: a publication's routing job is ownerless whoever asks", *job.OwnerID)
			}
			if job.CompileJobID != "job-1" {
				t.Errorf("compile_job_id = %q, want the pinned job-1, not the draft's newer compile", job.CompileJobID)
			}
			if job.Status != transit.JobStatusQueued {
				t.Errorf("status = %q, want queued", job.Status)
			}

			msgs := pub.Messages()
			if len(msgs) != 1 {
				t.Fatalf("published %d messages, want 1", len(msgs))
			}
			if msgs[0].CompileJobID != "job-1" || edgeFrom(msgs[0].Graph) != "a" {
				t.Errorf("message plots compile %q from %q, want job-1's graph", msgs[0].CompileJobID, edgeFrom(msgs[0].Graph))
			}

			stored, ok := store.only()
			if !ok || stored.OwnerID != nil {
				t.Fatalf("stored routing jobs = %d, owner %v; want one, ownerless", store.count(), stored.OwnerID)
			}
			// The pin is plotted as it stands: nothing compiled, nothing republished.
			if len(store.jobs) != jobsBefore || store.writes != writesBefore {
				t.Errorf("compile jobs %d -> %d, publication writes %d -> %d; want both unchanged",
					jobsBefore, len(store.jobs), writesBefore, store.writes)
			}

			// Anyone holding the id polls it back, through the rule the poll
			// already applies to an unowned job.
			if rec := pollAs(t, store, job.ID, account.User{}); rec.Code != http.StatusOK {
				t.Errorf("anonymous poll: status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if rec := pollAs(t, store, job.ID, svcStranger); rec.Code != http.StatusOK {
				t.Errorf("stranger poll: status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPublicationIsochroneAnswersUnpublishedAsUnknown(t *testing.T) {
	store := newFakePublicationIsochroneStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := seedPublicationService(store.fakePublicationStore, "svc-1", "line-a", svcOwner.ID, at)
	store.jobs = []transit.Job{succeededCompile(svc.ID, "job-1", at, nil)}
	pub := &routing.FakePublisher{}

	unknown := plotPublicationAs(t, store, pub, account.User{}, "no-such-service", isoValidBody)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: status = %d, want 404; body %s", unknown.Code, unknown.Body.String())
	}
	want := unknown.Body.String()
	// And the same body the publication read answers, so the two public
	// routes cannot be played off each other to tell a draft from nothing.
	if read := readPublicationAs(t, store, account.User{}, "no-such-service").Body.String(); read != want {
		t.Fatalf("isochrone 404 %s differs from the read's %s", want, read)
	}

	// A fresh compile is not a publication, and the owner gets no draft
	// fallback here: this route plots a pin or nothing.
	assertHidden := func(stage string) {
		t.Helper()
		for _, reader := range publicationReaders {
			rec := plotPublicationAs(t, store, pub, reader.user, svc.Slug, isoValidBody)
			if rec.Code != http.StatusNotFound || rec.Body.String() != want {
				t.Fatalf("%s, %s: status %d body %s; want 404 %s", stage, reader.name, rec.Code, rec.Body.String(), want)
			}
		}
		if n := store.count(); n != 0 {
			t.Fatalf("%s: recorded %d routing jobs", stage, n)
		}
		if n := len(pub.Messages()); n != 0 {
			t.Fatalf("%s: published %d messages", stage, n)
		}
	}

	assertHidden("never published")

	if rec := publishAs(t, store.fakePublicationStore, transit.DefaultBoardingWaitPolicy(), svcOwner, svc.Slug); rec.Code != http.StatusOK {
		t.Fatalf("publish: status = %d; body %s", rec.Code, rec.Body.String())
	}
	if rec := unpublishAs(t, store.fakePublicationStore, svcOwner, svc.Slug); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status = %d; body %s", rec.Code, rec.Body.String())
	}

	assertHidden("unpublished")
}

func TestPublicationIsochroneWithoutItsPinnedJob(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pinnedToNothing := func() *fakePublicationIsochroneStore {
		store := newFakePublicationIsochroneStore()
		svc := seedPublicationService(store.fakePublicationStore, "svc-1", "line-a", svcOwner.ID, at)
		store.pubs[svc.ID] = transit.ServicePublication{
			UserServiceID: svc.ID, CompileJobID: "job-1", Name: "Published", Routes: []transit.Route{}, PublishedAt: at,
		}
		return store
	}

	t.Run("job gone", func(t *testing.T) {
		store := pinnedToNothing()
		pub := &routing.FakePublisher{}
		want := plotPublicationAs(t, store, pub, account.User{}, "no-such-service", isoValidBody).Body.String()
		rec := plotPublicationAs(t, store, pub, account.User{}, "line-a", isoValidBody)
		if rec.Code != http.StatusNotFound || rec.Body.String() != want {
			t.Fatalf("status %d body %s; want 404 %s", rec.Code, rec.Body.String(), want)
		}
		if store.count() != 0 || len(pub.Messages()) != 0 {
			t.Fatalf("enqueued %d jobs, %d messages; want none", store.count(), len(pub.Messages()))
		}
	})

	t.Run("job without a graph", func(t *testing.T) {
		store := pinnedToNothing()
		job := succeededCompile("svc-1", "job-1", at, nil)
		job.Result = nil
		store.jobs = []transit.Job{job}
		pub := &routing.FakePublisher{}
		rec := plotPublicationAs(t, store, pub, account.User{}, "line-a", isoValidBody)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "job-1") {
			t.Fatalf("500 body leaks the job id: %s", rec.Body.String())
		}
		if store.count() != 0 || len(pub.Messages()) != 0 {
			t.Fatalf("enqueued %d jobs, %d messages; want none", store.count(), len(pub.Messages()))
		}
	})
}

func TestPublicationIsochroneStoreFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		fail func(*fakePublicationIsochroneStore)
	}{
		{"publication read", func(f *fakePublicationIsochroneStore) { f.pubReadErr = context.DeadlineExceeded }},
		{"pinned job read", func(f *fakePublicationIsochroneStore) { f.jobReadErr = context.DeadlineExceeded }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, svc := publishedThenEdited(t)
			tt.fail(store)
			pub := &routing.FakePublisher{}
			rec := plotPublicationAs(t, store, pub, account.User{}, svc.Slug, isoValidBody)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body.String())
			}
			if store.count() != 0 || len(pub.Messages()) != 0 {
				t.Fatalf("enqueued %d jobs, %d messages; want none", store.count(), len(pub.Messages()))
			}
		})
	}
}

func TestPublicationIsochroneRejectsABadRequest(t *testing.T) {
	for _, tt := range []struct{ name, body string }{
		{"malformed", `{"lat":`},
		{"invalid mode", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"teleport"}`},
		{"budget not positive", `{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, svc := publishedThenEdited(t)
			pub := &routing.FakePublisher{}
			rec := plotPublicationAs(t, store, pub, account.User{}, svc.Slug, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if store.count() != 0 || len(pub.Messages()) != 0 {
				t.Fatalf("enqueued %d jobs, %d messages; want none", store.count(), len(pub.Messages()))
			}
		})
	}
}

func TestPublicationIsochrone_502_unconfirmedPublishFailsTheJob(t *testing.T) {
	store, svc := publishedThenEdited(t)

	rec := plotPublicationAs(t, store, &routing.FakePublisher{Err: routing.ErrNotConfirmed}, account.User{}, svc.Slug, isoValidBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.PublishFailedErrorCode {
		t.Errorf("code = %q, want %q", body["code"], handler.PublishFailedErrorCode)
	}
	job, ok := store.only()
	if !ok {
		t.Fatalf("want exactly one routing job recorded, got %d", store.count())
	}
	if job.Status != transit.JobStatusFailed {
		t.Errorf("routing job status = %q, want failed", job.Status)
	}
}

func edgeFrom(g *transit.TransitGraph) string {
	if g == nil || len(g.Services) != 1 || len(g.Services[0].Edges) != 1 {
		return ""
	}
	return g.Services[0].Edges[0].FromSlug
}
