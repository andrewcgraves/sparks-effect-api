package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/traceid"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeSeededGraphStore stands in for the repository behind the public
// isochrone: seeded scenarios by slug, the compile job that carries each one's
// graph, and — since SPA-182 — the routing jobs it enqueues.
type fakeSeededGraphStore struct {
	fakeRoutingStore

	scenarios map[string]transit.Scenario
	jobs      map[string]transit.Job
	err       error
}

func newFakeSeededGraphStore() *fakeSeededGraphStore {
	return &fakeSeededGraphStore{
		scenarios: map[string]transit.Scenario{},
		jobs:      map[string]transit.Job{},
	}
}

func (f *fakeSeededGraphStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	if f.err != nil {
		return transit.Scenario{}, false, f.err
	}
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

func (f *fakeSeededGraphStore) GetLatestSucceededJob(_ context.Context, scenarioSlug, kind string) (transit.Job, bool, error) {
	if f.err != nil {
		return transit.Job{}, false, f.err
	}
	if kind != transit.JobKindCompileScenario {
		return transit.Job{}, false, nil
	}
	job, ok := f.jobs[scenarioSlug]
	return job, ok, nil
}

// compiledStore is a scenario with a compiled graph behind it — the shape a
// booted, seeded deployment is always in.
func compiledStore() *fakeSeededGraphStore {
	f := newFakeSeededGraphStore()
	f.scenarios["ca-hsr"] = transit.Scenario{ID: "sc-1", Slug: "ca-hsr", Name: "CA HSR"}
	f.jobs["ca-hsr"] = transit.Job{
		ID: "compile-job-1", Kind: transit.JobKindCompileScenario, Status: transit.JobStatusSucceeded,
		Result: freshGraph(),
	}
	return f
}

func postIsochrone(store handler.SeededGraphStore, pub routing.Publisher, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/isochrone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Isochrone(store, pub, logger.Discard())(rec, req)
	return rec
}

const validIsochroneBody = `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`

func errorField(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body["error"]
}

// The endpoint no longer answers with a computed isochrone. It answers 202 with
// the routing job the caller polls — the whole of SPA-182 from the client's
// side.
func TestIsochrone_202_enqueuesARoutingJob(t *testing.T) {
	store := compiledStore()
	pub := &routing.FakePublisher{}

	rec := postIsochrone(store, pub, validIsochroneBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.ID == "" {
		t.Fatal("202 body carries no job id; the caller has nothing to poll")
	}
	if job.Status != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued", job.Status)
	}
	if job.CompileJobID != "compile-job-1" {
		t.Errorf("compile_job_id = %q, want the scenario's latest succeeded compile", job.CompileJobID)
	}
	// A job the handler answered with but never persisted would be unpollable.
	if job.CreatedAt != fixedNow {
		t.Error("the 202 body was not the row the store persisted")
	}
	if _, ok := store.only(); !ok {
		t.Errorf("want exactly one routing job recorded, got %d", store.count())
	}
}

// The public endpoint mints an ownerless job, because nobody authenticates to
// call it. RoutingJobStatus reads that nil as "readable by anyone holding the
// id", so getting it wrong here would silently lock a caller out of the job
// they just requested.
func TestIsochrone_202_jobHasNoOwner(t *testing.T) {
	store := compiledStore()

	rec := postIsochrone(store, &routing.FakePublisher{}, validIsochroneBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if job := decodeRoutingJob(t, rec); job.OwnerID != nil {
		t.Errorf("owner_id = %v, want nil for the unauthenticated public isochrone", *job.OwnerID)
	}
}

// The published message must name the same job the caller was handed, carry the
// compiled graph inline, and repeat the request's own parameters — otherwise
// the worker computes something other than what was asked for.
func TestIsochrone_202_publishesTheResolvedRequest(t *testing.T) {
	store := compiledStore()
	pub := &routing.FakePublisher{}

	rec := postIsochrone(store, pub, validIsochroneBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)

	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want exactly 1", len(msgs))
	}
	msg := msgs[0]

	if msg.SchemaVersion != routing.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", msg.SchemaVersion, routing.SchemaVersion)
	}
	if msg.RoutingJobID != job.ID {
		t.Errorf("message names job %q but the caller was handed %q", msg.RoutingJobID, job.ID)
	}
	if msg.CompileJobID != "compile-job-1" {
		t.Errorf("compile_job_id = %q, want compile-job-1", msg.CompileJobID)
	}
	if msg.Lat != 37.7 || msg.Lng != -122.4 || msg.BudgetMins != 30 || msg.Mode != transit.TravelModeWalk {
		t.Errorf("message parameters = %+v, want the request's own", msg)
	}
	// The graph travels inline: the worker has no database to look it up in.
	if msg.Graph == nil {
		t.Fatal("message carries no graph; the worker has no way to resolve one")
	}
	if len(msg.Graph.Nodes) != len(freshGraph().Nodes) {
		t.Errorf("graph has %d nodes, want the compiled graph's %d",
			len(msg.Graph.Nodes), len(freshGraph().Nodes))
	}
}

// A publish the broker never confirmed must not leave a row in `queued` for a
// client to poll forever. The job is failed on the spot and the caller is told.
func TestIsochrone_502_unconfirmedPublishFailsTheJob(t *testing.T) {
	store := compiledStore()
	pub := &routing.FakePublisher{Err: routing.ErrNotConfirmed}

	rec := postIsochrone(store, pub, validIsochroneBody)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d: %s", rec.Code, rec.Body.String())
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
		t.Errorf("routing job status = %q, want failed — a queued row here is one no worker will ever answer", job.Status)
	}
	if job.Error == "" {
		t.Error("the failed job records no reason")
	}
}

// The publish failing is still a 502 even if recording the failure also fails.
// The caller's information is the same either way; nothing is left pretending
// to have succeeded.
func TestIsochrone_502_whenTheFailureCannotBeRecordedEither(t *testing.T) {
	store := compiledStore()
	store.failErr = fmt.Errorf("database is on fire")
	pub := &routing.FakePublisher{Err: routing.ErrNotConfirmed}

	rec := postIsochrone(store, pub, validIsochroneBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Nothing is published until the row exists: a message naming a routing job the
// worker cannot find has nothing to transition.
func TestIsochrone_500_nothingIsPublishedIfTheJobCannotBeRecorded(t *testing.T) {
	store := compiledStore()
	store.createErr = fmt.Errorf("database is on fire")
	pub := &routing.FakePublisher{}

	rec := postIsochrone(store, pub, validIsochroneBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := len(pub.Messages()); n != 0 {
		t.Errorf("published %d messages for a job that was never recorded", n)
	}
}

func TestIsochrone_400_invalidMode(t *testing.T) {
	rec := postIsochrone(compiledStore(), &routing.FakePublisher{},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "invalid mode: must be walk, bike, or drive" {
		t.Errorf("error: got %q", got)
	}
}

func TestIsochrone_400_zeroBudget(t *testing.T) {
	rec := postIsochrone(compiledStore(), &routing.FakePublisher{},
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "budget_mins must be greater than 0" {
		t.Errorf("error: want 'budget_mins must be greater than 0', got %q", got)
	}
}

func TestIsochrone_400_malformedJSON(t *testing.T) {
	rec := postIsochrone(compiledStore(), &routing.FakePublisher{}, `{not valid json}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

// A bad body is a 400 even for a scenario that does not exist: the body is
// wrong regardless of what was asked for.
func TestIsochrone_400_beforeScenarioLookup(t *testing.T) {
	rec := postIsochrone(newFakeSeededGraphStore(), &routing.FakePublisher{},
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"nope"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

// Nothing is enqueued for a request that never resolves to a graph — every 4xx
// arm must leave the queue and the routing_jobs table untouched.
func TestIsochrone_rejectedRequestsEnqueueNothing(t *testing.T) {
	uncompiled := newFakeSeededGraphStore()
	uncompiled.scenarios["ca-hsr"] = transit.Scenario{ID: "sc-1", Slug: "ca-hsr"}

	cases := []struct {
		name  string
		store *fakeSeededGraphStore
		body  string
	}{
		{"invalid mode", compiledStore(), `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly","scenario_slug":"ca-hsr"}`},
		{"zero budget", compiledStore(), `{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`},
		{"malformed body", compiledStore(), `{not valid json}`},
		{"unknown scenario", compiledStore(), `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`},
		{"never compiled", uncompiled, validIsochroneBody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := &routing.FakePublisher{}
			rec := postIsochrone(tc.store, pub, tc.body)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status = %d, want a 4xx", rec.Code)
			}
			if n := len(pub.Messages()); n != 0 {
				t.Errorf("published %d messages for a rejected request", n)
			}
			if n := tc.store.count(); n != 0 {
				t.Errorf("recorded %d routing jobs for a rejected request", n)
			}
		})
	}
}

func TestIsochrone_404_scenarioNotFound(t *testing.T) {
	rec := postIsochrone(compiledStore(), &routing.FakePublisher{},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "scenario not found" {
		t.Errorf("error: want 'scenario not found', got %q", got)
	}
}

// A scenario that exists but has never compiled is a distinct 404: nothing is
// wrong with the request, the graph simply is not there yet.
func TestIsochrone_404_noCompiledGraph(t *testing.T) {
	store := newFakeSeededGraphStore()
	store.scenarios["ca-hsr"] = transit.Scenario{ID: "sc-1", Slug: "ca-hsr"}

	rec := postIsochrone(store, &routing.FakePublisher{}, validIsochroneBody)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "no compiled graph for this scenario yet" {
		t.Errorf("error: want 'no compiled graph for this scenario yet', got %q", got)
	}
}

// A succeeded job whose result never made it to the row is the same "not
// compiled yet" to a caller as no job at all.
func TestIsochrone_404_succeededJobWithNoResult(t *testing.T) {
	store := compiledStore()
	job := store.jobs["ca-hsr"]
	job.Result = nil
	store.jobs["ca-hsr"] = job

	rec := postIsochrone(store, &routing.FakePublisher{}, validIsochroneBody)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
}

func TestIsochrone_500_storeFailure(t *testing.T) {
	store := compiledStore()
	store.err = fmt.Errorf("database is on fire")

	rec := postIsochrone(store, &routing.FakePublisher{}, validIsochroneBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
}

func TestIsochrone_contentType(t *testing.T) {
	uncompiled := newFakeSeededGraphStore()
	uncompiled.scenarios["ca-hsr"] = transit.Scenario{ID: "sc-1", Slug: "ca-hsr"}

	cases := []struct {
		name  string
		body  string
		store handler.SeededGraphStore
		pub   routing.Publisher
	}{
		{"202", validIsochroneBody, compiledStore(), &routing.FakePublisher{}},
		{"400-budget", `{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`, compiledStore(), &routing.FakePublisher{}},
		{"404-scenario", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`, compiledStore(), &routing.FakePublisher{}},
		{"404-uncompiled", validIsochroneBody, uncompiled, &routing.FakePublisher{}},
		{"502-publish", validIsochroneBody, compiledStore(), &routing.FakePublisher{Err: routing.ErrNotConfirmed}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postIsochrone(tc.store, tc.pub, tc.body)
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type: want application/json, got %q", ct)
			}
		})
	}
}

// The acceptance criterion behind the golden fixture is that it is what *the
// API produces*, not merely what a struct literal in the routing package
// serialises to. So this drives a real isochrone request, with the fixture's own
// compile job id, graph, and request parameters behind it, and asserts the
// message that came out the other end is the fixture byte for byte.
//
// It reaches across into the routing package's testdata deliberately: there is
// one fixture, shared with the worker repository, and a copy here would be a
// second thing to keep in step with the first.
func TestIsochrone_publishesTheGoldenFixtureMessage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "routing", "testdata", "message.golden.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var want routing.Message
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	// Stand the store up as the fixture describes: the compile job it names,
	// carrying the graph it carries.
	store := newFakeSeededGraphStore()
	store.scenarios["ca-hsr"] = transit.Scenario{ID: "sc-1", Slug: "ca-hsr", Name: "CA HSR"}
	store.jobs["ca-hsr"] = transit.Job{
		ID: want.CompileJobID, Kind: transit.JobKindCompileScenario,
		Status: transit.JobStatusSucceeded, Result: want.Graph,
	}
	pub := &routing.FakePublisher{}

	body := fmt.Sprintf(`{"lat":%v,"lng":%v,"budget_mins":%d,"mode":%q,"scenario_slug":"ca-hsr"}`,
		want.Lat, want.Lng, want.BudgetMins, want.Mode)

	// The fixture's trace id stands in for whatever traceid.Middleware would
	// have attached to a real request; injected directly here since this test
	// calls the handler without going through that middleware.
	req := httptest.NewRequest(http.MethodPost, "/api/isochrone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(traceid.WithContext(req.Context(), want.TraceID))
	rec := httptest.NewRecorder()
	handler.Isochrone(store, pub, logger.Discard())(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}

	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	got := msgs[0]

	// The routing job id is minted per request, so it cannot equal the
	// fixture's. What must hold is that it is the id the caller was handed —
	// checked here, then normalised so the rest compares field for field.
	if got.RoutingJobID != decodeRoutingJob(t, rec).ID {
		t.Errorf("published message names job %q, but the caller was handed a different one", got.RoutingJobID)
	}
	got.RoutingJobID = want.RoutingJobID

	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("the message the API published is not the golden fixture.\n--- produced ---\n%s\n--- fixture ---\n%s",
			gotJSON, raw)
	}
}
