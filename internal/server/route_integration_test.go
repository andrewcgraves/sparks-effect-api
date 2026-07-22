package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file exercises SPA-104 end-to-end against a real database and the real
// mux: an admin ingests alignments, and an anonymous client — the route picker,
// which runs before anyone has signed in — discovers them without knowing a
// slug in advance.

// The headline acceptance criterion: what an admin ingests is what the list
// offers, addressed by slug and stripped to what a picker renders.
func TestIntegration_IngestedRoutesAreDiscoverableFromTheRouteList(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)

	// Before any ingestion the list exists and is empty, rather than 404ing as
	// if the endpoint were missing.
	if got := getRouteList(t, h); len(got) != 0 {
		t.Fatalf("route list before ingestion = %+v, want empty", got)
	}

	for _, body := range []string{
		`{"type":"LineString","coordinates":[[-122.4,37.79],[-122.3,37.70]],
			"properties":{"name":"Peninsula Line","mode":"rail"}}`,
		`{"type":"LineString","coordinates":[[-118.2,34.05],[-118.1,34.10]],
			"properties":{"name":"Eastside Metro","mode":"metro"}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/routes", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("ingesting route: status %d, body %s", rec.Code, rec.Body.String())
		}
	}

	got := getRouteList(t, h)
	if len(got) != 2 {
		t.Fatalf("route list = %+v, want 2 routes", got)
	}

	// Slug order, so the list a picker renders is stable between calls.
	want := []map[string]any{
		{"slug": "eastside-metro", "name": "Eastside Metro", "mode": "metro"},
		{"slug": "peninsula-line", "name": "Peninsula Line", "mode": "rail"},
	}
	for i, w := range want {
		if len(got[i]) != len(w) {
			t.Errorf("route %d = %+v, want exactly the picker fields %+v", i, got[i], w)
			continue
		}
		for k, v := range w {
			if got[i][k] != v {
				t.Errorf("route %d %s = %v, want %v", i, k, got[i][k], v)
			}
		}
	}

	// The slug the list hands back is the one the detail read answers to — the
	// picker's output feeds straight into the next call with no ID conversion.
	rec := request(t, h, http.MethodGet, "/api/routes/peninsula-line", "")
	if rec.Code != http.StatusOK {
		t.Errorf("reading a listed slug: status %d, body %s", rec.Code, rec.Body.String())
	}
}

// getRouteList fetches the list anonymously — the posture the picker relies on —
// decoding loosely so fields that must be absent are proven absent.
func getRouteList(t *testing.T, h http.Handler) []map[string]any {
	t.Helper()
	rec := request(t, h, http.MethodGet, "/api/routes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/routes: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode route list: %v", err)
	}
	return got
}
