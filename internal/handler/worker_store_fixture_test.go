package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-contract/store"
)

// workerStoreEnvelope is the HTTP contract SPA-273 left without a fixture:
// the cache lookup/put wrappers and the job-transition bodies. The JSON
// tags live on the contract store types; a renamed field decodes as its
// zero value and every cache lookup silently misses.
type workerStoreEnvelope struct {
	CacheLookupRequest  store.CacheLookupRequest  `json:"cache_lookup_request"`
	CacheLookupResponse store.CacheLookupResponse `json:"cache_lookup_response"`
	CachePutRequest     store.CachePutRequest     `json:"cache_put_request"`
	JobSucceeded        store.JobSucceededBody    `json:"job_succeeded"`
	JobFailed           store.JobFailedBody       `json:"job_failed"`
}

func goldenIsochroneKey() IsochroneKey {
	return IsochroneKey{
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
		CacheLookupRequest: cacheLookupRequest{Keys: []IsochroneKey{key}},
		CacheLookupResponse: cacheLookupResponse{
			Entries: []cacheLookupEntry{{Key: key, Geometry: geom}},
		},
		CachePutRequest: cachePutRequest{
			Entries: []CachedIsochrone{{
				Key:       key,
				Geometry:  geom,
				TilesetAt: goldenTilesetAt(),
			}},
		},
		JobSucceeded: jobSucceededBody{Result: geom},
		JobFailed:    jobFailedBody{Error: "valhalla unreachable"},
	}
}

func workerStoreGoldenPath() string {
	return filepath.Join("testdata", "worker-store.golden.json")
}

func TestWorkerStoreEnvelope_matchesGoldenFixture(t *testing.T) {
	got, err := json.MarshalIndent(goldenEnvelope(), "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(workerStoreGoldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("worker-store envelope does not match %s.\n--- got ---\n%s\n--- want ---\n%s",
			workerStoreGoldenPath(), got, want)
	}
}

func TestWorkerStoreEnvelope_roundTripsThroughTheFixture(t *testing.T) {
	raw, err := os.ReadFile(workerStoreGoldenPath())
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var got workerStoreEnvelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	want := goldenEnvelope()
	if got.CacheLookupRequest.Keys[0] != want.CacheLookupRequest.Keys[0] {
		t.Errorf("lookup key = %+v, want %+v", got.CacheLookupRequest.Keys[0], want.CacheLookupRequest.Keys[0])
	}
	if got.CacheLookupResponse.Entries[0].Key != want.CacheLookupResponse.Entries[0].Key {
		t.Errorf("lookup entry key = %+v, want %+v", got.CacheLookupResponse.Entries[0].Key, want.CacheLookupResponse.Entries[0].Key)
	}
	if got.CachePutRequest.Entries[0].Key != want.CachePutRequest.Entries[0].Key {
		t.Errorf("put key = %+v, want %+v", got.CachePutRequest.Entries[0].Key, want.CachePutRequest.Entries[0].Key)
	}
	if !got.CachePutRequest.Entries[0].TilesetAt.Equal(want.CachePutRequest.Entries[0].TilesetAt) {
		t.Errorf("tileset_at = %v, want %v", got.CachePutRequest.Entries[0].TilesetAt, want.CachePutRequest.Entries[0].TilesetAt)
	}
	if got.JobFailed.Error != want.JobFailed.Error {
		t.Errorf("error = %q, want %q", got.JobFailed.Error, want.JobFailed.Error)
	}

	assertCompactJSONEqual(t, "lookup geometry", got.CacheLookupResponse.Entries[0].Geometry, want.CacheLookupResponse.Entries[0].Geometry)
	assertCompactJSONEqual(t, "put geometry", got.CachePutRequest.Entries[0].Geometry, want.CachePutRequest.Entries[0].Geometry)
	assertCompactJSONEqual(t, "job result", got.JobSucceeded.Result, want.JobSucceeded.Result)
}

func TestWorkerStoreEnvelope_isShapeComplete(t *testing.T) {
	raw, err := json.Marshal(goldenEnvelope())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := jsonTagsOf(reflect.TypeOf(workerStoreEnvelope{}))
	got := jsonKeysOf(decoded)
	for tag := range want {
		if _, ok := got[tag]; !ok {
			t.Errorf("fixture is missing %q — an omitempty field with a zero value would slip through identically", tag)
		}
	}

	assertNonEmptyJSON(t, "$", decoded)

	// Named because a missing departs_on is the SPA-269 failure mode: the
	// fixture still matches, every cache lookup misses, and there is no
	// error anywhere in the system to find it by.
	if _, ok := got["departs_on"]; !ok {
		t.Error("departs_on is missing from the fixture")
	}
	if _, ok := got["tileset_at"]; !ok {
		t.Error("tileset_at is missing from the fixture")
	}
}

func TestWorkerCacheLookup_servesTheGoldenEnvelope(t *testing.T) {
	env := goldenEnvelope()
	key := env.CacheLookupRequest.Keys[0]
	store := &goldenWorkerStore{cache: map[IsochroneKey]json.RawMessage{
		key: env.CacheLookupResponse.Entries[0].Geometry,
	}}

	body, err := json.Marshal(env.CacheLookupRequest)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache/lookup", bytes.NewReader(body))
	WorkerCacheLookup(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	want, err := json.Marshal(env.CacheLookupResponse)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	got := bytes.TrimSpace(rec.Body.Bytes())
	if !bytes.Equal(got, want) {
		t.Errorf("lookup response is not the golden envelope.\ngot  %s\nwant %s", got, want)
	}
}

func TestWorkerCachePut_acceptsTheGoldenEnvelope(t *testing.T) {
	env := goldenEnvelope()
	store := &goldenWorkerStore{}

	body, err := json.Marshal(env.CachePutRequest)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache", bytes.NewReader(body))
	WorkerCachePut(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if len(store.putEntries) != 1 {
		t.Fatalf("put %d entries, want 1", len(store.putEntries))
	}
	got := store.putEntries[0]
	if got.Key != env.CachePutRequest.Entries[0].Key {
		t.Errorf("key = %+v, want %+v", got.Key, env.CachePutRequest.Entries[0].Key)
	}
	if !got.TilesetAt.Equal(goldenTilesetAt()) {
		t.Errorf("tileset_at = %v, want %v", got.TilesetAt, goldenTilesetAt())
	}
}

func TestWorkerJobTransitions_acceptTheGoldenEnvelope(t *testing.T) {
	env := goldenEnvelope()
	store := &goldenWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/succeeded", WorkerMarkSucceeded(store))
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/failed", WorkerMarkFailed(store))

	succeeded, err := json.Marshal(env.JobSucceeded)
	if err != nil {
		t.Fatalf("marshal succeeded: %v", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/succeeded", bytes.NewReader(succeeded)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("succeeded status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(store.result, env.JobSucceeded.Result) {
		t.Errorf("result = %s, want %s", store.result, env.JobSucceeded.Result)
	}

	failed, err := json.Marshal(env.JobFailed)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/failed", bytes.NewReader(failed)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("failed status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if store.errMsg != env.JobFailed.Error {
		t.Errorf("error = %q, want %q", store.errMsg, env.JobFailed.Error)
	}
}

type goldenWorkerStore struct {
	cache      map[IsochroneKey]json.RawMessage
	putEntries []CachedIsochrone
	result     json.RawMessage
	errMsg     string
}

func (g *goldenWorkerStore) MarkRoutingJobRunning(context.Context, string) error { return nil }

func (g *goldenWorkerStore) SucceedRoutingJob(_ context.Context, _ string, result json.RawMessage) error {
	g.result = result
	return nil
}

func (g *goldenWorkerStore) FailRoutingJob(_ context.Context, _, errMsg string) error {
	g.errMsg = errMsg
	return nil
}

func (g *goldenWorkerStore) GetIsochroneCache(_ context.Context, keys []IsochroneKey) (map[IsochroneKey]json.RawMessage, error) {
	out := map[IsochroneKey]json.RawMessage{}
	for _, k := range keys {
		if v, ok := g.cache[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (g *goldenWorkerStore) PutIsochroneCache(_ context.Context, entries []CachedIsochrone) error {
	g.putEntries = entries
	return nil
}

func jsonTagsOf(t reflect.Type) map[string]struct{} {
	seen := map[string]struct{}{}
	collectJSONTags(t, seen)
	return seen
}

func collectJSONTags(t reflect.Type, seen map[string]struct{}) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return
		}
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
			if name == "" {
				name = f.Name
			}
			seen[name] = struct{}{}
			collectJSONTags(f.Type, seen)
		}
	case reflect.Slice, reflect.Array:
		if t == reflect.TypeOf(json.RawMessage(nil)) {
			return
		}
		collectJSONTags(t.Elem(), seen)
	}
}

func jsonKeysOf(v any) map[string]struct{} {
	seen := map[string]struct{}{}
	collectJSONKeys(v, seen)
	return seen
}

func collectJSONKeys(v any, seen map[string]struct{}) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			seen[k] = struct{}{}
			collectJSONKeys(child, seen)
		}
	case []any:
		for _, child := range x {
			collectJSONKeys(child, seen)
		}
	}
}

func assertCompactJSONEqual(t *testing.T, what string, got, want json.RawMessage) {
	t.Helper()
	gotCompact := compactJSON(t, got)
	wantCompact := compactJSON(t, want)
	if !bytes.Equal(gotCompact, wantCompact) {
		t.Errorf("%s: got %s, want %s", what, gotCompact, wantCompact)
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return buf.Bytes()
}

func assertNonEmptyJSON(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			t.Errorf("%s: empty object", path)
			return
		}
		for k, child := range x {
			assertNonEmptyJSON(t, path+"."+k, child)
		}
	case []any:
		if len(x) == 0 {
			t.Errorf("%s: empty array", path)
			return
		}
		for i, child := range x {
			assertNonEmptyJSON(t, fmt.Sprintf("%s[%d]", path, i), child)
		}
	case string:
		if x == "" {
			t.Errorf("%s: empty string", path)
		}
	case float64:
		if x == 0 {
			t.Errorf("%s: zero", path)
		}
	case nil:
		t.Errorf("%s: null", path)
	}
}
