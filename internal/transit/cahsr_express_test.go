package transit

import (
	"slices"
	"testing"
)

const (
	caHSRExpressID = "00000000-0000-4004-8001-000000000001"
	caHSRLocalID   = "00000000-0000-4004-8001-000000000002"
)

// compileCAHSR compiles the seeded ca-hsr scenario from the active services
// keep admits, under no boarding wait.
func compileCAHSR(t *testing.T, store *Store, keep func(Service) bool) *TransitGraph {
	t.Helper()
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}
	var services []Service
	for _, svc := range store.GetServicesByScenario(sc.ID) {
		if keep(svc) {
			services = append(services, svc)
		}
	}
	g, err := Compile(
		sc,
		store.GetRoutesByScenario(sc.ID),
		store.GetStationsByScenario(sc.ID),
		services,
		append([]VehicleType(nil), store.vehicleTypes...),
		mustTravelTimes(t, store, "ca-hsr"),
		BoardingWaitPolicy{Kind: BoardingWaitNone},
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return g
}

func withoutExpress(svc Service) bool { return svc.ID != caHSRExpressID }

func caHSRServiceGraph(t *testing.T, g *TransitGraph, serviceID string) ServiceGraph {
	t.Helper()
	for _, sg := range g.Services {
		if sg.ServiceID == serviceID {
			return sg
		}
	}
	t.Fatalf("service %s not in compiled graph", serviceID)
	return ServiceGraph{}
}

func TestCAHSRExpress_skipsSmallCentralValleyStops(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not compiled")
	}
	express := caHSRServiceGraph(t, g, caHSRExpressID)

	touched := map[string]bool{}
	for _, e := range express.Edges {
		touched[e.FromSlug] = true
		touched[e.ToSlug] = true
	}
	var stops []string
	for slug := range touched {
		stops = append(stops, slug)
	}
	slices.Sort(stops)
	want := []string{
		"anaheim", "bakersfield", "burbank-airport", "fresno", "gilroy",
		"los-angeles", "millbrae", "palmdale", "san-jose", "sf",
	}
	if !slices.Equal(stops, want) {
		t.Errorf("HSR Express calls at %v, want %v", stops, want)
	}
}

func TestCAHSRExpress_neverSlowerThanLocal(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not compiled")
	}
	express := caHSRServiceGraph(t, g, caHSRExpressID)
	local := caHSRServiceGraph(t, g, caHSRLocalID)

	calls := map[string]bool{}
	for _, e := range express.Edges {
		calls[e.FromSlug] = true
	}
	saved := 0
	for from := range calls {
		for to := range calls {
			if from == to {
				continue
			}
			ex, exOK := servicePathSecs(express, from, to)
			lo, loOK := servicePathSecs(local, from, to)
			if !exOK || !loOK {
				t.Errorf("%s→%s: express reachable %v, local reachable %v", from, to, exOK, loOK)
				continue
			}
			if ex > lo {
				t.Errorf("%s→%s: express %d s slower than local %d s", from, to, ex, lo)
			}
			if from == "sf" && to == "anaheim" {
				saved = lo - ex
			}
		}
	}
	// Two skipped calls, Madera and Kings/Tulare, at the vehicle's 90 s level
	// dwell. run_seconds carry the all-stop braking, so dwell is all it saves.
	if saved != 2*90 {
		t.Errorf("sf→anaheim: express saves %d s over local, want 180", saved)
	}
}

func TestCAHSRExpress_sharedStationIsOneNode(t *testing.T) {
	store := mustNewStore(t)
	g, ok := store.Graph("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr graph not compiled")
	}
	count := map[string]int{}
	for _, n := range g.Nodes {
		count[n.Slug]++
	}
	for slug, n := range count {
		if n != 1 {
			t.Errorf("node %q appears %d times, want once", slug, n)
		}
	}
	if count["fresno"] != 1 {
		t.Errorf("fresno, served by Local and Express, want one node, got %d", count["fresno"])
	}

	// The search keeps the fastest arrival per station: from SF, Fresno comes
	// in at the Express time, Madera (which the Express skips) at the Local's.
	// Times, not boarded service: SF→Millbrae is the same hop on both
	// patterns, so which one a path boards at SF is a tie.
	express := caHSRServiceGraph(t, g, caHSRExpressID)
	local := caHSRServiceGraph(t, g, caHSRLocalID)
	for _, tc := range []struct {
		to   string
		want ServiceGraph
		name string
	}{
		{"fresno", express, "HSR Express"},
		{"madera", local, "HSR Local"},
	} {
		got, _, _, ok := graphDijkstra(g, "sf", tc.to)
		if !ok {
			t.Fatalf("sf→%s unreachable", tc.to)
		}
		want, _ := servicePathSecs(tc.want, "sf", tc.to)
		if got != want {
			t.Errorf("sf→%s: got %d s, want the %s time %d s", tc.to, got, tc.name, want)
		}
	}
	viaLocal, _ := servicePathSecs(local, "sf", "fresno")
	if viaExpress, _ := servicePathSecs(express, "sf", "fresno"); viaExpress >= viaLocal {
		t.Errorf("sf→fresno: express %d s, want faster than local %d s", viaExpress, viaLocal)
	}
}
