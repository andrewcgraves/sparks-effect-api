package routing_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// goldenMessage is the message the fixture describes. It is small and
// hand-written rather than a real compiled scenario: the fixture's job is to
// pin the *shape* of the contract, and a 3,000-byte CA HSR graph pasted into
// testdata would obscure that behind data no reader can check by eye.
func goldenMessage() routing.Message {
	return routing.Message{
		SchemaVersion: routing.SchemaVersion,
		RoutingJobID:  "6f9619ff-8b86-d011-b42d-00c04fc964ff",
		CompileJobID:  "0f3b7c2a-4d1e-4a5b-9c8d-2e6f1a0b3c4d",
		Graph: &transit.TransitGraph{
			Services: []transit.ServiceGraph{{
				ServiceID: "svc-express",
				Edges: []transit.Edge{
					{FromSlug: "north", ToSlug: "south", Seconds: 1800},
				},
				WaitSecs: 300,
			}},
			Nodes: []transit.GraphNode{
				{Slug: "north", Lat: 37.7749, Lng: -122.4194, Names: []string{"North"}},
				{Slug: "south", Lat: 34.0522, Lng: -118.2437, Names: []string{"South"}},
			},
		},
		Lat:        37.79,
		Lng:        -122.397,
		BudgetMins: 45,
		Mode:       transit.TravelModeWalk,
	}
}

func goldenPath() string { return filepath.Join("testdata", "message.golden.json") }

// The wire format is a contract with a repository this compiler cannot see. The
// fixture is the only thing holding the two ends together: this test asserts
// the API produces it byte for byte, and the worker repo asserts it consumes
// the same file. A change here that is not deliberate fails loudly on this side
// before it can silently break the other.
func TestMessage_matchesGoldenFixture(t *testing.T) {
	got, err := json.MarshalIndent(goldenMessage(), "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("message does not match %s.\n--- got ---\n%s\n--- want ---\n%s",
			goldenPath(), got, want)
	}
}

// Reading the fixture back must reproduce the message exactly. Marshalling
// alone would not catch a field the worker can serialize but not parse — an
// unexported field, say, or a type whose UnmarshalJSON disagrees with its
// Marshal.
func TestMessage_roundTripsThroughTheFixture(t *testing.T) {
	raw, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var got routing.Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	reencoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	original, err := json.Marshal(goldenMessage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(reencoded, original) {
		t.Errorf("round trip changed the message.\ngot  %s\nwant %s", reencoded, original)
	}
}

// The worker branches on schema_version before it reads anything else, so the
// field must be present and correct even if a caller builds a Message without
// setting it deliberately. This pins the constant itself: bumping it is a
// contract change that should require editing a test, not just a const.
func TestMessage_schemaVersionIsOne(t *testing.T) {
	if routing.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", routing.SchemaVersion)
	}

	var decoded map[string]any
	raw, err := json.Marshal(goldenMessage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Every key the contract names must be on the wire — including the ones
	// whose zero value would otherwise tempt an `omitempty`.
	for _, key := range []string{
		"schema_version", "routing_job_id", "compile_job_id",
		"graph", "lat", "lng", "budget_mins", "mode",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("message is missing %q", key)
		}
	}
	if len(decoded) != 8 {
		t.Errorf("message has %d keys, want exactly the 8 the contract names: %v", len(decoded), decoded)
	}
}
