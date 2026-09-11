package transit_test

import (
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-contract/transit"
)

func TestTravelMode_wireValues(t *testing.T) {
	want := map[transit.TravelMode]string{
		transit.TravelModeWalk:    "walk",
		transit.TravelModeBike:    "bike",
		transit.TravelModeDrive:   "drive",
		transit.TravelModeTransit: "transit",
	}
	for mode, wire := range want {
		if string(mode) != wire {
			t.Errorf("%q = %q, want %q", mode, mode, wire)
		}
		if !mode.Valid() {
			t.Errorf("%q is not Valid", mode)
		}
	}
	if got := transit.TravelModes(); !slices.Equal(got, []transit.TravelMode{
		transit.TravelModeWalk, transit.TravelModeBike, transit.TravelModeDrive, transit.TravelModeTransit,
	}) {
		t.Errorf("TravelModes() = %v", got)
	}
	if got := transit.TravelModeList(); got != "walk, bike, drive, transit" {
		t.Errorf("TravelModeList() = %q", got)
	}
}
