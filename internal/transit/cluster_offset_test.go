package transit

import (
	"testing"
)

// metersPerDegLatOffsetTest is one degree of latitude in the compiler's local
// planar metric (R * pi/180 = 6371000 * pi/180), the same figure cluster_test.go
// and snap_test.go each derive independently. Latitude is the clean axis for
// these fixtures because, unlike longitude, it does not vary with where on
// Earth the fixture sits.
const metersPerDegLatOffsetTest = 111194.926644

// degLatOffsetTest converts a metre distance to degrees of latitude.
func degLatOffsetTest(m float64) float64 { return m / metersPerDegLatOffsetTest }

// eastWestLine builds a route running due east along a fixed latitude, long
// enough to hold every stop these fixtures place along it.
func eastWestLine(slug string, lat float64) Route {
	return Route{
		ID:   slug,
		Slug: slug,
		Name: slug,
		Geometry: GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{-122.0, lat}, {-121.0, lat}},
		},
	}
}

// twoStopServiceSPA113 is a minimal two-stop UserService on rt: one
// "Interchange" stop at the fixture's point of interest, plus a second stop
// sitting exactly on the alignment (offset 0) at anchorLng, purely to satisfy
// UserService.Validate's two-stop minimum. anchorLng must be given far apart
// between the two services in a test, so the anchors themselves — both
// on-line and therefore both very close to their own route — never
// accidentally merge with each other and confound the assertion on the
// Interchange pair.
func twoStopServiceSPA113(id, routeID string, routeLat, interchangeLat, interchangeLng, anchorLng float64) UserService {
	svc := UserService{
		ID:      id,
		Slug:    id,
		RouteID: routeID,
		OwnerID: "user-1",
		Name:    id,
		Vehicle: VehicleParams{MaxSpeedKMH: 160, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []ServiceStopPoint{
			{Name: "Interchange", Lat: interchangeLat, Lng: interchangeLng},
			{Name: "Anchor", Lat: routeLat, Lng: anchorLng},
		},
	}
	svc.NormalizeStops()
	return svc
}

// compileOne runs a single UserService through SnapToRoute and
// CompilableFromUserService, the same path the write handler and the compiler
// use, so these tests see real persisted-shape OffsetM rather than a
// hand-set stand-in.
func compileOne(t *testing.T, rt Route, svc UserService) CompilableService {
	t.Helper()
	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("SnapToRoute(%s): %v", svc.ID, err)
	}
	compilable, err := CompilableFromUserService(rt, svc)
	if err != nil {
		t.Fatalf("CompilableFromUserService(%s): %v", svc.ID, err)
	}
	return compilable
}

// TestMergeColocatedStops_authored20mApartOnAlignments400mApartMerges is the
// case SPA-113 exists for: two stops authored 20 m apart — as close to "the
// same interchange" as a user can state — on two routes whose alignments run
// parallel about 400 m apart at that point.
//
// A flat 50 m merge radius gets this wrong: each stop is well within
// OffRouteThresholdM (500 m) so both snaps succeed, but they land on their own
// alignments 400 m apart, comfortably outside a flat 50 m and outside the
// 250 m near-miss band too. The interchange would silently vanish.
//
// Widening the radius by both stops' OffsetM (SPA-113's chosen fix) resolves
// it: stop A needed correcting by 210 m, stop B by 170 m, so the effective
// radius is 50+210+170 = 430 m — comfortably covering the 400 m the stops
// actually ended up apart. This is pinned as a merge, not a near miss.
func TestMergeColocatedStops_authored20mApartOnAlignments400mApartMerges(t *testing.T) {
	const routeALat = 37.0
	routeBLat := routeALat + degLatOffsetTest(400) // parallel line ~400 m north

	stopALat := routeALat + degLatOffsetTest(210) // 210 m off route A
	stopBLat := stopALat + degLatOffsetTest(20)   // authored 20 m north of stop A

	rtA := eastWestLine("route-a", routeALat)
	rtB := eastWestLine("route-b", routeBLat)

	svcA := compileOne(t, rtA, twoStopServiceSPA113("svc-a", "route-a", routeALat, stopALat, -121.5, -121.05))
	svcB := compileOne(t, rtB, twoStopServiceSPA113("svc-b", "route-b", routeBLat, stopBLat, -121.5, -121.95))

	interchangeA := svcA.Stops[0]
	interchangeB := svcB.Stops[0]

	// Sanity-check the fixture actually exercises the case described above,
	// rather than accidentally landing somewhere the flat rule already handles.
	if interchangeA.OffsetM < 200 || interchangeA.OffsetM > 220 {
		t.Fatalf("fixture: stop A offset = %v, want ~210 m", interchangeA.OffsetM)
	}
	if interchangeB.OffsetM < 160 || interchangeB.OffsetM > 180 {
		t.Fatalf("fixture: stop B offset = %v, want ~170 m", interchangeB.OffsetM)
	}

	got, report, _ := MergeColocatedStops([]CompilableService{svcA, svcB})
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Fatalf("keys = %q and %q, want one shared key — 20 m of authored intent should survive "+
			"two independent 400-m-apart snaps once the radius accounts for the snapping uncertainty",
			keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
}

// TestMergeColocatedStops_authoredFarApartButSnappedCloseStillMerges pins the
// inverse failure the ticket calls out: two stops authored far enough apart
// that no reasonable person would call them one interchange, which happen to
// snap within the merge radius of each other. The current rule says yes, and
// SPA-113's offset-widened radius does not change that — widening only ever
// makes merging *more* likely, never less, so a pair already within the flat
// 50 m radius merges regardless of how far apart they were authored.
//
// The literal "2 km apart" the ticket describes cannot actually occur under
// today's constants: OffRouteThresholdM (500 m) bounds how far snapping may
// move *either* stop, so by the triangle inequality two stops that end up
// within the merge radius of each other cannot have been authored more than
// roughly 2*500+50 = 1050 m apart pre-snap. This fixture uses that true
// worst case — each stop pinned near its own 500 m limit, on either side of
// two alignments 20 m apart — which is the largest authored separation the
// system can actually produce for a merging pair, and is already far larger
// than any user would author intending two different places to be one stop.
func TestMergeColocatedStops_authoredFarApartButSnappedCloseStillMerges(t *testing.T) {
	const routeALat = 37.0
	routeBLat := routeALat + degLatOffsetTest(20) // alignments 20 m apart

	// Stop A authored ~490 m south of route A; stop B authored ~490 m north of
	// route B — on opposite sides, so the authored points are far apart even
	// though the two alignments themselves are close together.
	stopALat := routeALat - degLatOffsetTest(490)
	stopBLat := routeBLat + degLatOffsetTest(490)

	rawSeparationM := (stopBLat - stopALat) * metersPerDegLatOffsetTest
	if rawSeparationM < 900 {
		t.Fatalf("fixture: authored separation = %v m, want it far apart", rawSeparationM)
	}

	rtA := eastWestLine("route-a", routeALat)
	rtB := eastWestLine("route-b", routeBLat)

	svcA := compileOne(t, rtA, twoStopServiceSPA113("svc-a", "route-a", routeALat, stopALat, -121.5, -121.05))
	svcB := compileOne(t, rtB, twoStopServiceSPA113("svc-b", "route-b", routeBLat, stopBLat, -121.5, -121.95))

	got, report, _ := MergeColocatedStops([]CompilableService{svcA, svcB})
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Errorf("keys = %q and %q, want one shared key — the rule merges on post-snap proximity "+
			"alone, and that has not changed", keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
}
