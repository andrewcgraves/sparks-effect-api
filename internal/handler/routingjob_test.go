package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fixedNow is what the fake store stamps onto rows it "persists", so tests can
// tell a row that went through the store from one the handler invented.
var fixedNow = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

var (
	pollOwner    = transit.User{ID: "owner-1", Email: "owner@example.com"}
	pollStranger = transit.User{ID: "stranger-1", Email: "stranger@example.com"}
	pollAdmin    = transit.User{ID: "admin-1", Email: "admin@example.com", IsAdmin: true}
)

// pollAs issues GET /api/routing-jobs/{id} as user, or anonymously when user is
// the zero value — the anonymous case being the one auth.OptionalAuth exists
// for.
func pollAs(t *testing.T, store handler.RoutingStore, id string, user transit.User) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/routing-jobs/{id}", handler.RoutingJobStatus(store))

	r := httptest.NewRequest(http.MethodGet, "/api/routing-jobs/"+id, nil)
	if user.ID != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

func decodeRoutingJob(t *testing.T, rec *httptest.ResponseRecorder) transit.RoutingJob {
	t.Helper()
	var job transit.RoutingJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return job
}

// --- the poll's ownership rule ---

// An ownerless job came from the public /api/isochrone, which anyone may call.
// There is no identity to match it against, so holding the id is the whole of
// the credential — and the id is an unguessable v4 UUID.
func TestRoutingJobStatus_ownerlessJobIsReadableByAnyone(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{ID: "job-public", Status: transit.JobStatusQueued})

	for _, tc := range []struct {
		name string
		user transit.User
	}{
		{"anonymous", transit.User{}},
		{"some other user", pollStranger},
		{"an admin", pollAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := pollAs(t, store, "job-public", tc.user)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if got := decodeRoutingJob(t, rec).ID; got != "job-public" {
				t.Errorf("id = %q, want job-public", got)
			}
		})
	}
}

func TestRoutingJobStatus_ownedJobIsReadableByItsOwner(t *testing.T) {
	store := &fakeRoutingStore{}
	owner := pollOwner.ID
	store.put(transit.RoutingJob{ID: "job-owned", Status: transit.JobStatusQueued, OwnerID: &owner})

	rec := pollAs(t, store, "job-owned", pollOwner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

// An admin may view any job, matching the compile job poll's rule.
func TestRoutingJobStatus_ownedJobIsReadableByAnAdmin(t *testing.T) {
	store := &fakeRoutingStore{}
	owner := pollOwner.ID
	store.put(transit.RoutingJob{ID: "job-owned", Status: transit.JobStatusQueued, OwnerID: &owner})

	rec := pollAs(t, store, "job-owned", pollAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

// Everyone else gets the same 404 as an unknown id, so a caller cannot probe
// which job ids exist by watching the status code change.
func TestRoutingJobStatus_ownedJobIsNotFoundForAnyoneElse(t *testing.T) {
	store := &fakeRoutingStore{}
	owner := pollOwner.ID
	store.put(transit.RoutingJob{ID: "job-owned", Status: transit.JobStatusQueued, OwnerID: &owner})

	for _, tc := range []struct {
		name string
		user transit.User
	}{
		{"anonymous", transit.User{}},
		{"a different user", pollStranger},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := pollAs(t, store, "job-owned", tc.user)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
			}
			// The 404 must be indistinguishable from an unknown id, body included.
			unknown := pollAs(t, store, "no-such-job", tc.user)
			if rec.Body.String() != unknown.Body.String() {
				t.Errorf("a non-owner's 404 differs from an unknown id's:\n owned: %s\n unknown: %s",
					rec.Body.String(), unknown.Body.String())
			}
		})
	}
}

func TestRoutingJobStatus_404_unknownID(t *testing.T) {
	rec := pollAs(t, &fakeRoutingStore{}, "no-such-job", pollOwner)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRoutingJobStatus_500_storeFailure(t *testing.T) {
	store := &fakeRoutingStore{lookupErr: fmt.Errorf("database is on fire")}

	rec := pollAs(t, store, "job-1", pollOwner)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// The point of the poll: once the worker has finished, the result comes back
// with the job rather than from a second endpoint.
func TestRoutingJobStatus_succeededJobCarriesItsResult(t *testing.T) {
	store := &fakeRoutingStore{}
	result := json.RawMessage(`{"type":"FeatureCollection","features":[]}`)
	store.put(transit.RoutingJob{
		ID: "job-done", Status: transit.JobStatusSucceeded, Result: result,
	})

	rec := pollAs(t, store, "job-done", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.Status != transit.JobStatusSucceeded {
		t.Errorf("status = %q, want succeeded", job.Status)
	}
	var got map[string]any
	if err := json.Unmarshal(job.Result, &got); err != nil {
		t.Fatalf("result is not the JSON the worker wrote: %v", err)
	}
	if got["type"] != "FeatureCollection" {
		t.Errorf("result = %v, want the worker's own payload passed through unaltered", got)
	}
}

// A failed job reports why. Without this a client polling a job the broker
// never accepted would see "failed" and have nothing to show for it.
func TestRoutingJobStatus_failedJobCarriesItsError(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-failed", Status: transit.JobStatusFailed, Error: "the isochrone was never enqueued",
	})

	rec := pollAs(t, store, "job-failed", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := decodeRoutingJob(t, rec).Error; got == "" {
		t.Error("a failed routing job came back with no error to show the user")
	}
}
