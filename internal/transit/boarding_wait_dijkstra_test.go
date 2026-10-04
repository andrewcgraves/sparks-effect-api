package transit

import "testing"

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
		budget = 4 * 3600
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

func TestGraphDijkstra_palmdaleInterchangeNoTransferWaitUnderOverrides(t *testing.T) {
	store := mustNewStore(t)
	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr not found")
	}

	services := store.GetServicesByScenario(sc.ID)
	const (
		expressID    = caHSRExpressID
		localID      = caHSRLocalID
		brightlineID = "00000000-0000-4004-8001-000000000003"
	)
	for i := range services {
		switch services[i].ID {
		case expressID, localID:
			services[i].BoardingWait = &BoardingWaitOverride{Policy: BoardingWaitHalfHeadway}
		case brightlineID:
			services[i].BoardingWait = &BoardingWaitOverride{Policy: BoardingWaitFixed, Secs: intPtr(60)}
		}
	}

	g, err := Compile(
		sc,
		store.GetRoutesByScenario(sc.ID),
		store.GetStationsByScenario(sc.ID),
		services,
		append([]VehicleType(nil), store.vehicleTypes...),
		mustTravelTimes(t, store, "ca-hsr"),
		BoardingWaitPolicy{Kind: BoardingWaitFullHeadway},
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	_, originWait, originSvc, ok := graphDijkstra(g, "sf", "palmdale")
	if !ok {
		t.Fatal("sf→palmdale unreachable")
	}
	// Both Phase 1 patterns wait half their headway; the Express runs every
	// 30 min at peak to the Local's 60, so it is boarded at SF.
	if originSvc != expressID {
		t.Fatalf("sf→palmdale boarded %s, want HSR Express", originSvc)
	}
	if originWait != 900 {
		t.Fatalf("sf→palmdale wait: want Express half_headway 900, got %d", originWait)
	}

	_, wait, _, ok := graphDijkstra(g, "sf", "las-vegas")
	if !ok {
		t.Fatal("sf→las-vegas unreachable via Palmdale")
	}
	if wait != originWait {
		t.Errorf("sf→las-vegas wait: want origin-only %d, got %d (transfer wait leaked under mixed overrides)", originWait, wait)
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
