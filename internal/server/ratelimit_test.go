package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/ratelimit"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func tinyPolicy() config.RateLimitPolicy {
	return config.RateLimitPolicy{RatePerMinute: 1, Burst: 1}
}

func newRateLimitedServer(t *testing.T, deps AuthDeps, cfg config.Config) http.Handler {
	t.Helper()
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg.Port = "8080"
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = time.Hour
	}
	return New(cfg, store, deps, &routing.FakePublisher{}, logger.Discard()).Handler
}

func assertRateLimited(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Code != ratelimit.ErrorCode {
		t.Errorf("code = %q, want %q (not %q)", body.Code, ratelimit.ErrorCode, handler.BacklogFullErrorCode)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Errorf("Retry-After = %q, want an integer > 0", rec.Header().Get("Retry-After"))
	}
}

func requestFrom(t *testing.T, h http.Handler, method, path, token, remote, xff, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, http.NoBody)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const loginBody = `{"email":"a@b.c","password":"x"}`

const snapStopsBody = `{"stops":[{"lat":37.79,"lng":-122.4}]}`

func TestIsochroneSecondRequestFromSameIPIsRateLimited(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitIsochrone: tinyPolicy(),
	})

	first := request(t, h, http.MethodPost, "/api/isochrone", "", isochroneBody)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first request was 429; body %s", first.Body.String())
	}

	second := request(t, h, http.MethodPost, "/api/isochrone", "", isochroneBody)
	assertRateLimited(t, second)
}

func TestIsochroneEndpointsShareOneLimiter(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitIsochrone: tinyPolicy(),
	})

	first := request(t, h, http.MethodPost, "/api/isochrone", "", isochroneBody)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("anonymous isochrone was 429; body %s", first.Body.String())
	}

	// Same limiter instance as POST /api/isochrone, so SPA-357 wrapping this
	// route with OptionalAuth still inherits the anonymous isochrone policy.
	second := request(t, h, http.MethodPost, "/api/services/some-slug/isochrone", userToken, isochroneBody)
	assertRateLimited(t, second)
}

func TestLoginSecondRequestFromSameIPIsRateLimited(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitLogin: tinyPolicy(),
	})

	first := request(t, h, http.MethodPost, "/api/auth/login", "", loginBody)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first request was 429; body %s", first.Body.String())
	}

	second := request(t, h, http.MethodPost, "/api/auth/login", "", loginBody)
	assertRateLimited(t, second)
}

func TestSnapStopsSecondRequestFromSameIPIsRateLimited(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitSnapStops: tinyPolicy(),
	})

	first := request(t, h, http.MethodPost, "/api/routes/no-such-route/snap-stops", "", snapStopsBody)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first request was 429; body %s", first.Body.String())
	}

	second := request(t, h, http.MethodPost, "/api/routes/no-such-route/snap-stops", "", snapStopsBody)
	assertRateLimited(t, second)
}

func TestAuthenticatedIsochroneAndCompileAreRateLimited(t *testing.T) {
	for _, path := range []string{
		"/api/services/some-slug/isochrone",
		"/api/user-scenarios/some-slug/isochrone",
	} {
		t.Run(path, func(t *testing.T) {
			h := newRateLimitedServer(t, newStubDeps(), config.Config{
				RateLimitIsochrone: tinyPolicy(),
			})
			first := request(t, h, http.MethodPost, path, userToken, isochroneBody)
			if first.Code == http.StatusTooManyRequests {
				t.Fatalf("first request was 429; body %s", first.Body.String())
			}
			assertRateLimited(t, request(t, h, http.MethodPost, path, userToken, isochroneBody))
		})
	}

	for _, path := range []string{
		"/api/scenarios/ca-hsr/compile",
		"/api/services/some-slug/compile",
		"/api/user-scenarios/some-slug/compile",
	} {
		t.Run(path, func(t *testing.T) {
			h := newRateLimitedServer(t, newStubDeps(), config.Config{
				RateLimitCompile: tinyPolicy(),
			})
			first := request(t, h, http.MethodPost, path, userToken, "")
			if first.Code == http.StatusTooManyRequests {
				t.Fatalf("first request was 429; body %s", first.Body.String())
			}
			assertRateLimited(t, request(t, h, http.MethodPost, path, userToken, ""))
		})
	}
}

func TestPublicReadsAndPollingAreNotRateLimited(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitIsochrone: tinyPolicy(),
		RateLimitSnapStops: tinyPolicy(),
		RateLimitLogin:     tinyPolicy(),
		RateLimitCompile:   tinyPolicy(),
	})

	for _, path := range []string{"/api/scenarios", "/healthz", "/api/routing-jobs/some-id"} {
		t.Run(path, func(t *testing.T) {
			for i := 0; i < 8; i++ {
				rec := request(t, h, http.MethodGet, path, "")
				if rec.Code == http.StatusTooManyRequests {
					t.Fatalf("request %d: status = 429 on a route that must not be limited; body %s",
						i, rec.Body.String())
				}
			}
		})
	}
}

func TestRateLimitPoliciesDoNotShareBuckets(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitIsochrone: tinyPolicy(),
		RateLimitLogin:     tinyPolicy(),
	})

	if rec := request(t, h, http.MethodPost, "/api/auth/login", "", loginBody); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("login first was 429; body %s", rec.Body.String())
	}
	if rec := request(t, h, http.MethodPost, "/api/auth/login", "", loginBody); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login second status = %d, want 429", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, "/api/isochrone", "", isochroneBody); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("isochrone was 429 after login was exhausted; policies must not share buckets")
	}
}

func TestZeroConfigDoesNotRateLimit(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	for i := 0; i < 5; i++ {
		if rec := request(t, h, http.MethodPost, "/api/isochrone", "", isochroneBody); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: zero Config must leave limiters disabled; body %s", i, rec.Body.String())
		}
	}
}

func TestRateLimitSpoofedXFFSharesTheRealClientBucket(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		TrustedProxyCount:  1,
		RateLimitIsochrone: tinyPolicy(),
	})
	const proxy = "10.0.0.1:443"

	first := requestFrom(t, h, http.MethodPost, "/api/isochrone", "", proxy, "1.1.1.1, 203.0.113.10", isochroneBody)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("first request was 429; body %s", first.Body.String())
	}

	spoofed := requestFrom(t, h, http.MethodPost, "/api/isochrone", "", proxy, "9.9.9.9, 203.0.113.10", isochroneBody)
	assertRateLimited(t, spoofed)

	other := requestFrom(t, h, http.MethodPost, "/api/isochrone", "", proxy, "9.9.9.9, 203.0.113.11", isochroneBody)
	if other.Code == http.StatusTooManyRequests {
		t.Fatalf("a different real client was 429d; body %s", other.Body.String())
	}
}

func TestAuthoredIsochronesStill401BeforeTheRateLimit(t *testing.T) {
	h := newRateLimitedServer(t, newStubDeps(), config.Config{
		RateLimitIsochrone: tinyPolicy(),
	})

	for _, path := range []string{
		"/api/services/some-slug/isochrone",
		"/api/user-scenarios/some-slug/isochrone",
	} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodPost, path, "", isochroneBody)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}
