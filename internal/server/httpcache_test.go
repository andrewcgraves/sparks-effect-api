package server

import (
	"cmp"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const wantPublicCacheControl = "public, max-age=60, stale-while-revalidate=600"

func conditionalGet(t *testing.T, h http.Handler, path, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCuratedScenarioReadsAreCacheableAndRevalidate(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	for _, path := range []string{
		"/api/scenarios",
		"/api/scenarios/ca-hsr",
		"/api/scenarios/ca-hsr/routes",
		"/api/scenarios/ca-hsr/services",
		"/api/scenarios/ca-hsr/stations",
		"/api/scenarios/ca-hsr/travel-times",
	} {
		t.Run(path, func(t *testing.T) {
			rec := request(t, h, http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != wantPublicCacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, wantPublicCacheControl)
			}
			etag := rec.Header().Get("ETag")
			if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 3 {
				t.Fatalf("ETag = %q, want a strong, quoted tag", etag)
			}

			again := conditionalGet(t, h, path, etag)
			if again.Code != http.StatusNotModified {
				t.Fatalf("revalidation status = %d, want 304", again.Code)
			}
			if again.Body.Len() != 0 {
				t.Errorf("304 carried a body: %s", again.Body.String())
			}
			if got := again.Header().Get("ETag"); got != etag {
				t.Errorf("304 ETag = %q, want %q", got, etag)
			}

			if stale := conditionalGet(t, h, path, `"some-other-build"`); stale.Code != http.StatusOK {
				t.Errorf("mismatched If-None-Match: status = %d, want 200", stale.Code)
			}
		})
	}
}

func TestCuratedReadsDoNotCacheANotFound(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/scenarios/no-such-scenario", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Header().Get("Cache-Control"), "public") || rec.Header().Get("ETag") != "" {
		t.Errorf("a 404 was made cacheable: Cache-Control %q ETag %q",
			rec.Header().Get("Cache-Control"), rec.Header().Get("ETag"))
	}
}

// A shared cache that stored a public read fetched without an Origin — no
// Access-Control-Allow-Origin on it — must not hand that copy to the website,
// whose browser would then refuse it.
func TestPublicReadsVaryByOriginEvenWithoutOne(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	rec := request(t, h, http.MethodGet, "/api/scenarios", "")
	if !slices.Contains(rec.Header().Values("Vary"), "Origin") {
		t.Errorf("Vary = %q, want it to name Origin", rec.Header().Values("Vary"))
	}
}

// The reads deliberately opened to shared caches. Anything not listed here
// must come back private, whoever asks and whatever it answers.
var publiclyCacheable = map[string]bool{
	"GET /api/scenarios":                               true,
	"GET /api/scenarios/{slug}":                        true,
	"GET /api/scenarios/{slug}/routes":                 true,
	"GET /api/scenarios/{slug}/services":               true,
	"GET /api/scenarios/{slug}/stations":               true,
	"GET /api/scenarios/{slug}/travel-times":           true,
	"GET /api/scenarios/{slug}/prerendered-isochrones": true,
	"GET /api/prerendered-isochrones/{id}":             true,
	"GET /api/published-services":                      true,
	"GET /api/services/{slug}/publication":             true,
	// Not tagged: a live reading with nothing to revalidate against, so it
	// sets its own short max-age (SPA-442) and carries no identity.
	"GET /api/routing/status": true,
}

func TestEveryRouteButThePublicReadsIsPrivate(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour, WorkerToken: workerToken}
	deps := newStubDeps()
	h, patterns := routes(cfg, store, deps, &routing.FakePublisher{}, compile.NewRunner(deps, cfg.BoardingWait, nil), logger.Discard(), nil, nil)

	if len(patterns) < 50 {
		t.Fatalf("walked %d patterns; the table was not recorded", len(patterns))
	}
	for p := range publiclyCacheable {
		if !slices.Contains(patterns, p) {
			t.Errorf("public read %q is not registered; the list above is stale", p)
		}
	}

	for _, pattern := range patterns {
		if publiclyCacheable[pattern] {
			continue
		}
		method, path, found := strings.Cut(pattern, " ")
		if !found {
			method, path = http.MethodGet, pattern
		}
		path = placeholder.ReplaceAllString(path, "x")
		body := ""
		if method != http.MethodGet && method != http.MethodDelete {
			body = "{}"
		}
		for _, token := range []string{"", userToken, adminToken, workerToken} {
			t.Run(pattern+" as "+cmp.Or(token, "anonymous"), func(t *testing.T) {
				rec := request(t, h, method, path, token, body)
				if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
					t.Errorf("status %d: Cache-Control = %q, want private, no-store", rec.Code, cc)
				}
			})
		}
	}
}

var placeholder = regexp.MustCompile(`\{[^}]+\}`)

// The OptionalAuth reads answer per caller, so even their anonymous answer —
// the curated alignments, a curated graph — must not be stored for the next
// caller.
func TestOptionalAuthReadsArePrivateEvenWhenTheyAnswer(t *testing.T) {
	h := newTestServer(t, newStubDeps())
	for _, path := range []string{"/api/routes", "/api/routes/x", "/api/scenarios/ca-hsr/graph", "/api/routing-jobs/x"} {
		rec := request(t, h, http.MethodGet, path, "")
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Errorf("%s: status %d, Cache-Control = %q, want private, no-store", path, rec.Code, cc)
		}
	}
}
