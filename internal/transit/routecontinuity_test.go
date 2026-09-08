package transit

import (
	"math"
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

const (
	continuityChordM       = 500.0
	continuityToleranceDeg = 8.0
	continuityReversalDeg  = 170.0
	continuityApproachM    = 2000.0
	continuityDenseMedianM = 100.0
)

func TestSeededRouteAlignmentsDoNotKink(t *testing.T) {
	store := mustNewStore(t)

	checked := 0
	for _, sc := range store.GetScenarios() {
		stopsByRoute := servedStopLocations(store, sc.ID)

		for _, rt := range store.GetRoutesByScenario(sc.ID) {
			line := rt.Geometry.Coordinates
			if len(line) < 3 {
				continue
			}
			if medianSpacingM(line) > continuityDenseMedianM {
				t.Logf("route %q: median vertex spacing over %.0f m, not survey-grade geometry — skipped",
					rt.Slug, continuityDenseMedianM)
				continue
			}
			checked++
			checkAlignmentContinuity(t, rt.Slug, line, stopsByRoute[rt.ID])
		}
	}

	// Every route being skipped would make this test vacuously green, which is
	// the one failure mode a continuity check cannot report on itself.
	if checked == 0 {
		t.Fatal("no seeded route was dense enough to check; this test asserted nothing")
	}
}

func checkAlignmentContinuity(t *testing.T, slug string, line [][]float64, stops []physics.Point) {
	t.Helper()

	lengths := make([]float64, len(line)-1)
	bearings := make([]float64, len(line)-1)
	for i := range lengths {
		a, b := asPoint(line[i]), asPoint(line[i+1])
		lengths[i] = physics.DistanceM(a, b)
		bearings[i] = bearingDeg(a, b)
	}

	for i, length := range lengths {
		if length <= continuityChordM {
			continue
		}
		// A duplicated vertex has no direction, so the neighbour to compare
		// against is the nearest one that does.
		if j := prevRealSegment(lengths, i); j >= 0 {
			reportKink(t, slug, i, length, bearings[i], bearings[j], line[i], stops)
		}
		if j := nextRealSegment(lengths, i); j >= 0 {
			reportKink(t, slug, i, length, bearings[i], bearings[j], line[i+1], stops)
		}
	}
}

func reportKink(t *testing.T, slug string, i int, length, chord, neighbour float64, turn []float64, stops []physics.Point) {
	t.Helper()

	dev := bearingDeltaDeg(chord, neighbour)
	if dev <= continuityToleranceDeg {
		return
	}
	if dev >= continuityReversalDeg {
		if d, ok := nearestStopM(asPoint(turn), stops); ok && d <= continuityApproachM {
			return
		}
	}
	t.Errorf("route %q: the %.0f m chord at vertex %d turns %.1f° off the track beside it "+
		"(over the %.1f° tolerance) — a chord that long should be a tangent, so this looks "+
		"like source features stitched out of order",
		slug, length, i, dev, continuityToleranceDeg)
}

func servedStopLocations(store *Store, scenarioID string) map[string][]physics.Point {
	byID := make(map[string]Station)
	for _, st := range store.GetStationsByScenario(scenarioID) {
		byID[st.ID] = st
	}

	out := make(map[string][]physics.Point)
	for _, svc := range store.GetServicesByScenario(scenarioID) {
		for _, stop := range svc.Stops {
			st, ok := byID[stop.StationID]
			if !ok || len(st.Location.Coordinates) < 2 {
				continue
			}
			p := asPoint(st.Location.Coordinates)
			if !slices.Contains(out[svc.RouteID], p) {
				out[svc.RouteID] = append(out[svc.RouteID], p)
			}
		}
	}
	return out
}

func nearestStopM(p physics.Point, stops []physics.Point) (float64, bool) {
	best := math.Inf(1)
	for _, s := range stops {
		if d := physics.DistanceM(p, s); d < best {
			best = d
		}
	}
	return best, !math.IsInf(best, 1)
}

func medianSpacingM(line [][]float64) float64 {
	lengths := make([]float64, len(line)-1)
	for i := range lengths {
		lengths[i] = physics.DistanceM(asPoint(line[i]), asPoint(line[i+1]))
	}
	slices.Sort(lengths)
	return lengths[len(lengths)/2]
}

func prevRealSegment(lengths []float64, i int) int {
	for j := i - 1; j >= 0; j-- {
		if lengths[j] > 0 {
			return j
		}
	}
	return -1
}

func nextRealSegment(lengths []float64, i int) int {
	for j := i + 1; j < len(lengths); j++ {
		if lengths[j] > 0 {
			return j
		}
	}
	return -1
}

func asPoint(c []float64) physics.Point {
	return physics.Point{Lng: c[0], Lat: c[1]}
}

func bearingDeg(a, b physics.Point) float64 {
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	y := math.Sin(dLng) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLng)
	return math.Atan2(y, x) * 180 / math.Pi
}

func bearingDeltaDeg(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	return math.Min(d, 360-d)
}
