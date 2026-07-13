package stadia_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
)

func TestFakeClient_Isochrone(t *testing.T) {
	want := &stadia.IsochroneResponse{
		Type:     "FeatureCollection",
		Features: []json.RawMessage{json.RawMessage(`{"type":"Feature"}`)},
	}
	fc := &stadia.FakeClient{IsochroneResp: want}

	got, err := fc.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Type != want.Type {
		t.Errorf("Type: want %q, got %q", want.Type, got.Type)
	}
	if len(got.Features) != len(want.Features) {
		t.Errorf("Features len: want %d, got %d", len(want.Features), len(got.Features))
	}
	if len(fc.IsochoneCalls) != 1 {
		t.Errorf("IsochoneCalls: want 1, got %d", len(fc.IsochoneCalls))
	}
}

func TestFakeClient_Matrix(t *testing.T) {
	want := &stadia.MatrixResponse{
		SourcesToTargets: [][]stadia.MatrixCell{{{Time: 600, Distance: 1.0}}},
	}
	fc := &stadia.FakeClient{MatrixResp: want}

	got, err := fc.Matrix(context.Background(), stadia.MatrixRequest{
		Origins:      []stadia.LatLng{{Lat: 37.7, Lng: -122.4}},
		Destinations: []stadia.LatLng{{Lat: 37.8, Lng: -122.3}},
		Costing:      stadia.CostingPedestrian,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SourcesToTargets[0][0].Time != 600 {
		t.Errorf("time: want 600, got %d", got.SourcesToTargets[0][0].Time)
	}
	if len(fc.MatrixCalls) != 1 {
		t.Errorf("MatrixCalls: want 1, got %d", len(fc.MatrixCalls))
	}
}

func TestHTTPClient_Isochrone_requestShape(t *testing.T) {
	var capturedBody map[string]any
	var capturedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"FeatureCollection","features":[]}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedAuth != "Stadia-Auth test-key" {
		t.Errorf("Authorization: want %q, got %q", "Stadia-Auth test-key", capturedAuth)
	}

	locs, ok := capturedBody["locations"].([]any)
	if !ok || len(locs) != 1 {
		t.Fatalf("locations: want 1-element array, got %v", capturedBody["locations"])
	}
	loc := locs[0].(map[string]any)
	if loc["lat"] != 37.7 || loc["lon"] != -122.4 {
		t.Errorf("location: want lat=37.7 lon=-122.4, got %v", loc)
	}
	if capturedBody["costing"] != "pedestrian" {
		t.Errorf("costing: want pedestrian, got %v", capturedBody["costing"])
	}
	contours, ok := capturedBody["contours"].([]any)
	if !ok || len(contours) != 1 {
		t.Fatalf("contours: want 1-element array, got %v", capturedBody["contours"])
	}
	contour := contours[0].(map[string]any)
	if contour["time"] != float64(60) {
		t.Errorf("contours[0].time: want 60, got %v", contour["time"])
	}
	if capturedBody["polygons"] != true {
		t.Errorf("polygons: want true, got %v", capturedBody["polygons"])
	}
}

func TestHTTPClient_Matrix_requestShape(t *testing.T) {
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sources_to_targets":[[{"time":600,"distance":1.0}]]}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Matrix(context.Background(), stadia.MatrixRequest{
		Origins:      []stadia.LatLng{{Lat: 37.7, Lng: -122.4}},
		Destinations: []stadia.LatLng{{Lat: 37.8, Lng: -122.3}},
		Costing:      stadia.CostingPedestrian,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := capturedBody["sources"]; !ok {
		t.Error("request body missing 'sources'")
	}
	if _, ok := capturedBody["targets"]; !ok {
		t.Error("request body missing 'targets'")
	}
	if capturedBody["costing"] != "pedestrian" {
		t.Errorf("costing: want pedestrian, got %v", capturedBody["costing"])
	}
}

func TestHTTPClient_nonOK_returnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:  stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing: stadia.CostingPedestrian,
	})
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}

func TestHTTPClient_400_returnsErrStadiaBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error_code":154,"error":"Path distance exceeds the max distance limit 20000 meters","status_code":400,"status":"Bad Request"}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, stadia.ErrStadiaBadRequest) {
		t.Errorf("want ErrStadiaBadRequest, got %v", err)
	}
}

func TestHTTPClient_429_returnsErrStadiaRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Rate Limit Exceeded"}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, stadia.ErrStadiaRateLimit) {
		t.Errorf("want ErrStadiaRateLimit, got %v", err)
	}
}

func TestHTTPClient_503_returnsErrStadiaUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Service Unavailable"}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, stadia.ErrStadiaUpstream) {
		t.Errorf("want ErrStadiaUpstream, got %v", err)
	}
}

func TestHTTPClient_400_emptyBody_returnsErrStadiaBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7, Lng: -122.4},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 3600,
	})
	if !errors.Is(err, stadia.ErrStadiaBadRequest) {
		t.Errorf("want ErrStadiaBadRequest even with empty body, got %v", err)
	}
}

func TestHTTPClient_Matrix_429_returnsErrStadiaRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Rate Limit Exceeded"}`))
	}))
	defer srv.Close()

	c := stadia.NewHTTPClientWithBase(srv.URL+"/isochrone/v1", srv.URL+"/matrix/v1", "test-key")
	_, err := c.Matrix(context.Background(), stadia.MatrixRequest{
		Origins:      []stadia.LatLng{{Lat: 37.7, Lng: -122.4}},
		Destinations: []stadia.LatLng{{Lat: 37.8, Lng: -122.3}},
		Costing:      stadia.CostingPedestrian,
	})
	if !errors.Is(err, stadia.ErrStadiaRateLimit) {
		t.Errorf("want ErrStadiaRateLimit, got %v", err)
	}
}

func TestHTTPClient_integration(t *testing.T) {
	key := os.Getenv("STADIA_API_KEY")
	if key == "" {
		t.Skip("requires STADIA_API_KEY")
	}
	c := stadia.NewHTTPClient(key)
	resp, err := c.Isochrone(context.Background(), stadia.IsochroneRequest{
		Origin:     stadia.LatLng{Lat: 37.7749, Lng: -122.4194},
		Costing:    stadia.CostingPedestrian,
		BudgetSecs: 900,
	})
	if err != nil {
		t.Fatalf("Isochrone: %v", err)
	}
	if resp.Type != "FeatureCollection" {
		t.Errorf("Type: want FeatureCollection, got %q", resp.Type)
	}
}
