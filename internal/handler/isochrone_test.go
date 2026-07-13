package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
)

type fakeChainer struct {
	resp *isochrone.ChainResponse
	err  error
}

func (f *fakeChainer) Chain(_ context.Context, _ isochrone.ChainRequest) (*isochrone.ChainResponse, error) {
	return f.resp, f.err
}

func cannedChainResp() *isochrone.ChainResponse {
	return &isochrone.ChainResponse{
		Type:     "FeatureCollection",
		Features: []json.RawMessage{json.RawMessage(`{"type":"Feature","geometry":null,"properties":{"source":"origin"}}`)},
		Metadata: isochrone.ChainMetadata{
			ReachableStations: []isochrone.ReachableStation{},
			OriginBudgetMins:  30,
			ScenarioSlug:      "ca-hsr",
			Mode:              "walk",
			WaitModel:         "none",
		},
	}
}

func postIsochrone(chainer isochrone.Chainer, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/isochrone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	Isochrone(chainer, logger.Discard())(rec, req)
	return rec
}

func TestIsochrone_200_validRequest(t *testing.T) {
	rec := postIsochrone(&fakeChainer{resp: cannedChainResp()},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["type"] != "FeatureCollection" {
		t.Errorf("type: want FeatureCollection, got %v", body["type"])
	}
	if _, ok := body["metadata"]; !ok {
		t.Error("response missing metadata")
	}
}

func TestIsochrone_400_invalidMode(t *testing.T) {
	rec := postIsochrone(&fakeChainer{resp: cannedChainResp()},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected non-empty error field")
	}
}

func TestIsochrone_400_zeroBudget(t *testing.T) {
	rec := postIsochrone(&fakeChainer{resp: cannedChainResp()},
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "budget_mins must be greater than 0" {
		t.Errorf("error: want 'budget_mins must be greater than 0', got %q", body["error"])
	}
}

func TestIsochrone_400_malformedJSON(t *testing.T) {
	rec := postIsochrone(&fakeChainer{resp: cannedChainResp()}, `{not valid json}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestIsochrone_404_scenarioNotFound(t *testing.T) {
	rec := postIsochrone(&fakeChainer{err: isochrone.ErrScenarioNotFound},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "scenario not found" {
		t.Errorf("error: want 'scenario not found', got %q", body["error"])
	}
}

func TestIsochrone_502_stadiaError(t *testing.T) {
	wrappedErr := fmt.Errorf("%w: connection refused", isochrone.ErrStadiaUnavailable)
	rec := postIsochrone(&fakeChainer{err: wrappedErr},
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "routing service unavailable" {
		t.Errorf("error: want 'routing service unavailable', got %q", body["error"])
	}
}

func TestIsochrone_contentType(t *testing.T) {
	cases := []struct {
		name string
		body string
		fc   *fakeChainer
	}{
		{"200", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`, &fakeChainer{resp: cannedChainResp()}},
		{"400-budget", `{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk","scenario_slug":"ca-hsr"}`, &fakeChainer{}},
		{"404", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"nope"}`, &fakeChainer{err: isochrone.ErrScenarioNotFound}},
		{"502", `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`, &fakeChainer{err: isochrone.ErrStadiaUnavailable}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postIsochrone(tc.fc, tc.body)
			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type: want application/json, got %q", ct)
			}
		})
	}
}
