package routing_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestMessageFor_copiesJobFields(t *testing.T) {
	graph := &transit.TransitGraph{Services: []transit.ServiceGraph{{ServiceID: "svc-1"}}}
	job := transit.RoutingJob{
		ID:           "job-1",
		CompileJobID: "compile-1",
		Lat:          37.79,
		Lng:          -122.397,
		BudgetMins:   45,
		Mode:         transit.TravelModeWalk,
	}

	got := routing.MessageFor(job, graph, "trace-1")
	if got.SchemaVersion != routing.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, routing.SchemaVersion)
	}
	if got.RoutingJobID != job.ID || got.CompileJobID != job.CompileJobID {
		t.Errorf("ids = %s/%s, want %s/%s", got.RoutingJobID, got.CompileJobID, job.ID, job.CompileJobID)
	}
	if got.Graph != graph {
		t.Error("Graph pointer was not the one passed in")
	}
	if got.Lat != job.Lat || got.Lng != job.Lng || got.BudgetMins != job.BudgetMins || got.Mode != job.Mode {
		t.Errorf("origin fields = %+v", got)
	}
	if got.TraceID != "trace-1" {
		t.Errorf("TraceID = %q, want trace-1", got.TraceID)
	}
}

func TestMessageGolden_matchesContractModuleCopy(t *testing.T) {
	local, err := os.ReadFile("testdata/message.golden.json")
	if err != nil {
		t.Fatalf("read internal copy: %v", err)
	}
	canonical, err := os.ReadFile("../../contract/routing/testdata/message.golden.json")
	if err != nil {
		t.Fatalf("read contract copy: %v", err)
	}
	if !bytes.Equal(local, canonical) {
		t.Error("internal/routing/testdata/message.golden.json drifted from contract/routing/testdata/message.golden.json — the worker's check-contract curls the internal/ path")
	}
}
