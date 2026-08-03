package transit

import (
	"math"
	"slices"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/physics"
)

// A route alignment is stitched together out of source features, and the way
// that goes wrong is silent. Concatenating features — or the parts of one
// multi-part feature — in dataset order rather than end-to-end order leaves the
// line teleporting from where one piece stopped to where the next begins, and
// nothing downstream complains: the geometry is still a valid LineString, the
// stations still snap to it, and the only symptom is chainage (and so every
// distance and run time derived from it) being quietly wrong.
//
// The CA HSR Phase 1 alignment shipped with three such defects: a 28 km jump at
// the Central Valley Wye where a whole source feature had been left out, a
// 4.2 km 180° backtrack where one feature's two parts were concatenated in
// dataset order, and a 0.9 km corner cut where four parts of the LA–Anaheim
// feature were dropped.
//
// # Why this measures bearing and not gap length
//
// The obvious check — "no segment longer than N" — cannot work on this data.
// The source is CADD-converted MicroStation survey geometry: it samples curves
// at about 10 m but stores a straight tangent as a single two-vertex chord, so
// the longest segment in the corrected Phase 1 line is an 11.5 km straight past
// Kings/Tulare that is entirely correct. Of the 61 segments over 2 km, 58 are
// tangents like that one. Length does not separate them.
//
// Bearing does, decisively. A tangent chord runs in the same direction as the
// track either side of it — across every benign long segment in Phase 1 the
// bearing deviates by at most 0.68°. A stitching defect turns a corner: the
// three real ones deviated by 14.4°, 80.0° and 180.0°. The threshold below sits
// in the order-of-magnitude gap between those two populations.
const (
	// continuityChordM is how long a segment must be before this check applies.
	// Below it, a segment is short enough to be ordinary curve sampling, where
	// a real bearing change between consecutive vertices is expected.
	continuityChordM = 500.0

	// continuityToleranceDeg is how far a long chord's bearing may differ from
	// the track either side of it. Benign tangents stay under 3.2°; the
	// smallest real defect was 14.4°.
	continuityToleranceDeg = 8.0

	// continuityReversalDeg is the deviation above which a turn is a reversal —
	// the line doubling back on itself rather than merely bending.
	continuityReversalDeg = 170.0

	// continuityApproachM is how close to a station a reversal must be to count
	// as a service reversing at that station rather than a stitching defect.
	// Phase 1's one legitimate reversal turns 823 m from Merced; its two
	// spurious ones turned 27.7 km and 29.9 km from the nearest station.
	continuityApproachM = 2000.0

	// continuityDenseMedianM is the median vertex spacing at or below which a
	// route is survey-grade geometry, and so subject to this check at all. See
	// the note on skipping below.
	continuityDenseMedianM = 100.0
)

// TestSeededRouteAlignmentsDoNotKink is the regression guard for the stitching
// defects described above: every chord in a seeded alignment long enough to be
// a tangent must run in the direction of the track it joins.
//
// # What is deliberately allowed
//
// A near-180° turn within continuityApproachM of a station the route's own
// services call at. Phase 1 has exactly one, and it is real track: Merced sits
// ~24.6 km up a stub off the Central Valley Wye, so a train calling there
// reverses at Merced and retraces its own alignment (see the note at the head
// of routes.yaml). Away from a station, an identical-looking reversal is a
// stitching defect — that is precisely what the 4.2 km backtrack in the old
// Phase 1 geometry was — so the exemption is anchored to a station rather than
// granted to reversals in general.
//
// # What is deliberately skipped
//
// Routes whose median vertex spacing exceeds continuityDenseMedianM. The
// invariant here — "a long chord is a tangent, so it cannot turn" — is a
// property of densely-sampled survey geometry, and is false by construction for
// a coarse trace: the Brightline West spur is an OpenStreetMap I-15 trace
// simplified with Douglas–Peucker at a 10 m tolerance, whose whole purpose is
// to replace sampled curves with chords, so consecutive chords change bearing
// by design (up to 24° there). Median spacing is the property that decides
// which kind of line this is — Phase 1's is 10.4 m, the spur's is 403 m — so
// the test reads it off the data rather than naming routes it knows about.
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

// checkAlignmentContinuity reports every long chord in line whose bearing
// disagrees with the track it joins, other than a reversal at one of stops.
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

// reportKink fails the test if chord and neighbour disagree by more than the
// tolerance, unless the disagreement is a reversal at a station.
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

// servedStopLocations maps each route to the positions of the stations its
// services actually call at, which is where a reversal in the alignment can be
// a train turning round rather than a defect.
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

// nearestStopM is the distance from p to the closest of stops, and whether
// there were any.
func nearestStopM(p physics.Point, stops []physics.Point) (float64, bool) {
	best := math.Inf(1)
	for _, s := range stops {
		if d := physics.DistanceM(p, s); d < best {
			best = d
		}
	}
	return best, !math.IsInf(best, 1)
}

// medianSpacingM is the median distance between consecutive vertices — the
// median rather than the mean because a handful of multi-kilometre tangents
// would drag the mean of a finely-sampled line well past the threshold.
func medianSpacingM(line [][]float64) float64 {
	lengths := make([]float64, len(line)-1)
	for i := range lengths {
		lengths[i] = physics.DistanceM(asPoint(line[i]), asPoint(line[i+1]))
	}
	slices.Sort(lengths)
	return lengths[len(lengths)/2]
}

// prevRealSegment is the index of the nearest segment before i with a length to
// take a bearing from, or -1.
func prevRealSegment(lengths []float64, i int) int {
	for j := i - 1; j >= 0; j-- {
		if lengths[j] > 0 {
			return j
		}
	}
	return -1
}

// nextRealSegment is the index of the nearest segment after i with a length to
// take a bearing from, or -1.
func nextRealSegment(lengths []float64, i int) int {
	for j := i + 1; j < len(lengths); j++ {
		if lengths[j] > 0 {
			return j
		}
	}
	return -1
}

// asPoint reads a GeoJSON [lng, lat] pair as a physics.Point.
func asPoint(c []float64) physics.Point {
	return physics.Point{Lng: c[0], Lat: c[1]}
}

// bearingDeg is the initial great-circle bearing from a to b, in degrees.
func bearingDeg(a, b physics.Point) float64 {
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	y := math.Sin(dLng) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLng)
	return math.Atan2(y, x) * 180 / math.Pi
}

// bearingDeltaDeg is the absolute angle between two bearings, in [0, 180].
func bearingDeltaDeg(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	return math.Min(d, 360-d)
}
