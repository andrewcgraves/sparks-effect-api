package ratelimit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
}

func withUser(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id != "" {
			r = r.WithContext(auth.WithUser(r.Context(), account.User{ID: id}))
		}
		next.ServeHTTP(w, r)
	})
}

func do(h http.Handler, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", http.NoBody)
	if remote != "" {
		req.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLimitUnderLimitReachesHandler(t *testing.T) {
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	})
	h := Limit(New(60, 5), ClientIP(0))(next)

	rec := do(h, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if !reached {
		t.Error("handler was not reached")
	}
}

func TestLimitOverLimitReturns429(t *testing.T) {
	var reached int
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	})
	h := Limit(New(1, 1), ClientIP(0))(next)

	if rec := do(h, ""); rec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", rec.Code)
	}

	rec := do(h, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if reached != 1 {
		t.Errorf("handler reached %d times, want 1", reached)
	}

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != ErrorCode {
		t.Errorf("code = %q, want %q", body.Code, ErrorCode)
	}
	if body.Error == "" {
		t.Error("the 429 carries no message")
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Errorf("Retry-After = %q, want an integer > 0", rec.Header().Get("Retry-After"))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestLimitDifferentIPsAreIndependent(t *testing.T) {
	h := Limit(New(1, 1), ClientIP(0))(okHandler())

	if rec := do(h, "192.0.2.1:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("first IP status = %d, want 202", rec.Code)
	}
	if rec := do(h, "192.0.2.2:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("second IP status = %d, want 202", rec.Code)
	}
}

func TestLimitSameIPSharesBucket(t *testing.T) {
	h := Limit(New(1, 1), ClientIP(0))(okHandler())

	if rec := do(h, "192.0.2.1:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", rec.Code)
	}
	if rec := do(h, "192.0.2.1:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP second status = %d, want 429", rec.Code)
	}
}

func TestLimitAuthenticatedUsersShareIPAndHaveSeparateUserBuckets(t *testing.T) {
	lim := New(1, 2)
	inner := Limit(lim, ClientIP(0))(okHandler())

	// Burst 2: two users on the same IP each get one request (IP still has
	// tokens, and each user bucket is independent). A third request from
	// either is refused by the shared IP bucket.
	alice := withUser("alice", inner)
	bob := withUser("bob", inner)
	remote := "192.0.2.10:1"

	if rec := do(alice, remote); rec.Code != http.StatusAccepted {
		t.Fatalf("alice first = %d, want 202", rec.Code)
	}
	if rec := do(bob, remote); rec.Code != http.StatusAccepted {
		t.Fatalf("bob first = %d, want 202; exhausting alice must not 429 bob while IP has tokens", rec.Code)
	}
	if rec := do(alice, remote); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alice third (IP exhausted) = %d, want 429", rec.Code)
	}
	if rec := do(bob, remote); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bob after IP exhausted = %d, want 429", rec.Code)
	}
}

func TestLimitExhaustedUserDoesNotBlockAnotherUserOnAFreshIP(t *testing.T) {
	lim := New(1, 1)
	inner := Limit(lim, ClientIP(0))(okHandler())
	alice := withUser("alice", inner)
	bob := withUser("bob", inner)

	if rec := do(alice, "192.0.2.1:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("alice = %d, want 202", rec.Code)
	}
	// Alice is out of user tokens; a new IP still has an IP token, but her
	// user bucket refuses her. Bob on that IP is a different user bucket.
	if rec := do(alice, "192.0.2.2:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alice on a new IP = %d, want 429 (user bucket exhausted)", rec.Code)
	}
	if rec := do(bob, "192.0.2.2:1"); rec.Code != http.StatusAccepted {
		t.Fatalf("bob on alice's leftover IP = %d, want 202", rec.Code)
	}
}

func TestLimitDisabledNever429s(t *testing.T) {
	h := Limit(New(0, 1), ClientIP(0))(okHandler())
	for i := 0; i < 20; i++ {
		if rec := do(h, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d, want 202 with the limiter disabled", i, rec.Code)
		}
	}
}

func TestLimitNilLimiterIsPassThrough(t *testing.T) {
	h := Limit(nil, ClientIP(0))(okHandler())
	if rec := do(h, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
}
