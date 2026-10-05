package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

var fixedNow = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

var (
	pollOwner    = account.User{ID: "owner-1", Email: "owner@example.com"}
	pollStranger = account.User{ID: "stranger-1", Email: "stranger@example.com"}
	pollAdmin    = account.User{ID: "admin-1", Email: "admin@example.com", IsAdmin: true}
)

func pollAs(t *testing.T, store handler.RoutingJobPollStore, id string, user account.User) *httptest.ResponseRecorder {
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

func TestRoutingJobStatus_ownerlessJobIsReadableByAnyone(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{ID: "job-public", Status: transit.JobStatusQueued})

	for _, tc := range []struct {
		name string
		user account.User
	}{
		{"anonymous", account.User{}},
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

func TestRoutingJobStatus_ownedJobIsReadableByAnAdmin(t *testing.T) {
	store := &fakeRoutingStore{}
	owner := pollOwner.ID
	store.put(transit.RoutingJob{ID: "job-owned", Status: transit.JobStatusQueued, OwnerID: &owner})

	rec := pollAs(t, store, "job-owned", pollAdmin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func TestRoutingJobStatus_ownedJobIsNotFoundForAnyoneElse(t *testing.T) {
	store := &fakeRoutingStore{}
	owner := pollOwner.ID
	store.put(transit.RoutingJob{ID: "job-owned", Status: transit.JobStatusQueued, OwnerID: &owner})

	for _, tc := range []struct {
		name string
		user account.User
	}{
		{"anonymous", account.User{}},
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

func TestRoutingJobStatus_succeededJobCarriesItsResult(t *testing.T) {
	store := &fakeRoutingStore{}
	result := json.RawMessage(`{"type":"FeatureCollection","features":[]}`)
	store.put(transit.RoutingJob{
		ID: "job-done", Status: transit.JobStatusSucceeded, Result: result,
	})

	rec := pollAs(t, store, "job-done", account.User{})
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

func TestRoutingJobStatus_failedJobCarriesItsError(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-failed", Status: transit.JobStatusFailed, Error: "the isochrone was never enqueued",
	})

	rec := pollAs(t, store, "job-failed", account.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := decodeRoutingJob(t, rec).Error; got == "" {
		t.Error("a failed routing job came back with no error to show the user")
	}
}

func TestRoutingJobStatus_staleQueuedJobIsFailed(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-stale", Status: transit.JobStatusQueued,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale", account.User{})
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

func TestRoutingJobStatus_staleRunningJobIsFailed(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-stale-running", Status: transit.JobStatusRunning,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale-running", account.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := decodeRoutingJob(t, rec).Status; got != transit.JobStatusFailed {
		t.Errorf("status = %q, want failed", got)
	}
}

func TestRoutingJobStatus_freshQueuedJobIsUntouched(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{
		ID: "job-fresh", Status: transit.JobStatusQueued, CreatedAt: time.Now(),
	})

	rec := pollAs(t, store, "job-fresh", account.User{})
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

			rec := pollAs(t, store, tc.job.ID, account.User{})
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

func TestRoutingJobStatus_staleJobLeftUnchangedWhenTheFailWriteFails(t *testing.T) {
	store := &fakeRoutingStore{failErr: fmt.Errorf("database is on fire")}
	store.put(transit.RoutingJob{
		ID: "job-stale", Status: transit.JobStatusQueued,
		CreatedAt: time.Now().Add(-2 * handler.RoutingJobStaleAfter),
	})

	rec := pollAs(t, store, "job-stale", account.User{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := decodeRoutingJob(t, rec).Status; got != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued (unchanged, since the fail write itself failed)", got)
	}
}

// --- the queue position (SPA-437) ---

func pollFields(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var fields map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return fields
}

func TestRoutingJobStatus_queuedJobCarriesItsQueuePosition(t *testing.T) {
	createdAt := time.Now().Add(-10 * time.Second)
	store := &fakeRoutingStore{ahead: 2}
	store.put(transit.RoutingJob{ID: "job-third", Status: transit.JobStatusQueued, CreatedAt: createdAt})

	fields := pollFields(t, pollAs(t, store, "job-third", account.User{}))
	if got := fields["queue_position"]; got != float64(2) {
		t.Errorf("queue_position = %v, want 2", got)
	}
	// Ahead means in flight and created earlier, so it is counted over the
	// backlog's own window from this job's created_at.
	if !store.aheadOf.Equal(createdAt) {
		t.Errorf("counted jobs ahead of %v, want this job's created_at %v", store.aheadOf, createdAt)
	}
	if store.aheadWithin != handler.RoutingJobStaleAfter {
		t.Errorf("counted over %v, want RoutingJobStaleAfter (%v)", store.aheadWithin, handler.RoutingJobStaleAfter)
	}
}

func TestRoutingJobStatus_queuedJobAtTheFrontIsPositionZero(t *testing.T) {
	store := &fakeRoutingStore{}
	store.put(transit.RoutingJob{ID: "job-next", Status: transit.JobStatusQueued, CreatedAt: time.Now()})

	// Zero is an answer, not an absence: a missing field means the job is not
	// waiting in the queue at all.
	fields := pollFields(t, pollAs(t, store, "job-next", account.User{}))
	if got, ok := fields["queue_position"]; !ok || got != float64(0) {
		t.Errorf("queue_position = %v (present %v), want 0", got, ok)
	}
}

func TestRoutingJobStatus_onlyAQueuedJobCarriesAQueuePosition(t *testing.T) {
	stale := time.Now().Add(-2 * handler.RoutingJobStaleAfter)
	for _, tc := range []struct {
		name string
		job  transit.RoutingJob
	}{
		{"running", transit.RoutingJob{ID: "job-running", Status: transit.JobStatusRunning, CreatedAt: time.Now()}},
		{"succeeded", transit.RoutingJob{ID: "job-succeeded", Status: transit.JobStatusSucceeded, CreatedAt: time.Now()}},
		{"failed", transit.RoutingJob{ID: "job-failed", Status: transit.JobStatusFailed, CreatedAt: time.Now()}},
		{"queued but failed for staleness", transit.RoutingJob{ID: "job-stale", Status: transit.JobStatusQueued, CreatedAt: stale}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRoutingStore{ahead: 3}
			store.put(tc.job)

			fields := pollFields(t, pollAs(t, store, tc.job.ID, account.User{}))
			if got, ok := fields["queue_position"]; ok {
				t.Errorf("queue_position = %v, want none for a %v job", got, fields["status"])
			}
		})
	}
}

func TestRoutingJobStatus_queuePositionIsLeftOutWhenItCannotBeCounted(t *testing.T) {
	store := &fakeRoutingStore{aheadErr: fmt.Errorf("database is on fire")}
	store.put(transit.RoutingJob{ID: "job-queued", Status: transit.JobStatusQueued, CreatedAt: time.Now()})

	// The position is a courtesy; failing the poll over it would turn a slow
	// count into a visitor's failed isochrone.
	fields := pollFields(t, pollAs(t, store, "job-queued", account.User{}))
	if got, ok := fields["queue_position"]; ok {
		t.Errorf("queue_position = %v, want none when the count failed", got)
	}
	if fields["status"] != transit.JobStatusQueued {
		t.Errorf("status = %v, want queued", fields["status"])
	}
}
