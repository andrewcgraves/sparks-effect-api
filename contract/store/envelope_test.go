package store_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-contract/store"
)

type workerStoreEnvelope struct {
	CacheLookupRequest  store.CacheLookupRequest  `json:"cache_lookup_request"`
	CacheLookupResponse store.CacheLookupResponse `json:"cache_lookup_response"`
	CachePutRequest     store.CachePutRequest     `json:"cache_put_request"`
	JobSucceeded        store.JobSucceededBody    `json:"job_succeeded"`
	JobFailed           store.JobFailedBody       `json:"job_failed"`
}

func goldenIsochroneKey() store.IsochroneKey {
	return store.IsochroneKey{
		CompileJobID: "0f3b7c2a-4d1e-4a5b-9c8d-2e6f1a0b3c4d",
		StationSlug:  "north",
		Mode:         "transit",
		ContourMins:  30,
		// departs_on is omitempty. A zero here would drop the SPA-269 field
		// from the fixture and leave the exact drift that motivated the key
		// change unguarded.
		DepartsOn: "2026-09-02",
	}
}

func goldenGeometry() json.RawMessage {
	return json.RawMessage(`{"type":"Polygon","coordinates":[[[-122.4,37.8],[-122.3,37.8],[-122.3,37.7],[-122.4,37.8]]]}`)
}

func goldenTilesetAt() time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
}

func goldenEnvelope() workerStoreEnvelope {
	key := goldenIsochroneKey()
	geom := goldenGeometry()
	return workerStoreEnvelope{
		CacheLookupRequest: store.CacheLookupRequest{Keys: []store.IsochroneKey{key}},
		CacheLookupResponse: store.CacheLookupResponse{
			Entries: []store.CacheLookupEntry{{Key: key, Geometry: geom}},
		},
		CachePutRequest: store.CachePutRequest{
			Entries: []store.CachedIsochrone{{
				Key:       key,
				Geometry:  geom,
				TilesetAt: goldenTilesetAt(),
			}},
		},
		JobSucceeded: store.JobSucceededBody{Result: geom},
		JobFailed:    store.JobFailedBody{Error: "valhalla unreachable"},
	}
}

func goldenPath() string { return filepath.Join("testdata", "worker-store.golden.json") }

func TestWorkerStoreEnvelope_matchesGoldenFixture(t *testing.T) {
	got, err := json.MarshalIndent(goldenEnvelope(), "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("worker-store envelope does not match %s.\n--- got ---\n%s\n--- want ---\n%s",
			goldenPath(), got, want)
	}
}

func TestWorkerStoreEnvelope_roundTripsThroughTheFixture(t *testing.T) {
	raw, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var got workerStoreEnvelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	reencoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	original, err := json.Marshal(goldenEnvelope())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(reencoded, original) {
		t.Errorf("round trip changed the envelope.\ngot  %s\nwant %s", reencoded, original)
	}
}

func TestWorkerStoreEnvelope_fixtureExercisesEveryJSONTag(t *testing.T) {
	raw, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	want := jsonTags(reflect.TypeOf(workerStoreEnvelope{}))
	got := nonZeroJSONKeys(tree)

	for tag := range want {
		if !got[tag] {
			t.Errorf("JSON tag %q is reachable from the worker-store envelope but does not appear with a non-zero value in %s",
				tag, goldenPath())
		}
	}

	if !got["departs_on"] {
		t.Error("departs_on is missing or zero in the fixture")
	}
}

func TestIsochroneKey_isComparable(t *testing.T) {
	k := goldenIsochroneKey()
	m := map[store.IsochroneKey]int{k: 1}
	if m[k] != 1 {
		t.Fatal("IsochroneKey must remain a usable map key")
	}
}

func jsonTags(t reflect.Type) map[string]bool {
	tags := map[string]bool{}
	walkJSONTags(t, map[reflect.Type]bool{}, tags)
	return tags
}

func walkJSONTags(t reflect.Type, seen map[reflect.Type]bool, tags map[string]bool) {
	if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf(json.RawMessage(nil)) {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t == reflect.TypeOf(json.RawMessage(nil)) {
			return
		}
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
