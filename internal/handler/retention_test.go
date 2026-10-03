package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
)

type fakeRetentionStore struct {
	report handler.RetentionReport
	err    error
	apply  *bool
}

func (f *fakeRetentionStore) ApplyRetention(_ context.Context, apply bool) (handler.RetentionReport, error) {
	f.apply = &apply
	return f.report, f.err
}

func postRetention(body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, "/api/admin/retention", http.NoBody)
	} else {
		r = httptest.NewRequest(http.MethodPost, "/api/admin/retention", strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestRetentionDryRunAndApply(t *testing.T) {
	store := &fakeRetentionStore{report: handler.RetentionReport{
		DryRun:                     true,
		IsochroneCacheRows:         3,
		IsochroneCacheSuperseded:   2,
		IsochroneCacheStaleTransit: 1,
		RoutingJobResultsCleared:   4,
	}}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := handler.Retention(store)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postRetention(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty body status = %d, body %s", rec.Code, rec.Body.String())
	}
	if store.apply == nil || *store.apply {
		t.Fatalf("empty body apply = %v, want false", store.apply)
	}
	assertRetentionJSON(t, rec.Body.Bytes(), store.report)
	if !strings.Contains(logs.String(), "handler: retention dry-run") ||
		!strings.Contains(logs.String(), `"isochrone_cache_rows":3`) {
		t.Errorf("dry-run log = %s", logs.String())
	}

	logs.Reset()
	store.apply = nil
	store.report.DryRun = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, postRetention(`{"apply":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body %s", rec.Code, rec.Body.String())
	}
	if store.apply == nil || !*store.apply {
		t.Fatalf("apply flag = %v, want true", store.apply)
	}
	assertRetentionJSON(t, rec.Body.Bytes(), store.report)
	if !strings.Contains(logs.String(), "handler: retention applied") {
		t.Errorf("apply log = %s", logs.String())
	}

	store.apply = nil
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, postRetention(`{"apply":false}`))
	if rec.Code != http.StatusOK {
		t.Fatalf(`{"apply":false} status = %d`, rec.Code)
	}
	if store.apply == nil || *store.apply {
		t.Fatalf(`{"apply":false} apply = %v, want false`, store.apply)
	}
}

func TestRetentionRejectsMalformedBodies(t *testing.T) {
	store := &fakeRetentionStore{}
	h := handler.Retention(store)
	for _, body := range []string{
		`{`,
		`{"apply":"true"}`,
		`{"apply":1}`,
		`{"apply":null}`,
		`{"aplly":true}`,
		`{"apply":false,"extra":1}`,
		`[]`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			store.apply = nil
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, postRetention(body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if store.apply != nil {
				t.Error("store was called for a rejected body")
			}
		})
	}
}

func TestRetentionStoreError(t *testing.T) {
	store := &fakeRetentionStore{err: errors.New("db down")}
	h := handler.Retention(store)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postRetention(`{"apply":true}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func assertRetentionJSON(t *testing.T, body []byte, want handler.RetentionReport) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response %s: %v", body, err)
	}
	wantKeys := []string{
		"dry_run",
		"isochrone_cache_rows",
		"isochrone_cache_superseded",
		"isochrone_cache_stale_transit",
		"routing_job_results_cleared",
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("keys = %v, want %v", keysOf(got), wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in %s", k, body)
		}
	}
	if got["dry_run"] != want.DryRun {
		t.Errorf("dry_run = %v, want %v", got["dry_run"], want.DryRun)
	}
	for _, c := range []struct {
		key  string
		want int64
	}{
		{"isochrone_cache_rows", want.IsochroneCacheRows},
		{"isochrone_cache_superseded", want.IsochroneCacheSuperseded},
		{"isochrone_cache_stale_transit", want.IsochroneCacheStaleTransit},
		{"routing_job_results_cleared", want.RoutingJobResultsCleared},
	} {
		n, ok := got[c.key].(float64)
		if !ok || int64(n) != c.want {
			t.Errorf("%s = %v, want %d", c.key, got[c.key], c.want)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
