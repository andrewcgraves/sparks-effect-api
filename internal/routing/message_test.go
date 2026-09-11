package routing_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const goldenTraceID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

func goldenMessage() routing.Message {
	return routing.MessageFor(goldenJob(), goldenGraph(), goldenTraceID)
}

func goldenJob() transit.RoutingJob {
	return transit.RoutingJob{
		ID:           "6f9619ff-8b86-d011-b42d-00c04fc964ff",
		CompileJobID: "0f3b7c2a-4d1e-4a5b-9c8d-2e6f1a0b3c4d",
		Lat:          37.79,
		Lng:          -122.397,
		BudgetMins:   45,
		Mode:         transit.TravelModeWalk,
	}
}

func goldenGraph() *transit.TransitGraph {
	// Routing-anchor coordinates live in locals so taking their address is
	// stable. They are offset from `location` the way a real station is: the
	// pin stays on the alignment, Valhalla is aimed at something it can route
	// to (SPA-234).
	southRoutingLat, southRoutingLng := 34.0601, -118.238

	return &transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: "svc-express",
			Edges: []transit.Edge{
				// The edge names the corridor it runs over and where its two
				// stations sit along that corridor's alignment (SPA-264), which
				// is what lets the worker emit trip progress for a hop a rider's
				// budget does not finish. Descending chainage — the hop runs
				// against the direction the alignment was drawn in — because the
				// contract has to be pinned in the shape that is easiest to get
				// wrong, not the tidiest one.
				//
				// DwellS is the dwell *part* of Seconds, not an extra addend.
				// A non-zero value is required: omitempty dropped the field
				// entirely while it was zero, so SPA-223 never moved the fixture.
				{
					FromSlug: "north", ToSlug: "south", Seconds: 1800, DwellS: 45,
					RouteID: "rt-spine", FromChainageM: 27798.4, ToChainageM: 8123.9,
				},
			},
			WaitSecs:   300,
			WaitPolicy: string(transit.BoardingWaitFixed),
		}},
		// Both halves of the merge report, each with every nested tag set.
		// An empty report marshals as `{}` even with omitempty — a struct is
		// never "empty" to encoding/json — which is how a data-complete
		// fixture let the clusters / near_misses tags go unpinned.
		Merge: transit.MergeReport{
			Clusters: []transit.StopCluster{{
				Key:   "north",
				Names: []string{"North", "North Terminal"},
				Members: []transit.StopRef{
					{ServiceID: "svc-express", Slug: "north", Name: "North"},
					{ServiceID: "svc-local", Slug: "north-term", Name: "North Terminal"},
				},
			}},
			NearMisses: []transit.NearMiss{{
				A:         transit.StopRef{ServiceID: "svc-express", Slug: "south", Name: "South"},
				B:         transit.StopRef{ServiceID: "svc-local", Slug: "south-park", Name: "South Park"},
				DistanceM: 78.4,
			}},
		},
		Nodes: []transit.GraphNode{
			{Slug: "north", Lat: 37.7749, Lng: -122.4194, Names: []string{"North"}},
			{
				Slug: "south", Lat: 34.0522, Lng: -118.2437,
				RoutingLat: &southRoutingLat, RoutingLng: &southRoutingLng,
				Names: []string{"South"},
			},
		},
	}
}

func goldenPath() string { return filepath.Join("testdata", "message.golden.json") }

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
		"graph", "lat", "lng", "budget_mins", "mode", "trace_id",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("message is missing %q", key)
		}
	}
	if len(decoded) != 9 {
		t.Errorf("message has %d keys, want exactly the 9 the contract names: %v", len(decoded), decoded)
	}
}

func TestMessage_fixtureExercisesEveryJSONTag(t *testing.T) {
	raw, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	want := jsonTags(reflect.TypeOf(routing.Message{}))
	got := nonZeroJSONKeys(tree)

	for tag := range want {
		if !got[tag] {
			t.Errorf("JSON tag %q is reachable from routing.Message but does not appear with a non-zero value in %s",
				tag, goldenPath())
		}
	}
}

// jsonTags walks every exported json tag reachable from t, including through
// pointers and slices. An omitempty field that is left at its zero value
// never appears on the wire; collecting the tags from the type (rather than
// from one marshaled value) is what makes adding such a field fail this test
// until the fixture is enriched to match.
func jsonTags(t reflect.Type) map[string]bool {
	tags := map[string]bool{}
	walkJSONTags(t, map[reflect.Type]bool{}, tags)
	return tags
}

func walkJSONTags(t reflect.Type, seen map[reflect.Type]bool, tags map[string]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		walkJSONTags(t.Elem(), seen, tags)
		return
	case reflect.Map:
		walkJSONTags(t.Key(), seen, tags)
		walkJSONTags(t.Elem(), seen, tags)
		return
	case reflect.Struct:
	default:
		return
	}
	if seen[t] {
		return
	}
	seen[t] = true

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" && !f.Anonymous {
			name = f.Name
		}
		if name != "" && name != "-" {
			tags[name] = true
		}
		walkJSONTags(f.Type, seen, tags)
	}
}

func nonZeroJSONKeys(v any) map[string]bool {
	keys := map[string]bool{}
	collectNonZeroJSONKeys(v, keys)
	return keys
}

func collectNonZeroJSONKeys(v any, keys map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if jsonValueNonZero(val) {
				keys[k] = true
			}
			collectNonZeroJSONKeys(val, keys)
		}
	case []any:
		for _, val := range t {
			collectNonZeroJSONKeys(val, keys)
		}
	}
}

func jsonValueNonZero(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case json.Number:
		n, err := t.Float64()
		return err == nil && n != 0
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}
