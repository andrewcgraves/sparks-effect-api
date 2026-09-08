package transit

import (
	"testing"
)

const metersPerDegLatOffsetTest = 111194.926644

func degLatOffsetTest(m float64) float64 { return m / metersPerDegLatOffsetTest }

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

	got, report, _ := MergeColocatedStops([]CompilableService{svcA, svcB}, nil)
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

	got, report, _ := MergeColocatedStops([]CompilableService{svcA, svcB}, nil)
	keys := keysOf(got)
	if keys[0][0] != keys[1][0] {
		t.Errorf("keys = %q and %q, want one shared key — the rule merges on post-snap proximity "+
			"alone, and that has not changed", keys[0][0], keys[1][0])
	}
	if len(report.Clusters) != 1 {
		t.Errorf("clusters = %+v, want exactly one realised merge", report.Clusters)
	}
}
