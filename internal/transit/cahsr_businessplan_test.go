package transit

import (
	"slices"
	"testing"
)

// Table 3-4 of the 2026 Business Plan, all-stop, minutes. Rows are origins.
// Phase 1 stations plus Merced. Victor Valley and Las Vegas are omitted:
// Brightline connection time is out of scope.
var businessPlan2026AllStopMin = map[string]map[string]int{
	"sf": {
		"millbrae": 15, "san-jose": 56, "gilroy": 79, "merced": 136, "madera": 113,
		"fresno": 124, "kings-tulare": 140, "bakersfield": 170, "palmdale": 203,
		"burbank-airport": 251, "los-angeles": 269, "anaheim": 306,
	},
	"millbrae": {
		"sf": 15, "san-jose": 39, "gilroy": 62, "merced": 119, "madera": 96,
		"fresno": 107, "kings-tulare": 123, "bakersfield": 154, "palmdale": 187,
		"burbank-airport": 234, "los-angeles": 253, "anaheim": 289,
	},
	"san-jose": {
		"sf": 56, "millbrae": 39, "gilroy": 21, "merced": 78, "madera": 55,
		"fresno": 65, "kings-tulare": 82, "bakersfield": 112, "palmdale": 145,
		"burbank-airport": 193, "los-angeles": 211, "anaheim": 248,
	},
	"gilroy": {
		"sf": 79, "millbrae": 62, "san-jose": 21, "merced": 56, "madera": 33,
		"fresno": 44, "kings-tulare": 59, "bakersfield": 86, "palmdale": 123,
		"burbank-airport": 170, "los-angeles": 189, "anaheim": 225,
	},
	"madera": {
		"sf": 117, "millbrae": 100, "san-jose": 59, "gilroy": 32, "merced": 20,
		"fresno": 9, "kings-tulare": 25, "bakersfield": 51, "palmdale": 88,
		"burbank-airport": 136, "los-angeles": 154, "anaheim": 191,
	},
	"fresno": {
		"sf": 128, "millbrae": 111, "san-jose": 70, "gilroy": 43, "merced": 38,
		"madera": 9, "kings-tulare": 13, "bakersfield": 40, "palmdale": 78,
		"burbank-airport": 125, "los-angeles": 144, "anaheim": 180,
	},
	"kings-tulare": {
		"sf": 143, "millbrae": 126, "san-jose": 85, "gilroy": 59, "merced": 54,
		"madera": 25, "fresno": 13, "bakersfield": 25, "palmdale": 61,
		"burbank-airport": 109, "los-angeles": 127, "anaheim": 164,
	},
	"bakersfield": {
		"sf": 173, "millbrae": 156, "san-jose": 115, "gilroy": 85, "merced": 80,
		"madera": 51, "fresno": 40, "kings-tulare": 25, "palmdale": 31,
		"burbank-airport": 79, "los-angeles": 97, "anaheim": 134,
	},
	"palmdale": {
		"sf": 202, "millbrae": 186, "san-jose": 144, "gilroy": 122, "merced": 113,
		"madera": 84, "fresno": 72, "kings-tulare": 58, "bakersfield": 28,
		"burbank-airport": 46, "los-angeles": 64, "anaheim": 101,
	},
	"burbank-airport": {
		"sf": 251, "millbrae": 234, "san-jose": 193, "gilroy": 170, "merced": 161,
		"madera": 132, "fresno": 121, "kings-tulare": 106, "bakersfield": 76,
		"palmdale": 46, "los-angeles": 17, "anaheim": 53,
	},
	"los-angeles": {
		"sf": 269, "millbrae": 253, "san-jose": 212, "gilroy": 189, "merced": 180,
		"madera": 151, "fresno": 139, "kings-tulare": 125, "bakersfield": 95,
		"palmdale": 65, "burbank-airport": 17, "anaheim": 32,
	},
	"anaheim": {
		"sf": 306, "millbrae": 289, "san-jose": 248, "gilroy": 225, "merced": 216,
		"madera": 187, "fresno": 176, "kings-tulare": 161, "bakersfield": 131,
		"palmdale": 102, "burbank-airport": 53, "los-angeles": 32,
	},
	"merced": {
		"sf": 135, "millbrae": 118, "san-jose": 77, "gilroy": 54, "madera": 20,
		"fresno": 38, "kings-tulare": 54, "bakersfield": 80, "palmdale": 117,
		"burbank-airport": 165, "los-angeles": 183, "anaheim": 220,
	},
}

func TestCompiledCAHSRMatches2026BusinessPlan(t *testing.T) {
	store := mustNewStore(t)

	if len(businessPlan2026AllStopMin) != 13 {
		t.Fatalf("matrix origins: want 13, got %d", len(businessPlan2026AllStopMin))
	}
	for from, row := range businessPlan2026AllStopMin {
		if len(row) != 12 {
			t.Fatalf("%s: want 12 destinations, got %d", from, len(row))
		}
	}

	// The matrix is all-stop, so it is held against the graph without the HSR
	// Express, which would otherwise win every pair it serves.
	allStop := compileCAHSR(t, store, withoutExpress)
	for from, row := range businessPlan2026AllStopMin {
		for to, wantMin := range row {
			got, _, _, ok := graphDijkstra(allStop, from, to)
			if !ok {
				t.Errorf("%s→%s: got no path, matrix %d min", from, to, wantMin)
				continue
			}
			wantSec := wantMin * 60
			delta := got - wantSec
			if delta < 0 {
				delta = -delta
			}
			limit := 6 * 60
			if (from == "san-jose" && to == "fresno") || (from == "fresno" && to == "san-jose") {
				limit = 3 * 60
			}
			if delta > limit {
				t.Errorf("%s→%s: got %.1f min, matrix %d min, delta %.1f min",
					from, to, float64(got)/60, wantMin, float64(delta)/60)
			}
		}
	}

	sc, ok := store.GetScenarioBySlug("ca-hsr")
	if !ok {
		t.Fatal("ca-hsr scenario not found")
	}
	slugByStation := map[string]string{}
	for _, st := range store.GetStationsByScenario(sc.ID) {
		slugByStation[st.ID] = st.Slug
	}
	stopSlugs := func(svc Service) []string {
		t.Helper()
		stops := append([]ServiceStop(nil), svc.Stops...)
		slices.SortFunc(stops, func(a, b ServiceStop) int { return a.Sequence - b.Sequence })
		out := make([]string, len(stops))
		for i, stop := range stops {
			slug, found := slugByStation[stop.StationID]
			if !found {
				t.Fatalf("service %q stop %q has no station", svc.Name, stop.StationID)
			}
			out[i] = slug
		}
		return out
	}

	var local, express, shuttle *Service
	for i := range store.services {
		svc := &store.services[i]
		if svc.ScenarioID != sc.ID {
			continue
		}
		switch svc.Name {
		case "HSR Local":
			local = svc
		case "HSR Express":
			express = svc
		case "Merced Shuttle":
			shuttle = svc
		}
	}
	if local == nil || express == nil || shuttle == nil {
		t.Fatal("expected HSR Local, HSR Express, and Merced Shuttle in the seed")
	}
	if slices.Contains(stopSlugs(*local), "merced") {
		t.Errorf("HSR Local stops = %v, want no merced", stopSlugs(*local))
	}
	if slices.Contains(stopSlugs(*express), "merced") {
		t.Errorf("HSR Express stops = %v, want no merced", stopSlugs(*express))
	}
	if got := stopSlugs(*shuttle); !slices.Equal(got, []string{"merced", "madera"}) {
		t.Errorf("Merced Shuttle stops = %v, want [merced madera]", got)
	}

	const phase1RouteID = "00000000-0000-4002-8001-000000000001"
	tt, ok := store.GetTravelTimes("ca-hsr")
	if !ok {
		t.Fatal("travel times not found for ca-hsr")
	}
	for _, seg := range tt.Segments {
		if seg.RouteID != phase1RouteID {
			continue
		}
		if seg.FromSlug == "merced" || seg.ToSlug == "merced" {
			t.Errorf("Phase 1 segment %s→%s calls at merced", seg.FromSlug, seg.ToSlug)
		}
	}
}
