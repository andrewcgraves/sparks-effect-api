package transit_test

import (
	"errors"
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// snapTestRoute is a due-east line at latitude 37, one degree of longitude
// long. A straight line on a parallel makes the arithmetic checkable by hand:
// in the local planar frame a stop displaced 0.001 degrees of latitude sits
// ~111 m off the alignment, and 0.01 degrees ~1112 m — one comfortably inside
// the 500 m threshold and one comfortably outside it.
func snapTestRoute() transit.Route {
	return transit.Route{
		ID:   "route-1",
		Slug: "ca-hsr-central-valley",
		Name: "Central Valley",
		Geometry: transit.GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{-122.0, 37.0}, {-121.0, 37.0}},
		},
	}
}

// offsetForDegLat is the planar distance, in metres, that a displacement of
// degLat degrees of latitude produces — the same equirectangular frame
// internal/physics snaps in.
func offsetForDegLat(degLat float64) float64 {
	return 6371000.0 * degLat * math.Pi / 180
}

// serviceOnSnapRoute is a service on snapTestRoute stopping at the given
// longitudes, each displaced north of the alignment by degLat degrees. Stops
// are named A, B, C… in the order given, which is the authored order.
func serviceOnSnapRoute(degLat float64, lngs ...float64) transit.UserService {
	svc := transit.UserService{
		RouteID: "route-1",
		OwnerID: "user-1",
		Name:    "Central Valley Express",
		Vehicle: transit.VehicleParams{MaxSpeedKMH: 320, AccelerationMS2: 1.1, DecelerationMS2: 1.3, DwellS: 45},
	}
	for i, lng := range lngs {
		svc.Stops = append(svc.Stops, transit.ServiceStopPoint{
			Name: string(rune('A' + i)),
			Lat:  37.0 + degLat,
			Lng:  lng,
		})
	}
	svc.NormalizeStops()
	return svc
}

func TestSnapToRouteRewritesStopsOntoTheAlignment(t *testing.T) {
	svc := serviceOnSnapRoute(0.001, -121.8, -121.4)

	if err := svc.SnapToRoute(snapTestRoute()); err != nil {
		t.Fatalf("SnapToRoute: %v", err)
	}

	wantOffset := offsetForDegLat(0.001)
	for _, stop := range svc.Stops {
		if math.Abs(stop.Lat-37.0) > 1e-9 {
			t.Errorf("stop %q lat = %v, want it pulled onto the line at 37.0", stop.Name, stop.Lat)
		}
		if math.Abs(stop.OffsetM-wantOffset) > 0.5 {
			t.Errorf("stop %q offset = %v m, want ~%v m", stop.Name, stop.OffsetM, wantOffset)
		}
	}

	// Longitude is untouched by a perpendicular snap onto a parallel, so
	// chainage is the along-line distance from the line's western end.
	if svc.Stops[0].ChainageM <= 0 {
		t.Errorf("first stop chainage = %v, want a positive distance along the line", svc.Stops[0].ChainageM)
	}
	if svc.Stops[0].ChainageM >= svc.Stops[1].ChainageM {
		t.Errorf("chainage did not increase along the route: %v then %v",
			svc.Stops[0].ChainageM, svc.Stops[1].ChainageM)
	}
}

func TestSnapToRoutePreservesAuthoredOrder(t *testing.T) {
	svc := serviceOnSnapRoute(0.001, -121.8, -121.4)
	if err := svc.SnapToRoute(snapTestRoute()); err != nil {
		t.Fatalf("SnapToRoute: %v", err)
	}
	if svc.Stops[0].Name != "A" || svc.Stops[1].Name != "B" {
		t.Fatalf("stops were reordered: got %q, %q", svc.Stops[0].Name, svc.Stops[1].Name)
	}
	for i, stop := range svc.Stops {
		if stop.Seq != i {
			t.Errorf("stop %q seq = %d, want %d", stop.Name, stop.Seq, i)
		}
	}
}

func TestSnapToRouteRejectsAnOffRouteStop(t *testing.T) {
	svc := serviceOnSnapRoute(0, -121.8, -121.4)
	svc.Stops[1].Lat = 37.01 // ~1112 m north of the alignment

	err := svc.SnapToRoute(snapTestRoute())
	if err == nil {
		t.Fatal("SnapToRoute accepted a stop 1.1 km off the alignment")
	}
	if errors.Is(err, transit.ErrRouteGeometry) {
		t.Fatalf("off-route stop reported as a geometry fault: %v", err)
	}
	for _, want := range []string{`"B"`, "ca-hsr-central-valley", "1.1 km"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	// A rejected service must not be left half-rewritten.
	if svc.Stops[0].ChainageM != 0 {
		t.Errorf("stops were mutated by a rejected snap: %+v", svc.Stops[0])
	}
}

func TestSnapToRouteAcceptsAStopExactlyOnTheThreshold(t *testing.T) {
	// The preview flags with a strict comparison (offset > threshold), so a
	// stop on the boundary must save rather than be refused for what the
	// preview called acceptable.
	degLat := transit.OffRouteThresholdM / offsetForDegLat(1)
	svc := serviceOnSnapRoute(degLat, -121.8, -121.4)

	if err := svc.SnapToRoute(snapTestRoute()); err != nil {
		t.Fatalf("SnapToRoute rejected a stop at exactly the threshold: %v", err)
	}
}

func TestSnapToRouteRejectsChainageOrderDisagreement(t *testing.T) {
	// A and B run east; C doubles back between them, so the compiler would
	// build A→C→B while the service claims A→B→C.
	svc := serviceOnSnapRoute(0, -121.8, -121.4, -121.6)

	err := svc.SnapToRoute(snapTestRoute())
	if err == nil {
		t.Fatal("SnapToRoute accepted stops whose chainage zig-zags along the line")
	}
	if errors.Is(err, transit.ErrRouteGeometry) {
		t.Fatalf("order disagreement reported as a geometry fault: %v", err)
	}
	for _, want := range []string{`"B"`, `"C"`, "seq 1", "seq 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	if svc.Stops[0].ChainageM != 0 {
		t.Errorf("stops were mutated by a rejected snap: %+v", svc.Stops[0])
	}
}

func TestSnapToRouteAcceptsAServiceRunningAgainstTheLineDirection(t *testing.T) {
	// Authored east-to-west, against the geometry's own direction. Chainage
	// descends the whole way, which is a consistent pattern and not a
	// disagreement: the adjacent pairs the compiler builds are exactly the
	// authored ones. Rejecting this would make westbound services unauthorable
	// on a route whose geometry happens to be drawn eastward.
	svc := serviceOnSnapRoute(0, -121.2, -121.4, -121.8)

	if err := svc.SnapToRoute(snapTestRoute()); err != nil {
		t.Fatalf("SnapToRoute rejected a consistently westbound service: %v", err)
	}
	if svc.Stops[0].ChainageM <= svc.Stops[2].ChainageM {
		t.Fatalf("expected descending chainage, got %v then %v",
			svc.Stops[0].ChainageM, svc.Stops[2].ChainageM)
	}
}

func TestSnapToRouteIsIdempotent(t *testing.T) {
	rt := snapTestRoute()
	svc := serviceOnSnapRoute(0.001, -121.8, -121.4)
	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("first SnapToRoute: %v", err)
	}
	first := append([]transit.ServiceStopPoint(nil), svc.Stops...)

	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("second SnapToRoute: %v", err)
	}

	for i, stop := range svc.Stops {
		if math.Abs(stop.Lat-first[i].Lat) > 1e-12 || math.Abs(stop.Lng-first[i].Lng) > 1e-12 {
			t.Errorf("stop %q drifted on re-snap: %v,%v then %v,%v",
				stop.Name, first[i].Lat, first[i].Lng, stop.Lat, stop.Lng)
		}
		if math.Abs(stop.ChainageM-first[i].ChainageM) > 1e-6 {
			t.Errorf("stop %q chainage drifted on re-snap: %v then %v",
				stop.Name, first[i].ChainageM, stop.ChainageM)
		}
		// An already-snapped point sits on the line, so it has nowhere to move.
		if stop.OffsetM > 1e-6 {
			t.Errorf("stop %q re-snapped with offset %v m, want ~0", stop.Name, stop.OffsetM)
		}
	}
}

func TestSnapToRouteEditingOneStopDoesNotDriftTheOthers(t *testing.T) {
	rt := snapTestRoute()
	svc := serviceOnSnapRoute(0.001, -121.8, -121.4)
	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("first SnapToRoute: %v", err)
	}
	untouched := svc.Stops[0]

	// The user drags the second stop; the first is resubmitted as stored.
	svc.Stops[1].Lat = 37.002
	svc.Stops[1].Lng = -121.3
	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("second SnapToRoute: %v", err)
	}

	if svc.Stops[0].Lat != untouched.Lat || svc.Stops[0].Lng != untouched.Lng {
		t.Errorf("editing one stop moved another: %v,%v became %v,%v",
			untouched.Lat, untouched.Lng, svc.Stops[0].Lat, svc.Stops[0].Lng)
	}
	if svc.Stops[0].ChainageM != untouched.ChainageM {
		t.Errorf("editing one stop changed another's chainage: %v became %v",
			untouched.ChainageM, svc.Stops[0].ChainageM)
	}
}

func TestSnapToRouteResetsOffsetWhenAnAlreadySnappedStopIsResubmitted(t *testing.T) {
	// Pinned because it is a real consequence, not an accident: a stop that
	// comes back at the coordinate the last write returned has, by definition,
	// moved zero metres, so its recorded offset drops to 0 on every re-save.
	//
	// Position and chainage are stable, which is what makes an edit safe. But
	// offset is not a durable property of the stop — it is a property of one
	// write. SPA-113's recommended merge rule (widen the radius by each stop's
	// snapping uncertainty) would therefore see that uncertainty vanish the
	// second time a user saves an unrelated change. Preserving it would mean
	// carrying the authored position forward, which is exactly the decision
	// SPA-108 made against and SPA-113 owns reopening.
	rt := snapTestRoute()
	svc := serviceOnSnapRoute(0.001, -121.8, -121.4)
	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("first SnapToRoute: %v", err)
	}
	if svc.Stops[0].OffsetM <= 0 {
		t.Fatalf("first snap recorded no offset for a stop placed off the line: %v", svc.Stops[0].OffsetM)
	}

	if err := svc.SnapToRoute(rt); err != nil {
		t.Fatalf("second SnapToRoute: %v", err)
	}
	if svc.Stops[0].OffsetM != 0 {
		t.Errorf("re-snap offset = %v, want 0 — a stop on the line has nowhere to move", svc.Stops[0].OffsetM)
	}
}

func TestSnapToRouteReportsUnusableGeometry(t *testing.T) {
	rt := snapTestRoute()
	rt.Geometry.Coordinates = [][]float64{{-122.0, 37.0}}

	svc := serviceOnSnapRoute(0, -121.8, -121.4)
	if err := svc.SnapToRoute(rt); !errors.Is(err, transit.ErrRouteGeometry) {
		t.Fatalf("SnapToRoute error = %v, want it to wrap ErrRouteGeometry", err)
	}
}

// TestAWestboundServiceCompilesToTheSameGraphAsItsEastboundTwin is the proof
// behind the monotonicity rule in FirstChainageOrderFault, which accepts a
// service whose chainage descends the whole way even though physics.ProjectStops
// sorts it back into ascending order before building spans.
//
// The claim is that the sort cannot change the graph for a monotonic sequence:
// reversing a list preserves every adjacent pair, and compiled edges are emitted
// in both directions carrying the dwell of the end they arrive at. If that ever
// stops being true — a directional edge weight, an order-sensitive consumer —
// this test fails and the rule has to tighten to strict ascent.
func TestAWestboundServiceCompilesToTheSameGraphAsItsEastboundTwin(t *testing.T) {
	rt := transit.Route{
		ID: "route-1", Slug: "line", Name: "Line",
		Geometry: transit.GeoLineString{
			Type:        "LineString",
			Coordinates: [][]float64{{-122.0, 37.0}, {-121.0, 37.0}},
		},
	}

	// The same three physical stops, authored in opposite orders.
	eastbound := serviceOnSnapRoute(0, -121.8, -121.6, -121.4)
	eastbound.ID = "svc-1"
	eastbound.Stops[0].Name, eastbound.Stops[1].Name, eastbound.Stops[2].Name = "A", "B", "C"

	westbound := serviceOnSnapRoute(0, -121.4, -121.6, -121.8)
	westbound.ID = "svc-1"
	westbound.Stops[0].Name, westbound.Stops[1].Name, westbound.Stops[2].Name = "C", "B", "A"

	for _, svc := range []*transit.UserService{&eastbound, &westbound} {
		if err := svc.SnapToRoute(rt); err != nil {
			t.Fatalf("SnapToRoute(%s): %v", svc.Stops[0].Name, err)
		}
	}

	if eastbound.Stops[0].ChainageM >= eastbound.Stops[2].ChainageM {
		t.Fatal("eastbound fixture does not ascend along the line")
	}
	if westbound.Stops[0].ChainageM <= westbound.Stops[2].ChainageM {
		t.Fatal("westbound fixture does not descend along the line")
	}

	if got, want := compiledEdges(t, rt, westbound), compiledEdges(t, rt, eastbound); !maps.Equal(got, want) {
		t.Errorf("westbound compiled to a different graph:\n west = %v\n east = %v", got, want)
	}
}

// compiledEdges compiles svc and returns its edges as a set keyed "from->to",
// so two graphs compare regardless of the order spans happened to be built in.
func compiledEdges(t *testing.T, rt transit.Route, svc transit.UserService) map[string]int {
	t.Helper()
	compilable, err := transit.CompilableFromUserService(rt, svc)
	if err != nil {
		t.Fatalf("CompilableFromUserService: %v", err)
	}
	sg, err := transit.CompileServicePhysics(compilable)
	if err != nil {
		t.Fatalf("CompileServicePhysics: %v", err)
	}
	edges := make(map[string]int, len(sg.Edges))
	for _, e := range sg.Edges {
		edges[e.FromSlug+"->"+e.ToSlug] = e.Seconds
	}
	return edges
}
