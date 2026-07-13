package isochrone_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeTransitData implements transit.TransitData for tests.
type fakeTransitData struct {
	scenario    transit.Scenario
	stations    []transit.Station
	travelTimes map[[2]string]int
}

func (f *fakeTransitData) GetScenarioBySlug(slug string) (transit.Scenario, bool) {
	if f.scenario.Slug == slug {
		return f.scenario, true
	}
	return transit.Scenario{}, false
}

func (f *fakeTransitData) GetStationsByScenario(scenarioID string) []transit.Station {
	var out []transit.Station
	for _, st := range f.stations {
		if st.ScenarioID == scenarioID {
			out = append(out, st)
		}
	}
	return out
}

func (f *fakeTransitData) GetServicesByScenario(scenarioID string) []transit.Service {
	return nil
}

func (f *fakeTransitData) TravelTimeBetween(_ string, fromSlug, toSlug string) (int, bool) {
	if v, ok := f.travelTimes[[2]string{fromSlug, toSlug}]; ok {
		return v, true
	}
	if v, ok := f.travelTimes[[2]string{toSlug, fromSlug}]; ok {
		return v, true
	}
	return 0, false
}

func newTestData() *fakeTransitData {
	return &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{
			{
				ID: "st1", ScenarioID: "sc1", Slug: "station-a",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.39, 37.71}},
			},
			{
				ID: "st2", ScenarioID: "sc1", Slug: "station-b",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.41, 37.69}},
			},
		},
		travelTimes: map[[2]string]int{
			{"station-a", "station-b"}: 30,
		},
	}
}

func cannedIso() *stadia.IsochroneResponse {
	return &stadia.IsochroneResponse{
		Type: "FeatureCollection",
		Features: []json.RawMessage{
			json.RawMessage(`{"type":"Feature","geometry":{"type":"Polygon","coordinates":[]},"properties":{}}`),
		},
	}
}

func TestChainer_happyPath_twoStations(t *testing.T) {
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{
					{Time: 600, Distance: 1.0},  // station-a: 10 mins
					{Time: 3000, Distance: 5.0}, // station-b: 50 mins
				},
			},
		},
	}
	store := newTestData()
	chainer := isochrone.New(fc, store, logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat:          37.7,
		Lng:          -122.4,
		BudgetMins:   90,
		Mode:         isochrone.ModeWalk,
		ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if resp.Type != "FeatureCollection" {
		t.Errorf("Type: want FeatureCollection, got %q", resp.Type)
	}

	// 1 origin feature + 2 egress features (one per station)
	if len(resp.Features) != 3 {
		t.Errorf("Features len: want 3, got %d", len(resp.Features))
	}
	if len(resp.Metadata.ReachableStations) != 2 {
		t.Errorf("ReachableStations len: want 2, got %d", len(resp.Metadata.ReachableStations))
	}
	if resp.Metadata.WaitModel != "none" {
		t.Errorf("WaitModel: want none, got %q", resp.Metadata.WaitModel)
	}
	if resp.Metadata.OriginBudgetMins != 90 {
		t.Errorf("OriginBudgetMins: want 90, got %d", resp.Metadata.OriginBudgetMins)
	}
}

func TestChainer_noStations(t *testing.T) {
	fc := &stadia.FakeClient{IsochroneResp: cannedIso()}
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{},
	}
	chainer := isochrone.New(fc, store, logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	// Only origin feature
	if len(resp.Features) != 1 {
		t.Errorf("Features len: want 1, got %d", len(resp.Features))
	}
	if len(resp.Metadata.ReachableStations) != 0 {
		t.Errorf("ReachableStations: want 0, got %d", len(resp.Metadata.ReachableStations))
	}
	// Matrix should not have been called since no stations
	if len(fc.MatrixCalls) != 0 {
		t.Errorf("Matrix calls: want 0, got %d", len(fc.MatrixCalls))
	}
}

func TestChainer_allUnreachable(t *testing.T) {
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{
					{Time: -1, Distance: 0},
					{Time: -1, Distance: 0},
				},
			},
		},
	}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(resp.Features) != 1 {
		t.Errorf("Features len: want 1 (origin only), got %d", len(resp.Features))
	}
	if len(resp.Metadata.ReachableStations) != 0 {
		t.Errorf("ReachableStations: want 0, got %d", len(resp.Metadata.ReachableStations))
	}
}

func TestChainer_zeroRemainingExcludesStation(t *testing.T) {
	// budget=30, station-a in 10 mins (remaining=20), station-b only via HSR: 10+30=40 > budget
	// station-b direct: unreachable (-1)
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{
					{Time: 600, Distance: 1.0}, // station-a: 10 mins
					{Time: -1, Distance: 0},    // station-b: unreachable
				},
			},
		},
	}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	// origin + station-a egress only; station-b excluded (30-10-30 = -10 ≤ 0)
	if len(resp.Metadata.ReachableStations) != 1 {
		t.Errorf("ReachableStations: want 1, got %d", len(resp.Metadata.ReachableStations))
	}
	if resp.Metadata.ReachableStations[0].StationSlug != "station-a" {
		t.Errorf("station slug: want station-a, got %q", resp.Metadata.ReachableStations[0].StationSlug)
	}
}

func TestChainer_directAccess_AequalsB(t *testing.T) {
	// Single station, directly reachable; no HSR leg.
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{
			{
				ID: "st1", ScenarioID: "sc1", Slug: "station-a",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.39, 37.71}},
			},
		},
	}
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{{Time: 600, Distance: 1.0}}, // 10 mins
			},
		},
	}
	chainer := isochrone.New(fc, store, logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 60,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(resp.Metadata.ReachableStations) != 1 {
		t.Fatalf("ReachableStations: want 1, got %d", len(resp.Metadata.ReachableStations))
	}
	rs := resp.Metadata.ReachableStations[0]
	if rs.StationSlug != "station-a" {
		t.Errorf("slug: want station-a, got %q", rs.StationSlug)
	}
	if rs.RemainingMins != 50 { // 60 - 10
		t.Errorf("remaining: want 50, got %d", rs.RemainingMins)
	}
	// origin feature + egress feature
	if len(resp.Features) != 2 {
		t.Errorf("Features len: want 2, got %d", len(resp.Features))
	}
}

func TestChainer_concurrentFanOut_noRace(t *testing.T) {
	// 5 reachable stations to trigger concurrent egress fan-out.
	stations := make([]transit.Station, 5)
	matrixCells := make([]stadia.MatrixCell, 5)
	tt := make(map[[2]string]int)
	for i := range 5 {
		slug := string(rune('a' + i))
		stations[i] = transit.Station{
			ID:         slug,
			ScenarioID: "sc1",
			Slug:       "st-" + slug,
			Location:   transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.40 + 0.005*float64(i), 37.70 + 0.005*float64(i)}},
		}
		matrixCells[i] = stadia.MatrixCell{Time: 600, Distance: 1.0}
		for j := range 5 {
			if i != j {
				slugJ := string(rune('a' + j))
				tt[[2]string{"st-" + slug, "st-" + slugJ}] = 10
			}
		}
	}

	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{matrixCells},
		},
	}
	store := &fakeTransitData{
		scenario:    transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations:    stations,
		travelTimes: tt,
	}

	chainer := isochrone.New(fc, store, logger.Discard())
	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 90,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
}

func TestChainer_ErrScenarioNotFound(t *testing.T) {
	fc := &stadia.FakeClient{}
	store := newTestData()
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != isochrone.ErrScenarioNotFound {
		t.Errorf("err: want ErrScenarioNotFound, got %v", err)
	}
	if len(fc.IsochoneCalls) != 0 {
		t.Error("expected no Stadia calls on scenario not found")
	}
}

func TestChainer_ErrInvalidMode(t *testing.T) {
	fc := &stadia.FakeClient{}
	store := newTestData()
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.Mode("teleport"), ScenarioSlug: "test-sc",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != isochrone.ErrInvalidMode {
		t.Errorf("err: want ErrInvalidMode, got %v", err)
	}
	if len(fc.IsochoneCalls) != 0 {
		t.Error("expected no Stadia calls on invalid mode")
	}
}

func TestChainer_haversineFilterExcludesFarStations(t *testing.T) {
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{
			{
				ID: "st1", ScenarioID: "sc1", Slug: "station-near",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.39, 37.71}},
			},
			{
				ID: "st2", ScenarioID: "sc1", Slug: "station-far",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-121.0, 38.5}},
			},
		},
	}
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{{Time: 600, Distance: 1.0}},
			},
		},
	}
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 90,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(fc.MatrixCalls) != 1 {
		t.Fatalf("MatrixCalls: want 1, got %d", len(fc.MatrixCalls))
	}
	if len(fc.MatrixCalls[0].Destinations) != 1 {
		t.Errorf("Destinations: want 1 (far station excluded), got %d", len(fc.MatrixCalls[0].Destinations))
	}
}

func TestChainer_matrixReachClampedToStadiaPathLimit(t *testing.T) {
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{
			{
				ID: "st1", ScenarioID: "sc1", Slug: "station-near",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.39, 37.71}},
			},
			{
				ID: "st2", ScenarioID: "sc1", Slug: "station-30km",
				Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122.06, 37.71}},
			},
		},
	}
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp: &stadia.MatrixResponse{
			SourcesToTargets: [][]stadia.MatrixCell{
				{{Time: 600, Distance: 1.0}},
			},
		},
	}
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 90,
		Mode: isochrone.ModeDrive, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(fc.MatrixCalls) != 1 {
		t.Fatalf("MatrixCalls: want 1, got %d", len(fc.MatrixCalls))
	}
	if got := len(fc.MatrixCalls[0].Destinations); got != 1 {
		t.Errorf("Destinations: want 1 (30 km station beyond 20 km Stadia path limit), got %d", got)
	}
}

func TestChainer_matrixCap600_truncated(t *testing.T) {
	const stationCount = 620
	stations := make([]transit.Station, stationCount)
	matrixCells := make([]stadia.MatrixCell, stationCount)
	for i := range stationCount {
		stations[i] = transit.Station{
			ID:         fmt.Sprintf("st%d", i),
			ScenarioID: "sc1",
			Slug:       fmt.Sprintf("station-%d", i),
			// All within ~1 km of origin — well within walk budget haversine reach.
			Location: transit.GeoPoint{
				Type:        "Point",
				Coordinates: []float64{-122.40 + 0.001*float64(i%30), 37.70 + 0.001*float64(i/30)},
			},
		}
		matrixCells[i] = stadia.MatrixCell{Time: -1, Distance: 0}
	}
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: stations,
	}
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixResp:    &stadia.MatrixResponse{SourcesToTargets: [][]stadia.MatrixCell{matrixCells[:600]}},
	}
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 9999,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(fc.MatrixCalls) != 1 {
		t.Fatalf("MatrixCalls: want 1, got %d", len(fc.MatrixCalls))
	}
	if got := len(fc.MatrixCalls[0].Destinations); got > 600 {
		t.Errorf("Matrix destinations: want ≤600, got %d (Standard plan limit is 625)", got)
	}
}

func TestChainer_drive_largeBudget_noOriginFeature(t *testing.T) {
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{},
	}
	fc := &stadia.FakeClient{IsochroneResp: cannedIso()}
	chainer := isochrone.New(fc, store, logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 60,
		Mode: isochrone.ModeDrive, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if resp.Metadata.OriginIsoAvailable {
		t.Error("OriginIsoAvailable: want false for drive+large budget")
	}
	if !resp.Metadata.OriginIsoClamped {
		t.Error("OriginIsoClamped: want true for drive+large budget")
	}
	for _, f := range resp.Features {
		var feat map[string]any
		if err := json.Unmarshal(f, &feat); err != nil {
			t.Fatalf("unmarshal feature: %v", err)
		}
		props, _ := feat["properties"].(map[string]any)
		if props["source"] == "origin" {
			t.Error("found origin feature in response: drive+clamped should omit origin polygon")
		}
	}
	if len(fc.IsochoneCalls) != 0 {
		t.Errorf("IsochoneCalls: want 0 (origin iso skipped for drive+clamped), got %d", len(fc.IsochoneCalls))
	}
}

func TestChainer_drive_smallBudget_hasOriginFeature(t *testing.T) {
	// budget_mins=10 → budgetSecs=600 < driveMaxSecs=900 → not clamped → origin iso included
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{},
	}
	fc := &stadia.FakeClient{IsochroneResp: cannedIso()}
	chainer := isochrone.New(fc, store, logger.Discard())

	resp, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 10,
		Mode: isochrone.ModeDrive, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if !resp.Metadata.OriginIsoAvailable {
		t.Error("OriginIsoAvailable: want true for drive+small budget (not clamped)")
	}
	if len(fc.IsochoneCalls) != 1 {
		t.Errorf("IsochoneCalls: want 1, got %d", len(fc.IsochoneCalls))
	}
}

func TestChainer_stadiaClientError_propagatesAsErrStadiaClientError(t *testing.T) {
	fc := &stadia.FakeClient{IsochroneErr: stadia.ErrStadiaBadRequest}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, isochrone.ErrStadiaClientError) {
		t.Errorf("want ErrStadiaClientError, got %v", err)
	}
}

func TestChainer_stadiaRateLimit_propagatesAsErrStadiaRateLimit(t *testing.T) {
	fc := &stadia.FakeClient{IsochroneErr: stadia.ErrStadiaRateLimit}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, isochrone.ErrStadiaRateLimit) {
		t.Errorf("want ErrStadiaRateLimit, got %v", err)
	}
}

func TestChainer_stadiaUpstream_propagatesAsErrStadiaUnavailable(t *testing.T) {
	fc := &stadia.FakeClient{IsochroneErr: stadia.ErrStadiaUpstream}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 30,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, isochrone.ErrStadiaUnavailable) {
		t.Errorf("want ErrStadiaUnavailable, got %v", err)
	}
}

func TestChainer_matrixRateLimit_propagatesAsErrStadiaRateLimit(t *testing.T) {
	fc := &stadia.FakeClient{
		IsochroneResp: cannedIso(),
		MatrixErr:     stadia.ErrStadiaRateLimit,
	}
	chainer := isochrone.New(fc, newTestData(), logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 90,
		Mode: isochrone.ModeWalk, ScenarioSlug: "test-sc",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, isochrone.ErrStadiaRateLimit) {
		t.Errorf("want ErrStadiaRateLimit, got %v", err)
	}
}

func TestChainer_isochroneBudgetAboveModeClamp_neverSentUnclamped(t *testing.T) {
	// Verifies that BudgetSecs sent to Stadia never exceeds the per-mode max.
	// bike clamp = 20000m / (15km/h in m/s) = 4800s
	const bikeMaxSecs = 4800
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{},
	}
	fc := &stadia.FakeClient{IsochroneResp: cannedIso()}
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 120,
		Mode: isochrone.ModeBike, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	for _, call := range fc.IsochoneCalls {
		if call.BudgetSecs > bikeMaxSecs {
			t.Errorf("IsochoneCalls BudgetSecs=%d exceeds bike max %d — would trigger Stadia 400", call.BudgetSecs, bikeMaxSecs)
		}
	}
}

func TestChainer_bikeIsoBudgetClamped(t *testing.T) {
	store := &fakeTransitData{
		scenario: transit.Scenario{ID: "sc1", Slug: "test-sc"},
		stations: []transit.Station{},
	}
	fc := &stadia.FakeClient{IsochroneResp: cannedIso()}
	chainer := isochrone.New(fc, store, logger.Discard())

	_, err := chainer.Chain(context.Background(), isochrone.ChainRequest{
		Lat: 37.7, Lng: -122.4, BudgetMins: 90,
		Mode: isochrone.ModeBike, ScenarioSlug: "test-sc",
	})
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(fc.IsochoneCalls) != 1 {
		t.Fatalf("IsochoneCalls: want 1, got %d", len(fc.IsochoneCalls))
	}
	const wantBudget = 4800 // 20000m / (15km/h in m/s) = 4800s
	if fc.IsochoneCalls[0].BudgetSecs != wantBudget {
		t.Errorf("BudgetSecs: want %d (clamped), got %d", wantBudget, fc.IsochoneCalls[0].BudgetSecs)
	}
}
