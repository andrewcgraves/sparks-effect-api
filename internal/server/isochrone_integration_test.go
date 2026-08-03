package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const seededIsochroneBody = `{"lat":37.79,"lng":-122.397,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`

// The boot sequence, end to end against a real database: an empty database
// answers the public isochrone with a 404, seeding gives it the scenario, and
// compiling what was seeded is what makes the isochrone answerable — with no
// manual step and no admin credentials anywhere in it (SPA-181).
func TestIntegration_SeededIsochroneServedFromCompiledGraph(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()

	rec := request(t, h, http.MethodPost, "/api/isochrone", "", seededIsochroneBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("before seeding: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}

	// Seeded but not compiled: the scenario exists, its graph does not.
	rec = request(t, h, http.MethodPost, "/api/isochrone", "", seededIsochroneBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after seeding, before compiling: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	var errBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if errBody["error"] != "no compiled graph for this scenario yet" {
		t.Errorf("error = %q, want 'no compiled graph for this scenario yet'", errBody["error"])
	}

	compiled, err := transit.CompileSeededIfNeeded(ctx, repo)
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	if compiled == 0 {
		t.Fatal("CompileSeededIfNeeded compiled nothing after a fresh seed")
	}

	rec = request(t, h, http.MethodPost, "/api/isochrone", "", seededIsochroneBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("after compiling: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	// The 202 hands back a routing job, and the whole point of it having no
	// owner is that the anonymous caller who asked for it can poll it back.
	var job transit.RoutingJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode routing job: %v", err)
	}
	if job.OwnerID != nil {
		t.Errorf("owner_id = %v, want nil for the public isochrone", *job.OwnerID)
	}
	pollRec := request(t, h, http.MethodGet, "/api/routing-jobs/"+job.ID, "")
	if pollRec.Code != http.StatusOK {
		t.Fatalf("anonymous poll of an ownerless routing job: status %d, want 200; body %s",
			pollRec.Code, pollRec.Body.String())
	}
	var polled transit.RoutingJob
	if err := json.Unmarshal(pollRec.Body.Bytes(), &polled); err != nil {
		t.Fatalf("decode polled job: %v", err)
	}
	if polled.ID != job.ID || polled.Status != transit.JobStatusQueued {
		t.Errorf("polled %+v, want the queued job %s", polled, job.ID)
	}
	// It names the compile job whose graph it is plotted over — the identity the
	// worker keys its cache on.
	if polled.CompileJobID == "" {
		t.Error("routing job names no compile job; the worker has no graph identity to cache against")
	}

	// The same graph is public at the read endpoint, with no job id and no auth.
	graphRec := request(t, h, http.MethodGet, "/api/scenarios/ca-hsr/graph", "")
	if graphRec.Code != http.StatusOK {
		t.Fatalf("GET graph: status %d, want 200; body %s", graphRec.Code, graphRec.Body.String())
	}
	var graph transit.TransitGraph
	if err := json.NewDecoder(graphRec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(graph.Nodes) == 0 {
		t.Error("compiled graph carries no nodes; the isochrone has nothing to plot from")
	}
}

// Restarting against an already-compiled database must not recompile: the
// second boot finds the graph the first one wrote and leaves it alone.
func TestIntegration_BootDoesNotRecompileASeededDatabase(t *testing.T) {
	_, repo := integrationServer(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if _, err := transit.CompileSeededIfNeeded(ctx, repo); err != nil {
		t.Fatalf("CompileSeededIfNeeded: %v", err)
	}
	first, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("no compile job was recorded for the seeded data")
	}

	// A second boot.
	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		t.Fatalf("SeedIfEmpty (second): %v", err)
	}
	if seeded {
		t.Error("second SeedIfEmpty re-seeded a populated database")
	}
	compiled, err := transit.CompileSeededIfNeeded(ctx, repo)
	if err != nil {
		t.Fatalf("CompileSeededIfNeeded (second): %v", err)
	}
	if compiled != 0 {
		t.Errorf("second boot compiled %d scenario(s), want 0", compiled)
	}

	second, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs (second): %v", err)
	}
	if len(second) != len(first) {
		t.Errorf("jobs after second boot = %d, want %d (no new compile)", len(second), len(first))
	}
}
