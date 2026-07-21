package transit

import (
	"fmt"
	"sort"
)

// CompilableService is the narrow input the physics compiler actually needs:
// an alignment to project onto, kinematic limits, an ordered list of stops that
// each have an identity and a position, and the headways that set wait time.
//
// It exists so there is one physics compiler rather than one per domain model.
// The project grew two parallel service models — the seeded Service, which
// references shared Station and VehicleType rows, and the user-authored
// UserService, which embeds its stops and vehicle params inline — and neither
// of those shapes is what the compiler needs. Both are projected onto this by
// the adapters below, so CompileServicePhysics knows about neither.
//
// Stops are in stopping order; the adapters do the ordering, since Sequence
// (seeded) and Seq (authored) are different fields. Their slugs must be
// distinct — they are the graph's node keys.
type CompilableService struct {
	ID      string
	Route   Route
	Vehicle Kinematics
	Stops   []CompilableStop
	Windows []FrequencyWindow
}

// Kinematics is the whole of what the compiler asks of a vehicle: how fast it
// may go and how hard it may accelerate and brake.
//
// Deliberately not VehicleParams, which also carries a DwellS the compiler
// would ignore — dwell is settled per stop by the adapters (see
// CompilableStop.DwellS), so a vehicle-level dwell here would be a field that
// looks load-bearing and is not.
type Kinematics struct {
	MaxSpeedKMH     float64
	AccelerationMS2 float64
	DecelerationMS2 float64
}

// CompilableStop is one stop with everything the compiler needs already
// decided: an identity to key edges on, a position to project onto the
// alignment, and a dwell to add.
//
// DwellS is resolved, not a hint: the two models disagree about where dwell
// comes from — seeded compares a Station's platform height to the vehicle's
// floor height to choose between VehicleType.DwellLevelS and DwellStepS, while
// an authored service has one flat VehicleParams.DwellS — so each adapter
// settles it and the compiler just adds the number.
type CompilableStop struct {
	Slug   string
	Lat    float64
	Lng    float64
	DwellS int
}

// CompilableFromService adapts the seeded model. It resolves each stop's
// station reference to a position and its dwell to a number, so the behaviour
// that used to live inside CompileServicePhysics is preserved exactly — just
// moved to the boundary where Station and VehicleType are still in scope.
//
// Active is deliberately not consulted: whether an inactive service belongs in
// a graph is scenario-assembly semantics, and CompileScenario already skips it.
func CompilableFromService(route Route, stations []Station, svc Service, vt VehicleType) (CompilableService, error) {
	stationsByID := make(map[string]Station, len(stations))
	for _, st := range stations {
		stationsByID[st.ID] = st
	}

	stops := append([]ServiceStop(nil), svc.Stops...)
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].Sequence < stops[j].Sequence })

	compiled := make([]CompilableStop, len(stops))
	for i, stop := range stops {
		st, ok := stationsByID[stop.StationID]
		if !ok {
			return CompilableService{}, fmt.Errorf("compile: service %q references unknown station id %q", svc.ID, stop.StationID)
		}
		if len(st.Location.Coordinates) < 2 {
			return CompilableService{}, fmt.Errorf("compile: service %q: station %q has no location", svc.ID, st.Slug)
		}
		compiled[i] = CompilableStop{
			Slug:   st.Slug,
			Lng:    st.Location.Coordinates[0],
			Lat:    st.Location.Coordinates[1],
			DwellS: resolveDwell(stop, st, vt),
		}
	}

	return CompilableService{
		ID:    svc.ID,
		Route: route,
		Vehicle: Kinematics{
			MaxSpeedKMH:     vt.MaxSpeedKMH,
			AccelerationMS2: vt.AccelerationMS2,
			DecelerationMS2: vt.DecelerationMS2,
		},
		Stops:   compiled,
		Windows: svc.FrequencyWindows,
	}, nil
}

// CompilableFromUserService adapts the user-authored model. An embedded stop
// already carries its own position and the vehicle params are already inline,
// so the only real work is minting a stop identity — graph edges are keyed by
// slug and a ServiceStopPoint has none.
//
// route must be the one svc references. Projecting stops onto an alignment they
// were never authored against would produce a plausible-looking wrong graph
// rather than an error, so the mismatch is rejected here.
//
// Namespacing slugs by the owning service (see StopSlugs) is a trade, not a
// free win. It stops two unrelated services that each have a "Downtown" from
// fusing into one node and inventing a transfer between places 50km apart — but
// it equally means two services stopping at the *same* place cannot interchange
// there, because graphDijkstra pools edges by slug and namespaced stops are
// separate nodes. Until user services can share a stop identity, the services in
// a user scenario do not connect to each other. SPA-83 consumes these graphs and
// is where that surfaces.
//
// No Station row is created: stops stay embedded, which is the decision
// UserService was built around.
func CompilableFromUserService(route Route, svc UserService) (CompilableService, error) {
	if route.ID != svc.RouteID {
		return CompilableService{}, fmt.Errorf("compile: service %q references route %q, got route %q",
			svc.ID, svc.RouteID, route.ID)
	}

	slugs := StopSlugs(svc)

	// Identities are assigned over svc.Stops as authored and then reordered for
	// compilation, rather than assigned after sorting, so that a stop's slug
	// depends on where its author put it and not on where Seq happens to sort
	// it. That is what lets StopSlugs answer for the same stop out here.
	order := make([]int, len(svc.Stops))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return svc.Stops[order[a]].Seq < svc.Stops[order[b]].Seq })

	compiled := make([]CompilableStop, len(order))
	for i, idx := range order {
		stop := svc.Stops[idx]
		compiled[i] = CompilableStop{
			Slug:   slugs[idx],
			Lat:    stop.Lat,
			Lng:    stop.Lng,
			DwellS: svc.Vehicle.DwellS,
		}
	}

	return CompilableService{
		ID:    svc.ID,
		Route: route,
		Vehicle: Kinematics{
			MaxSpeedKMH:     svc.Vehicle.MaxSpeedKMH,
			AccelerationMS2: svc.Vehicle.AccelerationMS2,
			DecelerationMS2: svc.Vehicle.DecelerationMS2,
		},
		Stops:   compiled,
		Windows: svc.FrequencyWindows,
	}, nil
}

// StopSlugs mints the graph node key for every stop of a user-authored service —
// `{service}--{stop}` — returning one slug per stop, positionally aligned with
// svc.Stops.
//
// This is the only place those keys are minted, and it is exported because the
// slug is a persistence contract rather than a compile detail: SPA-103 stores it
// on the stop row, and a stored slug that disagreed with a derived one would
// shift a service's graph node keys underneath the backfill. Taking the whole
// service rather than a single name is what makes that guarantee keepable — the
// suffix a repeated name gets depends on the stops before it, so no per-name
// function could return the same answer the compiler uses.
//
// Stop names are not unique within a service, so a repeat takes a -2, -3, ...
// suffix, assigned in slice order — which UserService documents as the source of
// truth for the stopping pattern. A slug is therefore only as stable as the
// stops ahead of it: inserting a second "Central" before an existing one renames
// the existing one. A caller that has persisted these must re-read them after an
// edit rather than re-derive them.
func StopSlugs(svc UserService) []string {
	slugs := make([]string, len(svc.Stops))
	taken := make(map[string]bool, len(svc.Stops))
	for i, stop := range svc.Stops {
		base := Slugify(svc.Slug) + "--" + Slugify(stop.Name)
		slug := base
		for n := 2; taken[slug]; n++ {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		taken[slug] = true
		slugs[i] = slug
	}
	return slugs
}
