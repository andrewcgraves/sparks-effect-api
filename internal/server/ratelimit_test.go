package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestClientIP_usesLastForwardedForEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	// A client claiming to be 1.2.3.4 through a proxy that appended the real
	// address after it — the trusted (last) entry is what must be used, not
	// whatever the client put first (SPA-198).
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 9.9.9.9")

	got := clientIP(req)
	if got != "9.9.9.9" {
		t.Errorf("clientIP: want %q, got %q", "9.9.9.9", got)
	}
}

func TestClientIP_fallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:5555"

	got := clientIP(req)
	if got != "203.0.113.7" {
		t.Errorf("clientIP: want %q, got %q", "203.0.113.7", got)
	}
}

func TestIPRateLimiter_perIPIsolation(t *testing.T) {
	limiter := newIPRateLimiter(rate.Limit(0), 1, time.Minute)

	if !limiter.allow("1.1.1.1") {
		t.Fatal("first request from 1.1.1.1 should be allowed (burst)")
	}
	if limiter.allow("1.1.1.1") {
		t.Fatal("second immediate request from 1.1.1.1 should be throttled")
	}
	if !limiter.allow("2.2.2.2") {
		t.Fatal("a different IP should have its own untouched bucket")
	}
}

func TestRateLimit_middlewareAnswers429WhenExceeded(t *testing.T) {
	limiter := newIPRateLimiter(rate.Limit(0), 1, time.Minute)
	called := 0
	h := rateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/analytics/events", nil)
	req.RemoteAddr = "5.5.5.5:1"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: want 429, got %d", rec2.Code)
	}
	if called != 1 {
		t.Errorf("wrapped handler: want 1 call, got %d", called)
	}
}
