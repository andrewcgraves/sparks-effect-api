package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func userIsochroneMux(store handler.UserIsochroneStore, sc stadia.Client) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/user-scenarios/{slug}/isochrone", handler.UserScenarioIsochrone(store, sc, logger.Discard()))
	return mux
}

func isoServeAs(t *testing.T, store handler.UserIsochroneStore, sc stadia.Client, user transit.User, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if user.ID != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	rec := httptest.NewRecorder()
	userIsochroneMux(store, sc).ServeHTTP(rec, r)
	return rec
}

const isoValidBody = `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk"}`

func freshGraph() *transit.TransitGraph {
	return &transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: "svc-1", WaitSecs: 60,
			Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 300}},
		}},
		Nodes: []transit.GraphNode{
			{Slug: "a", Lat: 37.7, Lng: -122.4, Names: []string{"A"}},
			{Slug: "b", Lat: 37.71, Lng: -122.41, Names: []string{"B"}},
		},
	}
}

func fakeStadia() *stadia.FakeClient {
	return &stadia.FakeClient{
		IsochroneResp: &stadia.IsochroneResponse{
			Type: "FeatureCollection",
			Features: []json.RawMessage{
				json.RawMessage(`{"type":"Feature","geometry":{"type":"Polygon","coordinates":[]},"properties":{}}`),
			},
		},
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{{{Time: 300, Distance: 1.0}}},
		},
	}
}

func TestUserScenarioIsochrone_401_unauthenticated(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, fakeStadia(), transit.User{}, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_unknownSlug(t *testing.T) {
	store := newFakeScenarioStore()

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/nope/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_nonOwner(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, fakeStadia(), scnStranger, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_noCompiledGraphYet(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_400_invalidMode(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/trip/isochrone",
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_409_deletedMember(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	// The job compiled svc-1 and svc-2; svc-2 has since been deleted, so the
	// scenario's current membership (svc-1 only) no longer matches — the exact
	// gap SPA-116 closes.
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1", "svc-2"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code: want %q, got %q", handler.StaleGraphErrorCode, body["code"])
	}
	// The stale response must never leak the outdated graph data.
	if _, ok := body["features"]; ok {
		t.Error("409 response leaks graph features")
	}
}

func TestUserScenarioIsochrone_409_editedMember(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(time.Minute)} // edited after compile
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_200_fresh(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, fakeStadia(), scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["type"] != "FeatureCollection" {
		t.Errorf("type: want FeatureCollection, got %v", resp["type"])
	}
}
