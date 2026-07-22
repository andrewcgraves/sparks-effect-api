package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// A user compiles their own service: 202 with a queued job targeting the
// service by kind, and the worker runs it through to a stored graph whose
// compiled member ids are recorded.
func TestCompileUserServiceReturnsQueuedJobAndCompilesAsync(t *testing.T) {
	store := newFakeCompileStore()
	svcID, _ := store.compilableUserFixture("user-1")

	rec := postAs(t, handler.CompileUserService(store), "/api/services/line-a/compile", "slug", "line-a",
		transit.User{ID: "user-1"})
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

// A caller may not compile someone else's service: a non-owner sees the same
// 404 as an unknown slug, so ownership is not probeable.
func TestCompileUserServiceRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("owner")

	rec := postAs(t, handler.CompileUserService(store), "/api/services/line-a/compile", "slug", "line-a",
		transit.User{ID: "someone-else"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestCompileUserServiceUnknownSlugIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	rec := postAs(t, handler.CompileUserService(store), "/api/services/nope/compile", "slug", "nope",
		transit.User{ID: "user-1"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCompileUserServiceRequiresAuth(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("user-1")
	// No user in context — the method the request carries is irrelevant here, the
	// handler is invoked directly and rejects on the missing identity first.
	rec := getWithPathValueAs(t, handler.CompileUserService(store), "/api/services/line-a/compile", "slug", "line-a", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// A user compiles their own scenario's curated members into one graph.
func TestCompileUserScenarioReturnsQueuedJobAndCompilesAsync(t *testing.T) {
	store := newFakeCompileStore()
	svcID, scenarioID := store.compilableUserFixture("user-1")

	rec := postAs(t, handler.CompileUserScenario(store), "/api/user-scenarios/trip/compile", "slug", "trip",
		transit.User{ID: "user-1"})
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

	rec := postAs(t, handler.CompileUserScenario(store), "/api/user-scenarios/trip/compile", "slug", "trip",
		transit.User{ID: "someone-else"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

// The compiled graph is retrievable by the scenario's slug once a compile has
// succeeded — owner-scoped, unlike the public seeded graph.
func TestUserScenarioGraphReturnsCompiledResultForOwner(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("user-1")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	owner := transit.User{ID: "user-1"}
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

func TestUserScenarioGraphRejectsNonOwner(t *testing.T) {
	store := newFakeCompileStore()
	_, scenarioID := store.compilableUserFixture("owner")
	store.jobs["job-1"] = transit.Job{
		ID: "job-1", Kind: transit.JobKindCompileUserScenario, Status: transit.JobStatusSucceeded,
		UserScenarioID: &scenarioID,
		Result:         &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "usvc-1"}}},
	}

	stranger := transit.User{ID: "someone-else"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &stranger)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a non-owner", rec.Code)
	}
}

func TestUserScenarioGraphNotYetCompiledIsNotFound(t *testing.T) {
	store := newFakeCompileStore()
	store.compilableUserFixture("user-1")

	owner := transit.User{ID: "user-1"}
	rec := getWithPathValueAs(t, handler.UserScenarioGraph(store), "/api/user-scenarios/trip/graph", "slug", "trip", &owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 before any compile", rec.Code)
	}
}
