package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestCompileUserServiceReturnsQueuedJobAndCompilesAsync(t *testing.T) {
	store := newFakeCompileStore()
	svcID, _ := store.compilableUserFixture("user-1")

	rec := postAs(t, handler.CompileUserService(store, transit.DefaultBoardingWaitPolicy()), "/api/services/line-a/compile", "slug", "line-a",
		account.User{ID: "user-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	var job transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.Kind != transit.JobKindCompileUserService {
		t.Errorf("kind = %q, want %q", job.Kind, transit.JobKindCompileUserService)
	}
	if job.UserServiceID == nil || *job.UserServiceID != svcID {
		t.Errorf("user_service_id = %v, want %s", job.UserServiceID, svcID)
	}
	if job.Status != transit.JobStatusQueued {
		t.Errorf("status = %q, want queued", job.Status)
	}

	completed := store.waitForCompletion(t)
	if completed.Status != transit.JobStatusSucceeded {
		t.Fatalf("completed status = %q, want succeeded (error: %s)", completed.Status, completed.Error)
	}
	if completed.Result == nil || len(completed.Result.Services) != 1 || completed.Result.Services[0].ServiceID != svcID {
		t.Errorf("result = %+v, want one compiled ServiceGraph for %s", completed.Result, svcID)
	}
	if len(completed.CompiledServiceIDs) != 1 || completed.CompiledServiceIDs[0] != svcID {
		t.Errorf("compiled_service_ids = %v, want [%s]", completed.CompiledServiceIDs, svcID)
	}
}

func TestCompileUserServiceRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("owner")

	rec := postAs(t, handler.CompileUserService(store, transit.DefaultBoardingWaitPolicy()), "/api/services/line-a/compile", "slug", "line-a",
		account.User{ID: "someone-else"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestCompileUserServiceUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	rec := postAs(t, handler.CompileUserService(store, transit.DefaultBoardingWaitPolicy()), "/api/services/nope/compile", "slug", "nope",
		account.User{ID: "user-1"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCompileUserServiceRequiresAuth(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("user-1")
	// No user in context — the method the request carries is irrelevant here, the
	// handler is invoked directly and rejects on the missing identity first.
	rec := getWithPathValueAs(t, handler.CompileUserService(store, transit.DefaultBoardingWaitPolicy()), "/api/services/line-a/compile", "slug", "line-a", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCompileUserScenarioReturnsQueuedJobAndCompilesAsync(t *testing.T) {
	store := newFakeCompileStore()
	svcID, scenarioID := store.compilableUserFixture("user-1")

	rec := postAs(t, handler.CompileUserScenario(store, transit.DefaultBoardingWaitPolicy()), "/api/user-scenarios/trip/compile", "slug", "trip",
		account.User{ID: "user-1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	var job transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.Kind != transit.JobKindCompileUserScenario {
		t.Errorf("kind = %q, want %q", job.Kind, transit.JobKindCompileUserScenario)
	}
	if job.UserScenarioID == nil || *job.UserScenarioID != scenarioID {
		t.Errorf("user_scenario_id = %v, want %s", job.UserScenarioID, scenarioID)
	}

	completed := store.waitForCompletion(t)
	if completed.Status != transit.JobStatusSucceeded {
		t.Fatalf("completed status = %q, want succeeded (error: %s)", completed.Status, completed.Error)
	}
	if len(completed.CompiledServiceIDs) != 1 || completed.CompiledServiceIDs[0] != svcID {
		t.Errorf("compiled_service_ids = %v, want [%s]", completed.CompiledServiceIDs, svcID)
	}
}

func TestCompileUserScenarioRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("owner")

	rec := postAs(t, handler.CompileUserScenario(store, transit.DefaultBoardingWaitPolicy()), "/api/user-scenarios/trip/compile", "slug", "trip",
		account.User{ID: "someone-else"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestUserScenarioGraphReturnsCompiledResultForOwner(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("user-1")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var graph transit.TransitGraph
	if err := json.NewDecoder(rec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(graph.Services) != 1 || graph.Services[0].ServiceID != "usvc-1" {
		t.Errorf("graph = %+v, want the stored graph", graph)
	}
}

func TestUserScenarioGraphBundlesMemberRoutes(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("user-1")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &owner)
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
	// The graph fields stay inlined alongside the new routes array.
	if len(resp.Services) != 1 || resp.Services[0].ServiceID != "usvc-1" {
		t.Errorf("services = %+v, want the stored graph", resp.Services)
	}
	if len(resp.Routes) != 1 || resp.Routes[0].ID != "rt-user" {
		t.Fatalf("routes = %+v, want the member service's route", resp.Routes)
	}
	if len(resp.Routes[0].Geometry.Coordinates) == 0 {
		t.Errorf("route geometry was not bundled: %+v", resp.Routes[0])
	}
}

func TestUserScenarioGraphRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("owner")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	stranger := account.User{ID: "someone-else"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &stranger)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestUserScenarioGraphNotYetCompiledIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("user-1")

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 before any compile", rec.Code)
	}
}

// --- single-service graph read (SPA-140) ---

func seedServiceCompileJob(store *fakeCompileStore, svcID string, result *transit.TransitGraph) {
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserService, Status: transit.JobStatusSucceeded,
		UserServiceID: &svcID,
		Result:        result,
	}
}

func TestUserServiceGraphReturnsCompiledGraphAndRouteForOwner(t *testing.T) {
	store := newFakeCompileStore()
	svcID, _ := store.compilableUserFixture("user-1")
	seedServiceCompileJob(store, svcID, &transit.TransitGraph{
		Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}},
	})

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/line-a/graph", "slug", "line-a", &owner)
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
	// The shape matches the scenario graph read exactly, so the frontend's
	// graph-to-map helpers work against either without a service-specific path.
	if len(resp.Services) != 1 || resp.Services[0].ServiceID != "usvc-1" {
		t.Errorf("services = %+v, want the stored graph", resp.Services)
	}
	if len(resp.Routes) != 1 || resp.Routes[0].ID != "rt-user" {
		t.Fatalf("routes = %+v, want the service's own route", resp.Routes)
	}
	if len(resp.Routes[0].Geometry.Coordinates) == 0 {
		t.Errorf("route geometry was not bundled: %+v", resp.Routes[0])
	}
}

func TestUserServiceGraphRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	svcID, _ := store.compilableUserFixture("owner")
	seedServiceCompileJob(store, svcID, &transit.TransitGraph{
		Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}},
	})

	stranger := account.User{ID: "someone-else"}
	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/line-a/graph", "slug", "line-a", &stranger)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestUserServiceGraphUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeCompileStore()

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/nope/graph", "slug", "nope", &owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown slug", rec.Code)
	}
}

func TestUserServiceGraphNotYetCompiledIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("user-1")

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/line-a/graph", "slug", "line-a", &owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 before any compile", rec.Code)
	}
}

func TestUserServiceGraphIgnoresScenarioCompileJobs(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("user-1")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	owner := account.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/line-a/graph", "slug", "line-a", &owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when only a scenario compile exists", rec.Code)
	}
}

func TestUserServiceGraphRejectsUnauthenticated(t *testing.T) {
	store := newFakeCompileStore()
	svcID, _ := store.compilableUserFixture("user-1")
	seedServiceCompileJob(store, svcID, &transit.TransitGraph{
		Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}},
	})

	rec := getWithPathValueAs(t, handler.UserServiceGraph(store), "/api/services/line-a/graph", "slug", "line-a", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a session", rec.Code)
	}
}
