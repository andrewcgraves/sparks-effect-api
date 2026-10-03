package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// A syntactically valid object whose one field runs past every cap below, so
// only the limit can be what rejects it.
func oversizedBody(field string) string {
	return `{"` + field + `":"` + strings.Repeat("x", 1<<20) + `"}`
}

func assertTooLarge(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body %s", rec.Code, rec.Body.String())
	}
	if got := errorField(t, rec); got != "request body too large" {
		t.Errorf("error = %q, want the shared 413 message", got)
	}
}

func TestLogin_413_beforeTheCredentialLookup(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.Login(store, time.Hour, testHasher), "/api/auth/login", oversizedBody("password"))

	assertTooLarge(t, rec)
	// Both bcrypt paths, the real comparison and VerifyNothing, sit behind
	// this lookup, so no lookup means no hashing.
	if store.lookups != 0 {
		t.Errorf("credential lookups = %d, want 0: an oversized body reached bcrypt", store.lookups)
	}
}

func TestCreateUser_413_createsNothing(t *testing.T) {
	store := newFakeAuthStore(t)
	rec := postJSON(t, handler.CreateUser(store, testHasher), "/api/admin/users", oversizedBody("name"))

	assertTooLarge(t, rec)
	if len(store.created) != 0 {
		t.Errorf("created %d users from an oversized body", len(store.created))
	}
}

func TestCreateRoute_413(t *testing.T) {
	big := `{"pad":"` + strings.Repeat("x", 9<<20) + `"}`
	assertTooLarge(t, postJSON(t, handler.CreateRoute(newFakeRouteStore()), "/api/admin/routes", big))
}

func TestIsochrone_413(t *testing.T) {
	pub := &routing.FakePublisher{}
	rec := postIsochrone(compiledStore(), pub, oversizedBody("scenario_slug"))

	assertTooLarge(t, rec)
	if len(pub.Messages()) != 0 {
		t.Error("an oversized body was enqueued")
	}
}

func TestPublicationIsochrone_413(t *testing.T) {
	store, svc := publishedThenEdited(t)
	pub := &routing.FakePublisher{}
	rec := plotPublicationAs(t, store, pub, account.User{}, svc.Slug, oversizedBody("mode"))

	assertTooLarge(t, rec)
	if store.count() != 0 || len(pub.Messages()) != 0 {
		t.Error("an oversized body was enqueued")
	}
}

func TestUserScenarioIsochrone_413(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	assertTooLarge(t, isoServeAs(t, store, &routing.FakePublisher{}, scnOwner,
		"/api/user-scenarios/trip/isochrone", oversizedBody("mode")))
}

func TestUserServiceIsochrone_413(t *testing.T) {
	// No store at all: the body is rejected before anything would touch one.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/services/{slug}/isochrone",
		handler.UserServiceIsochrone(nil, &routing.FakePublisher{}, logger.Discard(), transit.DefaultBoardingWaitPolicy()))
	req := httptest.NewRequest(http.MethodPost, "/api/services/any/isochrone", strings.NewReader(oversizedBody("mode")))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assertTooLarge(t, rec)
}

func TestCreatePrerenderedIsochrone_413(t *testing.T) {
	store := newFakePrerenderedStore()
	big := `{"label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":"` +
		strings.Repeat("x", 3<<20) + `"}`

	assertTooLarge(t, createPrerendered(t, store, preAdminTok, big))
	if len(store.entries) != 0 {
		t.Error("an oversized payload was stored")
	}
}

func TestWorkerCachePut_413_writesNothing(t *testing.T) {
	store := &fakeWorkerStore{}
	big := `{"entries":[{"key":{"compile_job_id":"c1","station_slug":"north","mode":"walk","contour_mins":30},` +
		`"geometry":{"pad":"` + strings.Repeat("x", 9<<20) + `"}}]}`

	assertTooLarge(t, postJSON(t, handler.WorkerCachePut(store), "/api/internal/isochrone-cache", big))
	if store.putEntries != nil {
		t.Error("an oversized put reached the store")
	}
}

// Tiny and numerous: the byte cap admits it, and only the count can refuse it
// before it becomes that many statements in one batch.
func manyCacheKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf(`{"compile_job_id":"c1","station_slug":"s%d","mode":"walk","contour_mins":30}`, i)
	}
	return keys
}

func manyCacheEntries(n int) string {
	keys := manyCacheKeys(n)
	for i, k := range keys {
		keys[i] = `{"key":` + k + `,"geometry":{}}`
	}
	return `{"entries":[` + strings.Join(keys, ",") + `]}`
}

func manyCacheLookupKeys(n int) string {
	return `{"keys":[` + strings.Join(manyCacheKeys(n), ",") + `]}`
}

func TestWorkerCachePut_400_tooManyEntries(t *testing.T) {
	store := &fakeWorkerStore{}

	rec := postJSON(t, handler.WorkerCachePut(store), "/api/internal/isochrone-cache", manyCacheEntries(1001))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	if got := errorField(t, rec); got != "entries: at most 1000 per put" {
		t.Errorf("error = %q, want the limit named", got)
	}
	if store.putEntries != nil {
		t.Error("an over-count put reached the store")
	}
}

func TestWorkerCachePut_204_atTheEntryCap(t *testing.T) {
	store := &fakeWorkerStore{}

	rec := postJSON(t, handler.WorkerCachePut(store), "/api/internal/isochrone-cache", manyCacheEntries(1000))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if len(store.putEntries) != 1000 {
		t.Errorf("put %d entries, want 1000", len(store.putEntries))
	}
}

func TestWorkerCacheLookup_413(t *testing.T) {
	store := &fakeWorkerStore{}
	big := `{"keys":[{"compile_job_id":"c1","station_slug":"` + strings.Repeat("x", 2<<20) + `","mode":"walk","contour_mins":30}]}`

	assertTooLarge(t, postJSON(t, handler.WorkerCacheLookup(store), "/api/internal/isochrone-cache/lookup", big))
	if store.gotKeys != nil {
		t.Error("an oversized lookup reached the store")
	}
}

func TestWorkerCacheLookup_400_tooManyKeys(t *testing.T) {
	store := &fakeWorkerStore{}

	rec := postJSON(t, handler.WorkerCacheLookup(store), "/api/internal/isochrone-cache/lookup", manyCacheLookupKeys(1001))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	if got := errorField(t, rec); got != "keys: at most 1000 per lookup" {
		t.Errorf("error = %q, want the limit named", got)
	}
	if store.gotKeys != nil {
		t.Error("an over-count lookup reached the store")
	}
}

func TestWorkerCacheLookup_200_atTheKeyCap(t *testing.T) {
	store := &fakeWorkerStore{}

	rec := postJSON(t, handler.WorkerCacheLookup(store), "/api/internal/isochrone-cache/lookup", manyCacheLookupKeys(1000))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if len(store.gotKeys) != 1000 {
		t.Errorf("looked up %d keys, want 1000", len(store.gotKeys))
	}
}

func TestWorkerMarkSucceeded_413_recordsNothing(t *testing.T) {
	store := &fakeWorkerStore{}
	big := `{"result":{"pad":"` + strings.Repeat("x", 9<<20) + `"}}`

	assertTooLarge(t, postJSON(t, handler.WorkerMarkSucceeded(store), "/api/internal/routing-jobs/j/succeeded", big))
	if store.result != nil {
		t.Error("an oversized result reached the store")
	}
}

// The largest real chain result must keep fitting: refusing it fails the job.
func TestWorkerMarkSucceeded_204_largestCommittedChainResult(t *testing.T) {
	raw, err := os.ReadFile("../transit/data/scenarios/ca-hsr/prerendered/isochrone-sj-240-bike.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	store := &fakeWorkerStore{}
	rec := postJSON(t, handler.WorkerMarkSucceeded(store), "/api/internal/routing-jobs/j/succeeded",
		`{"result":`+string(env.Result)+`}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
}

func TestWorkerMarkFailed_413_recordsNothing(t *testing.T) {
	store := &fakeWorkerStore{}

	assertTooLarge(t, postJSON(t, handler.WorkerMarkFailed(store), "/api/internal/routing-jobs/j/failed", oversizedBody("error")))
	if store.errMsg != "" {
		t.Error("an oversized failure reached the store")
	}
}

// The fixture is the put the San Jose 240-minute bike chain made: its twelve
// egress polygons, taken from that job's result (the committed prerendered
// isochrone-sj-240-bike.json) with the three properties the chain adds to each
// one stripped, which is the shape the worker caches. The limits are sized from
// it, so it must keep fitting under them.
func TestWorkerCachePut_204_realChainFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "cache-put-sj-240-bike.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	store := &fakeWorkerStore{}
	rec := postJSON(t, handler.WorkerCachePut(store), "/api/internal/isochrone-cache", string(body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 for a %d-byte put; body %s", rec.Code, len(body), rec.Body.String())
	}
	if len(store.putEntries) != 12 {
		t.Errorf("put %d entries, want the chain's 12", len(store.putEntries))
	}
}

// The cap is set from the committed payloads, so it must keep admitting the
// largest of them, including any added after it was chosen.
func TestCreatePrerenderedIsochrone_201_largestCommittedFixture(t *testing.T) {
	paths, err := filepath.Glob("../transit/data/scenarios/*/prerendered/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no committed prerendered fixtures found (err %v)", err)
	}
	var largest []byte
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if len(b) > len(largest) {
			largest = b
		}
	}

	store := newFakePrerenderedStore()
	rec := createPrerendered(t, store, preAdminTok, string(largest))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a %d-byte fixture; body %s",
			rec.Code, len(largest), rec.Body.String())
	}
}
