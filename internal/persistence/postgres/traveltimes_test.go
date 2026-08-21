package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestTravelTimesRoundTripPreservesOverrideAndNil(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	const (
		scenarioID = "00000000-0000-4001-8003-000000000001"
		routeID    = "00000000-0000-4002-8003-000000000001"
	)
	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: scenarioID, Slug: "asym", Name: "Asym",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, ScenarioID: ptr(scenarioID), Slug: "main", Name: "Main", Mode: "rail",
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	rev := 400
	want := transit.TravelTimes{
		ScenarioSlug: "asym",
		Provenance:   transit.ProvenanceCalibrated,
		Source:       "test",
		Segments: []transit.SegmentTime{
			{FromSlug: "a", ToSlug: "b", RunSeconds: 600, ReverseRunSeconds: &rev, RouteID: routeID},
			{FromSlug: "b", ToSlug: "c", RunSeconds: 1200, RouteID: routeID},
		},
	}
	if err := repo.UpsertTravelTimes(ctx, want); err != nil {
		t.Fatalf("UpsertTravelTimes: %v", err)
	}

	got, ok, err := repo.GetTravelTimes(ctx, "asym")
	if err != nil || !ok {
		t.Fatalf("GetTravelTimes: ok=%v err=%v", ok, err)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("segments: want 2, got %d", len(got.Segments))
	}

	ab, bc := got.Segments[0], got.Segments[1]
	if ab.FromSlug != "a" || ab.ToSlug != "b" {
		t.Fatalf("first segment: want a→b, got %s→%s", ab.FromSlug, ab.ToSlug)
	}
	if ab.ReverseRunSeconds == nil || *ab.ReverseRunSeconds != 400 {
		t.Errorf("a→b reverse_run_seconds: want 400, got %v", ab.ReverseRunSeconds)
	}
	if bc.FromSlug != "b" || bc.ToSlug != "c" {
		t.Fatalf("second segment: want b→c, got %s→%s", bc.FromSlug, bc.ToSlug)
	}
	if bc.ReverseRunSeconds != nil {
		t.Errorf("b→c reverse_run_seconds: want nil, got %d", *bc.ReverseRunSeconds)
	}
}

func TestSeedLandsReverseRunSecondsOverrides(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	if !seeded {
		t.Fatal("expected seed to run on empty database")
	}

	tt, ok, err := repo.GetTravelTimes(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetTravelTimes: ok=%v err=%v", ok, err)
	}

	var gilroy, bakersfield, millbrae *transit.SegmentTime
	for i := range tt.Segments {
		seg := &tt.Segments[i]
		switch {
		case seg.FromSlug == "gilroy" && seg.ToSlug == "merced":
			gilroy = seg
		case seg.FromSlug == "bakersfield" && seg.ToSlug == "palmdale":
			bakersfield = seg
		case seg.FromSlug == "sf" && seg.ToSlug == "millbrae":
			millbrae = seg
		}
	}
	if gilroy == nil {
		t.Fatal("gilroy→merced segment not seeded")
	}
	if gilroy.ReverseRunSeconds == nil || *gilroy.ReverseRunSeconds != 2940 {
		t.Errorf("gilroy→merced reverse_run_seconds: want 2940, got %v", gilroy.ReverseRunSeconds)
	}
	if bakersfield == nil {
		t.Fatal("bakersfield→palmdale segment not seeded")
	}
	if bakersfield.ReverseRunSeconds == nil || *bakersfield.ReverseRunSeconds != 1490 {
		t.Errorf("bakersfield→palmdale reverse_run_seconds: want 1490, got %v", bakersfield.ReverseRunSeconds)
	}
	if millbrae == nil {
		t.Fatal("sf→millbrae segment not seeded")
	}
	if millbrae.ReverseRunSeconds != nil {
		t.Errorf("sf→millbrae reverse_run_seconds: want nil, got %d", *millbrae.ReverseRunSeconds)
	}
}
