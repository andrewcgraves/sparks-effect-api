package transit

import (
	"reflect"
	"testing"
)

const caHSRPhase1RouteID = "00000000-0000-4002-8001-000000000001"

func TestCAHSRNonstop_onlySFAndLAAt2h40OnPhase1(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not compiled")
	}
	nonstop := caHSRServiceGraph(t, g, caHSRNonstopID)

	got := map[string]int{}
	for _, e := range nonstop.Edges {
		got[e.FromSlug+"→"+e.ToSlug] = e.Seconds
		if e.RouteID != caHSRPhase1RouteID {
			t.Errorf("%s→%s: placed on route %q, want Phase 1", e.FromSlug, e.ToSlug, e.RouteID)
		}
	}
	want := map[string]int{"sf→los-angeles": 9600, "los-angeles→sf": 9600}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HSR Nonstop edges = %v, want %v", got, want)
	}
}

func TestCAHSRNonstop_isTheFastestSFToLA(t *testing.T) {
	store := mustNewStore(t)
	for _, p := range [][2]string{{"sf", "los-angeles"}, {"los-angeles", "sf"}} {
		secs, _, via, ok := store.TravelTimeBetween("ca-hsr", p[0], p[1])
		if !ok {
			t.Fatalf("%s→%s unreachable", p[0], p[1])
		}
		if secs != 9600 || via != caHSRNonstopID {
			t.Errorf("%s→%s: got %d s via %s, want 9600 s via the HSR Nonstop", p[0], p[1], secs, via)
		}
	}
}

// The direct sf→los-angeles segment must not change how the stopping services
// compile: the compiler takes the path with the fewest hops, and no other
// service has consecutive stops sf and los-angeles.
func TestCAHSRNonstop_leavesExpressAndLocalEdgesUnchanged(t *testing.T) {
	store := mustNewStore(t)
	withNonstop := compileCAHSR(t, store, func(Service) bool { return true })

	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}
	tt := mustTravelTimes(t, store, "ca-hsr")
	var segs []SegmentTime
	for _, seg := range tt.Segments {
		if seg.FromSlug == "sf" && seg.ToSlug == "los-angeles" {
			continue
		}
		segs = append(segs, seg)
	}
	if len(segs) != len(tt.Segments)-1 {
		t.Fatalf("want exactly one sf→los-angeles segment to drop, dropped %d", len(tt.Segments)-len(segs))
	}
	tt.Segments = segs
	var services []Service
	for _, svc := range store.GetServicesByScenario(sc.ID) {
		if svc.ID != caHSRNonstopID {
			services = append(services, svc)
		}
	}
	before, err := Compile(
		sc,
		store.GetRoutesByScenario(sc.ID),
		store.GetStationsByScenario(sc.ID),
		services,
		append([]VehicleType(nil), store.vehicleTypes...),
		tt,
		BoardingWaitPolicy{Kind: BoardingWaitNone},
	)
	if err != nil {
		t.Fatalf("Compile without the nonstop: %v", err)
	}

	for _, id := range []string{caHSRExpressID, caHSRLocalID} {
		was := caHSRServiceGraph(t, before, id)
		now := caHSRServiceGraph(t, withNonstop, id)
		if !reflect.DeepEqual(was, now) {
			t.Errorf("service %s compiles differently with the HSR Nonstop seeded", id)
		}
	}
}
