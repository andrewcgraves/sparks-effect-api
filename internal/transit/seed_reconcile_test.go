package transit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReconcileSeed_insertsWhenMissing(t *testing.T) {
	sink := &fakeSeedSink{}
	written, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written == 0 {
		t.Fatal("expected writes into an empty sink")
	}
	if len(sink.scenarios) != 1 || sink.scenarios[0].Slug != "ca-hsr" {
		t.Fatalf("scenarios = %+v, want one ca-hsr", sink.scenarios)
	}
	if sink.updates != 0 {
		t.Fatalf("updates = %d, want 0 on a first insert", sink.updates)
	}
}

func TestReconcileSeed_isIdempotentOnMatchingRows(t *testing.T) {
	sink := &fakeSeedSink{}
	if _, err := ReconcileSeed(context.Background(), sink); err != nil {
		t.Fatalf("first ReconcileSeed: %v", err)
	}
	creates := len(sink.routes) + len(sink.stations) + len(sink.services)
	written, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("second ReconcileSeed: %v", err)
	}
	if written != 0 {
		t.Fatalf("second run wrote %d rows, want 0", written)
	}
	if sink.updates != 0 {
		t.Fatalf("updates = %d, want 0", sink.updates)
	}
	if got := len(sink.routes) + len(sink.stations) + len(sink.services); got != creates {
		t.Fatalf("row count changed on a no-op reconcile: %d → %d", creates, got)
	}
}

func TestReconcileSeed_restoresADriftedStation(t *testing.T) {
	sink := &fakeSeedSink{}
	if _, err := ReconcileSeed(context.Background(), sink); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var (
		idx  = -1
		want GeoPoint
		slug string
	)
	for i, st := range sink.stations {
		if st.Slug == "las-vegas" {
			idx = i
			want = st.Location
			slug = st.Slug
			break
		}
	}
	if idx < 0 {
		t.Fatal("seeded las-vegas station not found")
	}
	sink.stations[idx].Location = GeoPoint{Type: "Point", Coordinates: []float64{-115.136, 36.174}}

	written, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written != 1 {
		t.Fatalf("wrote %d rows, want 1 (the drifted station)", written)
	}
	if sink.updates != 1 {
		t.Fatalf("updates = %d, want 1", sink.updates)
	}
	got := sink.stations[idx].Location
	if got.Type != want.Type || len(got.Coordinates) != len(want.Coordinates) ||
		got.Coordinates[0] != want.Coordinates[0] || got.Coordinates[1] != want.Coordinates[1] {
		t.Errorf("%s location = %+v, want %+v", slug, got, want)
	}

	again, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("third ReconcileSeed: %v", err)
	}
	if again != 0 {
		t.Fatalf("reconcile after restore wrote %d rows, want 0", again)
	}
}

func TestReconcileSeed_skipsAnAuthoredScenarioWithASeededSlug(t *testing.T) {
	owner := "owner-1"
	sink := &fakeSeedSink{
		scenarios: []Scenario{{
			ID: "authored-ca-hsr", Slug: "ca-hsr", Name: "Mine", OwnerID: &owner,
		}},
	}
	written, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written != 0 {
		t.Fatalf("wrote %d rows, want 0", written)
	}
	if sink.scenarios[0].Name != "Mine" {
		t.Errorf("authored name = %q, want unchanged", sink.scenarios[0].Name)
	}
	if len(sink.routes) != 0 || len(sink.stations) != 0 {
		t.Fatal("must not write seed children into an authored scenario")
	}
}

func TestReconcileSeed_skipsAnAuthoredRoute(t *testing.T) {
	sink := &fakeSeedSink{}
	if _, err := ReconcileSeed(context.Background(), sink); err != nil {
		t.Fatalf("seed: %v", err)
	}
	owner := "owner-1"
	if len(sink.routes) == 0 {
		t.Fatal("expected seeded routes")
	}
	sink.routes[0].OwnerID = &owner
	sink.routes[0].Geometry = GeoLineString{Type: "LineString", Coordinates: [][]float64{{0, 0}, {1, 1}}}

	written, err := ReconcileSeed(context.Background(), sink)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written != 0 {
		t.Fatalf("wrote %d rows, want 0 (authored route must not be clobbered)", written)
	}
	if sink.routes[0].Geometry.Coordinates[0][0] != 0 {
		t.Error("authored route geometry was overwritten")
	}
}

func TestReconcileSeed_listError(t *testing.T) {
	sink := &fakeSeedSink{listErr: errors.New("db down")}
	_, err := ReconcileSeed(context.Background(), sink)
	if err == nil || !strings.Contains(err.Error(), "looking up scenario") {
		t.Fatalf("got %v, want a lookup error", err)
	}
}
