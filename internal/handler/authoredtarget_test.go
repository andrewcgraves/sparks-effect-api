package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The two authored targets — a service and a scenario — share one
// orchestration for compile, graph, and isochrone, so the only thing that may
// differ between their responses is which target they name. These tests pin
// that wording per adapter: a shared body that reached for the wrong noun, or
// a status policy that drifted on one target only, shows up here rather than
// in a frontend that branches on the message.

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return body
}

// A non-owner sees the same 404 as an unknown slug on both compile triggers,
// named after the target they addressed.
func TestCompileAuthoredTargetNonOwnerIsNotFound(t *testing.T) {
	tests := []struct {
		name    string
		handler func(handler.CompileStore) http.HandlerFunc
		path    string
		slug    string
		wantMsg string
	}{
		{"service", handler.CompileUserService, "/api/services/line-a/compile", "line-a", "service not found"},
		{"scenario", handler.CompileUserScenario, "/api/user-scenarios/trip/compile", "trip", "scenario not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeCompileStore()
			store.compilableUserFixture("owner")

			rec := postAs(t, tt.handler(store), tt.path, "slug", tt.slug, transit.User{ID: "someone-else"})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
			}
			if got := errorBody(t, rec)["error"]; got != tt.wantMsg {
				t.Errorf("error = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

// Both graph reads answer 404 before any compile, naming the target the caller
// asked about — the signal the frontend acts on by firing that target's
// compile, so it must not name the other one.
func TestAuthoredTargetGraphNotYetCompiledNamesItsTarget(t *testing.T) {
	tests := []struct {
		name    string
		handler func(handler.CompileStore) http.HandlerFunc
		path    string
		slug    string
		wantMsg string
	}{
		{"service", handler.UserServiceGraph, "/api/services/line-a/graph", "line-a", "no compiled graph for this service yet"},
		{"scenario", handler.UserScenarioGraph, "/api/user-scenarios/trip/graph", "trip", "no compiled graph for this scenario yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeCompileStore()
			store.compilableUserFixture("user-1")

			owner := transit.User{ID: "user-1"}
			rec := getWithPathValueAs(t, tt.handler(store), tt.path, "slug", tt.slug, &owner)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
			}
			if got := errorBody(t, rec)["error"]; got != tt.wantMsg {
				t.Errorf("error = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

// Both graph reads bundle the routes their services run on, under the same
// key, so one client-side graph-to-map path serves either response.
func TestAuthoredTargetGraphBundlesRoutesForBothTargets(t *testing.T) {
	tests := []struct {
		name    string
		handler func(handler.CompileStore) http.HandlerFunc
		seed    func(store *fakeCompileStore, svcID, scenarioID string)
		path    string
		slug    string
	}{
		{
			name:    "service",
			handler: handler.UserServiceGraph,
			seed: func(store *fakeCompileStore, svcID, _ string) {
				seedServiceCompileJob(store, svcID, &transit.TransitGraph{
					Services: []transit.ServiceGraph{{ServiceID: svcID}},
				})
			},
			path: "/api/services/line-a/graph", slug: "line-a",
		},
		{
			name:    "scenario",
			handler: handler.UserScenarioGraph,
			seed: func(store *fakeCompileStore, svcID, scenarioID string) {
				store.jobs["job-1"] = transit.Job{
					ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
					UserScenarioID: &scenarioID,
					Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: svcID}}},
				}
			},
			path: "/api/user-scenarios/trip/graph", slug: "trip",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeCompileStore()
			svcID, scenarioID := store.compilableUserFixture("user-1")
			tt.seed(store, svcID, scenarioID)

			owner := transit.User{ID: "user-1"}
			rec := getWithPathValueAs(t, tt.handler(store), tt.path, "slug", tt.slug, &owner)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}

			var resp struct {
				Services []transit.ServiceGraph `json:"services"`
				Routes   []transit.Route        `json:"routes"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Services) != 1 || resp.Services[0].ServiceID != svcID {
				t.Errorf("services = %+v, want the stored graph", resp.Services)
			}
			if len(resp.Routes) != 1 || resp.Routes[0].ID != "rt-user" {
				t.Fatalf("routes = %+v, want the service's route bundled", resp.Routes)
			}
		})
	}
}

// A stale graph is a 409 on both isochrones, carrying the same machine-readable
// code and a message telling the caller which target to recompile.
func TestAuthoredTargetIsochroneStaleNamesItsTarget(t *testing.T) {
	created := time.Now().Add(-time.Hour)

	t.Run("service", func(t *testing.T) {
		store := newFakeServiceStore()
		seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(time.Minute)) // edited after compile
		store.jobs["line-a"] = transit.Job{
			ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
			CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
		}

		rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
		assertStale(t, rec, "compiled graph is stale; recompile the service and retry")
	})

	t.Run("scenario", func(t *testing.T) {
		store := newFakeScenarioStore()
		seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
		store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(time.Minute)}
		store.jobs["trip"] = transit.Job{
			ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
			CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
		}

		rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
		assertStale(t, rec, "compiled graph is stale; recompile the scenario and retry")
	})
}

func assertStale(t *testing.T, rec *httptest.ResponseRecorder, wantMsg string) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	body := errorBody(t, rec)
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code = %q, want %q", body["code"], handler.StaleGraphErrorCode)
	}
	if body["error"] != wantMsg {
		t.Errorf("error = %q, want %q", body["error"], wantMsg)
	}
}

// Neither isochrone renders a graph that has never been compiled, and each
// says so about its own target.
func TestAuthoredTargetIsochroneNotYetCompiledNamesItsTarget(t *testing.T) {
	t.Run("service", func(t *testing.T) {
		store := newFakeServiceStore()
		seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, time.Now())

		rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
		}
		if got := errorBody(t, rec)["error"]; got != "no compiled graph for this service yet" {
			t.Errorf("error = %q, want the service wording", got)
		}
	})

	t.Run("scenario", func(t *testing.T) {
		store := newFakeScenarioStore()
		seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

		rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
		}
		if got := errorBody(t, rec)["error"]; got != "no compiled graph for this scenario yet" {
			t.Errorf("error = %q, want the scenario wording", got)
		}
	})
}
