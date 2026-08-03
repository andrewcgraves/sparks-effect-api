package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeSeededGraphStore stands in for the repository behind the public
// isochrone: seeded scenarios by slug, and the compile job that carries each
// one's graph.
type fakeSeededGraphStore struct {
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
	graph := freshGraph()
	f.jobs["ca-hsr"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileScenario, Status: transit.JobStatusSucceeded,
		Result: graph,
	}
	return f
}

func postIsochrone(store handler.SeededGraphStore, sc stadia.Client, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/isochrone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Isochrone(store, sc, logger.Discard())(rec, req)
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

func TestIsochrone_200_validRequest(t *testing.T) {
	rec := postIsochrone(compiledStore(), fakeStadia(), validIsochroneBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["type"] != "FeatureCollection" {
		t.Errorf("type: want FeatureCollection, got %v", body["type"])
	}
	if _, ok := body["metadata"]; !ok {
		t.Error("response missing metadata")
	}
}

// The graph the isochrone plots from is the compile job's result, not the
// embedded store's — the whole point of SPA-181. The fixture graph's nodes are
// the only places an egress polygon can be requested for, so seeing exactly
// those origins proves where the data came from.
func TestIsochrone_plotsFromTheCompiledGraph(t *testing.T) {
	sc := fakeStadia()
	rec := postIsochrone(compiledStore(), sc, validIsochroneBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	graphNodes := make(map[stadia.LatLng]bool)
	for _, n := range freshGraph().Nodes {
		graphNodes[stadia.LatLng{Lat: n.Lat, Lng: n.Lng}] = true
	}
	origin := stadia.LatLng{Lat: 37.7, Lng: -122.4}
	egress := 0
	for _, call := range sc.IsochoneCalls {
		if call.Origin == origin {
			continue
		}
		if !graphNodes[call.Origin] {
			t.Errorf("isochrone requested at %+v, which is not a node of the compiled graph", call.Origin)
		}
		egress++
	}
	if egress == 0 {
		t.Error("no egress isochrone was requested; the graph's nodes were never reached")
	}
}

func TestIsochrone_400_invalidMode(t *testing.T) {
	rec := postIsochrone(compiledStore(), fakeStadia(),
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if errorField(t, rec) == "" {
		t.Error("expected non-empty error field")
	}
}

func TestIsochrone_400_zeroBudget(t *testing.T) {
	rec := postIsochrone(compiledStore(), fakeStadia(),
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "budget_mins must be greater than 0" {
		t.Errorf("error: want 'budget_mins must be greater than 0', got %q", got)
	}
}

func TestIsochrone_400_malformedJSON(t *testing.T) {
	rec := postIsochrone(compiledStore(), fakeStadia(), `{not valid json}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

// A bad body is a 400 even for a scenario that does not exist: the body is
// wrong regardless of what was asked for.
func TestIsochrone_400_beforeScenarioLookup(t *testing.T) {
	rec := postIsochrone(newFakeSeededGraphStore(), fakeStadia(),
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"nope"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestIsochrone_404_scenarioNotFound(t *testing.T) {
	rec := postIsochrone(compiledStore(), fakeStadia(),
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

	rec := postIsochrone(store, fakeStadia(), validIsochroneBody)

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

	rec := postIsochrone(store, fakeStadia(), validIsochroneBody)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
}

func TestIsochrone_500_storeFailure(t *testing.T) {
	store := compiledStore()
	store.err = fmt.Errorf("database is on fire")

	rec := postIsochrone(store, fakeStadia(), validIsochroneBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
}

func TestIsochrone_502_stadiaError(t *testing.T) {
	sc := fakeStadia()
	sc.IsochroneErr = fmt.Errorf("%w: connection refused", stadia.ErrStadiaUpstream)

	rec := postIsochrone(compiledStore(), sc, validIsochroneBody)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "routing service unavailable" {
		t.Errorf("error: want 'routing service unavailable', got %q", got)
	}
}

func TestIsochrone_400_stadiaClientError(t *testing.T) {
	sc := fakeStadia()
	sc.IsochroneErr = fmt.Errorf("%w: path distance exceeds limit", stadia.ErrStadiaBadRequest)

	rec := postIsochrone(compiledStore(), sc, validIsochroneBody)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "routing request exceeded service limits" {
		t.Errorf("error: want 'routing request exceeded service limits', got %q", got)
	}
}

func TestIsochrone_429_stadiaRateLimit(t *testing.T) {
	sc := fakeStadia()
	sc.IsochroneErr = fmt.Errorf("%w: credit exhausted", stadia.ErrStadiaRateLimit)

	rec := postIsochrone(compiledStore(), sc, validIsochroneBody)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429, got %d", rec.Code)
	}
	if got := errorField(t, rec); got != "routing service rate limit exceeded" {
		t.Errorf("error: want 'routing service rate limit exceeded', got %q", got)
	}
}

func TestIsochrone_contentType(t *testing.T) {
	rateLimited := fakeStadia()
	rateLimited.IsochroneErr = stadia.ErrStadiaRateLimit
	unavailable := fakeStadia()
	unavailable.IsochroneErr = stadia.ErrStadiaUpstream

	cases := []struct {
		name   string
		body   string
		store  handler.SeededGraphStore
		stadia stadia.Client
	}{
		{"200", validIsochroneBody, compiledStore(), fakeStadia()},
		{"400-budget", `{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`, compiledStore(), fakeStadia()},
		{"404", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`, compiledStore(), fakeStadia()},
		{"429", validIsochroneBody, compiledStore(), rateLimited},
		{"502", validIsochroneBody, compiledStore(), unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postIsochrone(tc.store, tc.stadia, tc.body)
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type: want application/json, got %q", ct)
			}
		})
	}
}
