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

func TestHaversineKm_oneDegreeOfLatitude(t *testing.T) {
	got := geo.HaversineKm(0, 0, 1, 0)
	if math.Abs(got-111.19) > 0.05 {
		t.Errorf("one degree of latitude = %v km, want ~111.19", got)
	}
}

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

func TestReachKm_nonPositiveBudgetReachesNothing(t *testing.T) {
	for _, mins := range []int{0, -1, -240} {
		if got := geo.ReachKm(geo.WalkSpeedKmH, mins); got != 0 {
			t.Errorf("ReachKm(walk, %d) = %v, want 0", mins, got)
		}
	}
}

func TestReachKm_isLooserThanTheWorkersPreFilter(t *testing.T) {
	const workerDetourFactor = 1.4

	reach := geo.ReachKm(geo.WalkSpeedKmH, 60)
	workerPreFilter := reach / workerDetourFactor
	if reach <= workerPreFilter {
		t.Errorf("reach %v km is not looser than the worker's %v km pre-filter", reach, workerPreFilter)
	}
}
