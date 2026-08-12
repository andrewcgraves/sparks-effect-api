package transit

import "testing"

// Removing boarding wait can only expand reach: every station reachable under
// half_headway from sf within a budget remains reachable under none, and the
// none set is a (possibly strict) superset.
func TestBoardingWaitNone_reachableIsSupersetOfHalfHeadway(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}

	compile := func(policy BoardingWaitPolicy) *TransitGraph {
		t.Helper()
		g, err := Compile(
			sc,
			store.GetRoutesByScenario(sc.ID),
			store.GetStationsByScenario(sc.ID),
			store.GetServicesByScenario(sc.ID),
			append([]VehicleType(nil), store.vehicleTypes...),
			mustTravelTimes(t, store, "ca-hsr"),
			policy,
		)
		if err != nil {
			t.Fatalf("Compile(%s): %v", policy.Kind, err)
		}
		return g
	}

	withWait := compile(BoardingWaitPolicy{Kind: BoardingWaitHalfHeadway})
	noWait := compile(DefaultBoardingWaitPolicy())

	const (
		origin = "sf"
		budget = 4 * 3600 // 4 hours of in-vehicle + boarding budget
	)
	reachable := func(g *TransitGraph) map[string]bool {
		out := map[string]bool{origin: true}
		for _, st := range store.GetStationsByScenario(sc.ID) {
			if st.Slug == origin {
				continue
			}
			secs, wait, _, ok := graphDijkstra(g, origin, st.Slug)
			if ok && secs+wait <= budget {
				out[st.Slug] = true
			}
		}
		return out
	}

	half := reachable(withWait)
	none := reachable(noWait)
	for slug := range half {
		if !none[slug] {
			t.Errorf("station %q reachable under half_headway but not under none", slug)
		}
	}
	if len(none) < len(half) {
		t.Errorf("none reach set size %d < half_headway %d", len(none), len(half))
	}
}

// A Palmdale interchange between HSR Local and Brightline West must charge
// boarding wait only at the origin — never again at the transfer, under every
// policy value (SPA-236: no transfer cost).
func TestGraphDijkstra_palmdaleInterchangeChargesNoTransferWait(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}

	policies := []BoardingWaitPolicy{
		{Kind: BoardingWaitNone},
		{Kind: BoardingWaitHalfHeadway},
		{Kind: BoardingWaitFullHeadway},
		{Kind: BoardingWaitFixed, FixedSecs: 900},
	}
	for _, policy := range policies {
		t.Run(string(policy.Kind), func(t *testing.T) {
			g, err := Compile(
				sc,
				store.GetRoutesByScenario(sc.ID),
				store.GetStationsByScenario(sc.ID),
				store.GetServicesByScenario(sc.ID),
				append([]VehicleType(nil), store.vehicleTypes...),
				mustTravelTimes(t, store, "ca-hsr"),
				policy,
			)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			// Origin boarding wait alone.
			_, originWait, originSvc, ok := graphDijkstra(g, "sf", "palmdale")
			if !ok {
				t.Fatal("sf→palmdale unreachable")
			}
			wantOriginWait, err := policy.WaitSecs(serviceWindows(t, store, sc.ID, originSvc))
			if err != nil {
				t.Fatalf("WaitSecs: %v", err)
			}
			if originWait != wantOriginWait {
				t.Fatalf("sf→palmdale wait: want %d, got %d", wantOriginWait, originWait)
			}

			// Through the Palmdale interchange to Las Vegas: wait must still be
			// exactly the origin boarding wait — zero additional at transfer.
			secs, wait, _, ok := graphDijkstra(g, "sf", "las-vegas")
			if !ok {
				t.Fatal("sf→las-vegas unreachable via Palmdale")
			}
			if wait != originWait {
				t.Errorf("sf→las-vegas wait: want origin-only %d, got %d (transfer wait leaked)", originWait, wait)
			}
			if secs <= 0 {
				t.Errorf("sf→las-vegas vehicle secs: want > 0, got %d", secs)
			}
		})
	}
}

func serviceWindows(t *testing.T, store *Store, scenarioID, serviceID string) []FrequencyWindow {
	t.Helper()
	for _, svc := range store.GetServicesByScenario(scenarioID) {
		if svc.ID == serviceID {
			return svc.FrequencyWindows
		}
	}
	t.Fatalf("service %s not found", serviceID)
	return nil
}
