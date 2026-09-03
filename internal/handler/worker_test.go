package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

type fakeWorkerStore struct {
	runningErr   error
	succeededErr error
	failedErr    error
	getErr       error
	putErr       error

	runningID   string
	succeededID string
	result      json.RawMessage
	failedID    string
	errMsg      string
	gotKeys     []handler.IsochroneKey
	putEntries  []handler.CachedIsochrone
	cache       map[handler.IsochroneKey]json.RawMessage
}

func (f *fakeWorkerStore) MarkRoutingJobRunning(_ context.Context, id string) error {
	f.runningID = id
	return f.runningErr
}

func (f *fakeWorkerStore) SucceedRoutingJob(_ context.Context, id string, result json.RawMessage) error {
	f.succeededID = id
	f.result = result
	return f.succeededErr
}

func (f *fakeWorkerStore) FailRoutingJob(_ context.Context, id, errMsg string) error {
	f.failedID = id
	f.errMsg = errMsg
	return f.failedErr
}

func (f *fakeWorkerStore) GetIsochroneCache(_ context.Context, keys []handler.IsochroneKey) (map[handler.IsochroneKey]json.RawMessage, error) {
	f.gotKeys = keys
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := map[handler.IsochroneKey]json.RawMessage{}
	for _, k := range keys {
		if v, ok := f.cache[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakeWorkerStore) PutIsochroneCache(_ context.Context, entries []handler.CachedIsochrone) error {
	f.putEntries = entries
	return f.putErr
}

func TestWorkerReady(t *testing.T) {
	rec := httptest.NewRecorder()
	handler.WorkerReady().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/internal/worker", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestWorkerMarkRunning(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/running", handler.WorkerMarkRunning(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/running", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if store.runningID != "job-1" {
		t.Errorf("id = %q, want job-1", store.runningID)
	}
}

func TestWorkerMarkRunning_notFound(t *testing.T) {
	store := &fakeWorkerStore{runningErr: handler.ErrJobNotFound}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/running", handler.WorkerMarkRunning(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/running", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWorkerMarkSucceeded(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/succeeded", handler.WorkerMarkSucceeded(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/succeeded",
		bytes.NewReader([]byte(`{"result":{"type":"FeatureCollection"}}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if store.succeededID != "job-1" {
		t.Errorf("id = %q, want job-1", store.succeededID)
	}
	var got map[string]any
	if err := json.Unmarshal(store.result, &got); err != nil {
		t.Fatalf("result: %v", err)
	}
	if got["type"] != "FeatureCollection" {
		t.Errorf("result = %v", got)
	}
}

func TestWorkerMarkSucceeded_malformedBody(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/succeeded", handler.WorkerMarkSucceeded(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/succeeded",
		bytes.NewReader([]byte(`not json`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWorkerMarkFailed(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/failed", handler.WorkerMarkFailed(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/failed",
		bytes.NewReader([]byte(`{"error":"valhalla unreachable"}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if store.failedID != "job-1" || store.errMsg != "valhalla unreachable" {
		t.Errorf("failed %s/%q", store.failedID, store.errMsg)
	}
}

func TestWorkerCacheLookupEchoesCallerKeys(t *testing.T) {
	k := handler.IsochroneKey{CompileJobID: "c1", StationSlug: "north", Mode: "walk", ContourMins: 30}
	store := &fakeWorkerStore{cache: map[handler.IsochroneKey]json.RawMessage{
		k: json.RawMessage(`{"type":"Polygon"}`),
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/isochrone-cache/lookup", handler.WorkerCacheLookup(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache/lookup",
		bytes.NewReader([]byte(`{"keys":[{"compile_job_id":"c1","station_slug":"north","mode":"walk","contour_mins":30},{"compile_job_id":"c1","station_slug":"south","mode":"walk","contour_mins":30}]}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Entries []struct {
			Key      handler.IsochroneKey `json:"key"`
			Geometry json.RawMessage      `json:"geometry"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(out.Entries))
	}
	if out.Entries[0].Key != k {
		t.Errorf("key = %+v, want %+v", out.Entries[0].Key, k)
	}
}

func TestWorkerCacheLookupKeepsTransitDatesApart(t *testing.T) {
	sep := handler.IsochroneKey{
		CompileJobID: "c1", StationSlug: "north", Mode: "transit", ContourMins: 30, DepartsOn: "2026-09-01",
	}
	oct := sep
	oct.DepartsOn = "2026-09-02"
	store := &fakeWorkerStore{cache: map[handler.IsochroneKey]json.RawMessage{
		sep: json.RawMessage(`{"day":"sep"}`),
		oct: json.RawMessage(`{"day":"oct"}`),
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/isochrone-cache/lookup", handler.WorkerCacheLookup(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache/lookup",
		bytes.NewReader([]byte(`{"keys":[{"compile_job_id":"c1","station_slug":"north","mode":"transit","contour_mins":30,"departs_on":"2026-09-02"}]}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Entries []struct {
			Key      handler.IsochroneKey `json:"key"`
			Geometry json.RawMessage      `json:"geometry"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(out.Entries))
	}
	if out.Entries[0].Key.DepartsOn != "2026-09-02" {
		t.Errorf("departs_on = %q, want 2026-09-02", out.Entries[0].Key.DepartsOn)
	}
	var geom map[string]any
	if err := json.Unmarshal(out.Entries[0].Geometry, &geom); err != nil {
		t.Fatalf("geometry: %v", err)
	}
	if geom["day"] != "oct" {
		t.Errorf("geometry = %v, want the October row", geom)
	}
}

func TestWorkerCachePut(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/isochrone-cache", handler.WorkerCachePut(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache",
		bytes.NewReader([]byte(`{"entries":[{"key":{"compile_job_id":"c1","station_slug":"north","mode":"walk","contour_mins":30},"geometry":{"type":"Polygon"}}]}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if len(store.putEntries) != 1 {
		t.Fatalf("put %d entries, want 1", len(store.putEntries))
	}
	if store.putEntries[0].Key.StationSlug != "north" {
		t.Errorf("slug = %q", store.putEntries[0].Key.StationSlug)
	}
}

func TestWorkerCachePutCarriesDepartsOn(t *testing.T) {
	store := &fakeWorkerStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/isochrone-cache", handler.WorkerCachePut(store))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/isochrone-cache",
		bytes.NewReader([]byte(`{"entries":[{"key":{"compile_job_id":"c1","station_slug":"north","mode":"transit","contour_mins":30,"departs_on":"2026-09-02"},"geometry":{"type":"Polygon"}}]}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if len(store.putEntries) != 1 || store.putEntries[0].Key.DepartsOn != "2026-09-02" {
		t.Errorf("put %+v, want departs_on=2026-09-02", store.putEntries)
	}
}

func TestWorkerStoreInternalError(t *testing.T) {
	store := &fakeWorkerStore{runningErr: errors.New("db down")}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/internal/routing-jobs/{id}/running", handler.WorkerMarkRunning(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/internal/routing-jobs/job-1/running", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
