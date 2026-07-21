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
// Slugs are namespaced by the owning service (`{service}--{stop}`) so two
// services that both stop at "Downtown" stay distinct in a graph assembled from
// both. No Station row is created: stops stay embedded, which is the decision
// UserService was built around.
func CompilableFromUserService(route Route, svc UserService) CompilableService {
	stops := append([]ServiceStopPoint(nil), svc.Stops...)
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].Seq < stops[j].Seq })

	compiled := make([]CompilableStop, len(stops))
	taken := make(map[string]bool, len(stops))
	for i, stop := range stops {
		compiled[i] = CompilableStop{
			Slug:   uniqueStopSlug(svc.Slug, stop.Name, taken),
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
	}
}

// StopSlug mints the graph node key for one stop of a user-authored service:
// `{service}--{stop}`, namespaced so two services that both stop at "Downtown"
// stay distinct in a graph assembled from both.
//
// Exported because it is a persistence contract, not just a compile detail.
// SPA-103 stores this slug on the stop row; a stored slug and a derived one
// must agree exactly, or a service's graph node keys shift underneath the
// backfill. Both sides call this.
func StopSlug(serviceSlug, stopName string) string {
	return Slugify(serviceSlug) + "--" + Slugify(stopName)
}

// uniqueStopSlug is StopSlug plus collision handling, so two stops sharing a
// name within one service do not collapse onto the same graph node. taken
// accumulates across a service's stops and is mutated by each call.
func uniqueStopSlug(serviceSlug, stopName string, taken map[string]bool) string {
	base := StopSlug(serviceSlug, stopName)
	slug := base
	for n := 2; taken[slug]; n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	taken[slug] = true
	return slug
}
