package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

// fakeBacklogStore is a handler.RoutingBacklogStore whose answer the test sets.
// It records the window it was asked for, which is the one argument the cap
// chooses rather than passes through.
type fakeBacklogStore struct {
	mu     sync.Mutex
	count  int
	err    error
	calls  int
	window time.Duration
}

func (f *fakeBacklogStore) CountInFlightRoutingJobs(_ context.Context, within time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.window = within
	return f.count, f.err
}

func (f *fakeBacklogStore) set(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count = n
}

// capped wraps a handler that records whether it ran, which is what "admitted"
// means here — the cap's whole job is deciding that.
func capped(t *testing.T, store handler.RoutingBacklogStore, limit int) (http.Handler, *bool) {
	t.Helper()
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	})
	return handler.CapIsochroneBacklog(store, limit, logger.Discard())(next), &reached
}

func postCapped(h http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/isochrone", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The threshold is a ceiling, not a target: the backlog is allowed to reach the
// limit and is refused from there on, so the limit is the most that can ever be
// outstanding.
func TestCapIsochroneBacklogThresholdCrossing(t *testing.T) {
	tests := []struct {
		name     string
		inFlight int
		limit    int
		want     int
	}{
		{"empty backlog", 0, 3, http.StatusAccepted},
		{"one below the limit", 2, 3, http.StatusAccepted},
		{"at the limit", 3, 3, http.StatusTooManyRequests},
		{"past the limit", 9, 3, http.StatusTooManyRequests},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeBacklogStore{count: tc.inFlight}
			h, reached := capped(t, store, tc.limit)

			rec := postCapped(h)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if admitted := tc.want == http.StatusAccepted; *reached != admitted {
				t.Errorf("handler reached = %v, want %v", *reached, admitted)
			}
		})
	}
}

// A refusal has to say enough for a client to act: a code it can branch on
// rather than parse prose for, and a Retry-After telling it when to come back.
func TestCapIsochroneBacklogRefusalCarriesCodeAndRetryAfter(t *testing.T) {
	store := &fakeBacklogStore{count: 5}
	h, _ := capped(t, store, 5)

	rec := postCapped(h)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != handler.BacklogFullErrorCode {
		t.Errorf("code = %q, want %q", body.Code, handler.BacklogFullErrorCode)
	}
	if body.Error == "" {
		t.Error("the 429 carries no message; this text reaches the user's error banner")
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("no Retry-After on the 429; a client has nothing to wait on")
	}

	// The window the cap counts over is the one the poll gives up after, so a
	// job nobody is working on stops counting rather than wedging the endpoint.
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.window != handler.RoutingJobStaleAfter {
		t.Errorf("counted over %v, want RoutingJobStaleAfter (%v)", store.window, handler.RoutingJobStaleAfter)
	}
}

// Recovery is the other half of the cap: nothing resets it, so the backlog
// falling below the limit has to be enough on its own to let work through
// again.
func TestCapIsochroneBacklogRecovers(t *testing.T) {
	store := &fakeBacklogStore{count: 4}
	h, reached := capped(t, store, 4)

	if rec := postCapped(h); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("full backlog: status = %d, want 429", rec.Code)
	}

	// The worker finishes one; nothing else happens.
	store.set(3)

	*reached = false
	if rec := postCapped(h); rec.Code != http.StatusAccepted {
		t.Fatalf("after the backlog drained: status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if !*reached {
		t.Error("the handler was not reached after the backlog drained")
	}
}

// A count that cannot be read admits the request. This is a protective cap, not
// a correctness gate: failing it closed would take the isochrone down over a
// read nothing else on the path needed.
func TestCapIsochroneBacklogAdmitsWhenTheCountFails(t *testing.T) {
	store := &fakeBacklogStore{count: 100, err: errors.New("database is down")}
	h, reached := capped(t, store, 1)

	rec := postCapped(h)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (fail open); body %s", rec.Code, rec.Body.String())
	}
	if !*reached {
		t.Error("the handler was not reached; an unreadable count must not refuse the request")
	}
}

// A limit of zero or less is the documented off switch, and off means the store
// is not even asked.
func TestCapIsochroneBacklogDisabled(t *testing.T) {
	for _, limit := range []int{0, -1} {
		store := &fakeBacklogStore{count: 1000}
		h, reached := capped(t, store, limit)

		if rec := postCapped(h); rec.Code != http.StatusAccepted {
			t.Errorf("limit %d: status = %d, want 202", limit, rec.Code)
		}
		if !*reached {
			t.Errorf("limit %d: the handler was not reached", limit)
		}
		store.mu.Lock()
		if store.calls != 0 {
			t.Errorf("limit %d: counted the backlog %d times with the cap disabled", limit, store.calls)
		}
		store.mu.Unlock()
	}
}
