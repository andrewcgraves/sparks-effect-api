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

// --- staleness (SPA-230): the isochrone service being down, with no worker to
// ever consume the message ---

// A job that has sat queued well past RoutingJobStaleAfter with no worker
// ever picking it up is the shape of the outage this exists for: the broker
// took the message, so enqueueIsochrone's publish-failure path never fired,
// and nothing else was ever going to tell the caller.
func TestRoutingJobStatus_staleQueuedJobIsFailed(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-stale", Status: transit.JobStatusQueued,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.Status != transit.JobStatusFailed {
		t.Errorf("status = %q, want failed", job.Status)
	}
	if job.Error == "" {
		t.Error("a stale routing job came back with no error to show the user")
	}

	// The write must have actually landed, not just been reflected in this
	// response — the next poll (by this caller or another) has to see the same
	// answer, not resurrect the job back to queued.
	persisted, ok := store.only()
	if !ok {
		t.Fatalf("want exactly one routing job recorded, got %d", store.count())
	}
	if persisted.Status != transit.JobStatusFailed {
		t.Errorf("persisted status = %q, want failed", persisted.Status)
	}
}

// A worker that claimed the job and then vanished — the process died, the pod
// was killed — leaves it stuck `running` exactly the way an unconsumed message
// leaves it stuck `queued`. Both are "the service is not answering" from a
// caller's point of view, so both are caught the same way.
func TestRoutingJobStatus_staleRunningJobIsFailed(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-stale-running", Status: transit.JobStatusRunning,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale-running", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := decodeRoutingJob(t, rec).Status; got != transit.JobStatusFailed {
		t.Errorf("status = %q, want failed", got)
	}
}

// A job still well within RoutingJobStaleAfter must not be touched — this is
// the ordinary "sitting behind another user's request" wait the frontend's own
// deadline already tolerates, not an outage.
func TestRoutingJobStatus_freshQueuedJobIsUntouched(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-fresh", Status: transit.JobStatusQueued, CreatedAt: time.Now(),
	})

	rec := pollAs(t, store, "job-fresh", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.Status != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued", job.Status)
	}
	if job.Error != "" {
		t.Errorf("error = %q, want none for a job well within its deadline", job.Error)
	}
}

// A succeeded or failed job is terminal and must never be rewritten by the
// staleness check, however old it is — a result the worker actually computed
// must not be clobbered by a poll that happens to land late.
func TestRoutingJobStatus_terminalJobsAreNeverRewrittenForStaleness(t *testing.T) {
	old := time.Now().Add(-2 * handler.RoutingJobStaleAfter)
	result := json.RawMessage(`{"type":"FeatureCollection","features":[]}`)

	for _, tc := range []struct {
		name string
		job  transit.RoutingJob
	}{
		{"succeeded", transit.RoutingJob{ID: "job-old-succeeded", Status: transit.JobStatusSucceeded, Result: result, CreatedAt: old}},
		{"failed", transit.RoutingJob{ID: "job-old-failed", Status: transit.JobStatusFailed, Error: "some real reason", CreatedAt: old}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRoutingStore{}
			store.put(tc.job)

			rec := pollAs(t, store, tc.job.ID, transit.User{})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			got := decodeRoutingJob(t, rec)
			if got.Status != tc.job.Status {
				t.Errorf("status = %q, want unchanged %q", got.Status, tc.job.Status)
			}
			if got.Error != tc.job.Error {
				t.Errorf("error = %q, want unchanged %q", got.Error, tc.job.Error)
			}
		})
	}
}

// A store that cannot record the failure must not tell the caller it happened
// anyway — the job comes back exactly as it was, so the next poll gets another
// chance to persist it rather than a caller believing a failure that never
// made it to the database.
func TestRoutingJobStatus_staleJobLeftUnchangedWhenTheFailWriteFails(t *testing.T) {
	store := &fakeRoutingStore{failErr: fmt.Errorf("database is on fire")}
	store.put(transit.RoutingJob{
		ID: "job-stale", Status: transit.JobStatusQueued,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale", transit.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := decodeRoutingJob(t, rec).Status; got != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued (unchanged, since the fail write itself failed)", got)
	}
}
