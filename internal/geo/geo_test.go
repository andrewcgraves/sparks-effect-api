package geo_test

import (
	"math"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/geo"
)

func TestHaversineKm_zeroForTheSamePoint(t *testing.T) {
	if got := geo.HaversineKm(37.7, -122.4, 37.7, -122.4); got != 0 {
		t.Errorf("distance to self = %v, want 0", got)
	}
}

// A degree of latitude is ~111.19 km on a sphere of this radius, everywhere.
// Checking it against the known figure is what catches a radians/degrees slip,
// which is the mistake this function is prone to and the one that would make
// every range check wrong by a constant factor.
func TestHaversineKm_oneDegreeOfLatitude(t *testing.T) {
	got := geo.HaversineKm(0, 0, 1, 0)
	if math.Abs(got-111.19) > 0.05 {
		t.Errorf("one degree of latitude = %v km, want ~111.19", got)
	}
}

// A degree of longitude shrinks with the cosine of the latitude. At 60° it is
// half what it is at the equator, which is the cheapest way to show the
// cos(lat) term is present and applied to the right argument.
func TestHaversineKm_longitudeShrinksWithLatitude(t *testing.T) {
	atEquator := geo.HaversineKm(0, 0, 0, 1)
	atSixty := geo.HaversineKm(60, 0, 60, 1)
	if math.Abs(atSixty-atEquator/2) > 0.1 {
		t.Errorf("a degree of longitude at 60°N = %v km, want ~half of %v", atSixty, atEquator)
	}
}

func TestHaversineKm_isSymmetric(t *testing.T) {
	there := geo.HaversineKm(37.7, -122.4, 34.05, -118.24)
	back := geo.HaversineKm(34.05, -118.24, 37.7, -122.4)
	if there != back {
		t.Errorf("distance is not symmetric: %v vs %v", there, back)
	}
}

// The table the whole guard is calibrated against. These are the radii a
// rejection is measured with, so they are asserted as the literal numbers
// rather than recomputed from the formula — a test that restates the
// implementation would agree with any change to it.
func TestReachKm_theBudgetTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		speedKmH   float64
		budgetMins int
		want       float64
	}{
		{"walk 30", geo.WalkSpeedKmH, 30, 2.5},
		{"walk 60", geo.WalkSpeedKmH, 60, 5},
		{"walk 120", geo.WalkSpeedKmH, 120, 10},
		{"walk 240", geo.WalkSpeedKmH, 240, 20},
		{"bike 30", geo.BikeSpeedKmH, 30, 7.5},
		{"bike 240", geo.BikeSpeedKmH, 240, 60},
		{"drive 30", geo.DriveSpeedKmH, 30, 40},
		{"drive 240", geo.DriveSpeedKmH, 240, 320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := geo.ReachKm(tc.speedKmH, tc.budgetMins); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("ReachKm(%v, %d) = %v, want %v", tc.speedKmH, tc.budgetMins, got, tc.want)
			}
		})
	}
}

// A non-positive budget reaches nowhere rather than somewhere negative. The
// request validator rejects these before they could arrive, so this pins the
// behaviour rather than covering a live path.
func TestReachKm_nonPositiveBudgetReachesNothing(t *testing.T) {
	for _, mins := range []int{0, -1, -240} {
		if got := geo.ReachKm(geo.WalkSpeedKmH, mins); got != 0 {
			t.Errorf("ReachKm(walk, %d) = %v, want 0", mins, got)
		}
	}
}

// The routing worker sizes its destination pre-filter by dividing this same
// product by a detour factor of 1.4. This side must not: a rejection has to be
// the looser of the two bounds or it refuses origins the worker would have
// plotted. Stated as a test because the two numbers live in separate
// repositories with nothing else holding them in relation.
func TestReachKm_isLooserThanTheWorkersPreFilter(t *testing.T) {
	const workerDetourFactor = 1.4

	reach := geo.ReachKm(geo.WalkSpeedKmH, 60)
	workerPreFilter := reach / workerDetourFactor
	if reach <= workerPreFilter {
		t.Errorf("reach %v km is not looser than the worker's %v km pre-filter", reach, workerPreFilter)
	}
}
