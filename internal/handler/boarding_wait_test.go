package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestScenarioServices_exposesResolvedBoardingWait(t *testing.T) {
	store, err := transit.NewStore(transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/scenarios/ca-hsr/services", nil)
	req.SetPathValue("slug", "ca-hsr")
	rec := httptest.NewRecorder()
	handler.ScenarioServices(store, transit.DefaultBoardingWaitPolicy()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	var services []transit.Service
	if err := json.Unmarshal(rec.Body.Bytes(), &services); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(services) == 0 {
		t.Fatal("expected services")
	}
	for _, svc := range services {
		if svc.BoardingWaitPolicy != string(transit.BoardingWaitNone) {
			t.Errorf("%s boarding_wait_policy: want %q, got %q", svc.Name, transit.BoardingWaitNone, svc.BoardingWaitPolicy)
		}
		if svc.BoardingWaitSecs != 0 {
			t.Errorf("%s boarding_wait_secs: want 0, got %d", svc.Name, svc.BoardingWaitSecs)
		}
	}

	// half_headway resolves to the known CA HSR waits without the client
	// re-deriving min(headway)/2 itself.
	rec = httptest.NewRecorder()
	handler.ScenarioServices(store, transit.BoardingWaitPolicy{Kind: transit.BoardingWaitHalfHeadway}).ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &services); err != nil {
		t.Fatalf("decode half_headway: %v", err)
	}
	byName := map[string]transit.Service{}
	for _, svc := range services {
		byName[svc.Name] = svc
	}
	if got := byName["HSR Local"].BoardingWaitSecs; got != 1800 {
		t.Errorf("HSR Local boarding_wait_secs: want 1800, got %d", got)
	}
	if got := byName["Brightline West"].BoardingWaitSecs; got != 3600 {
		t.Errorf("Brightline West boarding_wait_secs: want 3600, got %d", got)
	}
}
