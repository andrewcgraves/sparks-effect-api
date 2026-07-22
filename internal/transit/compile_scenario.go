package transit

import "fmt"

// CompileScenario builds a scenario's TransitGraph directly from track
// geometry and vehicle kinematics — CompileServicePhysics per active service —
// as an alternative to Compile's hand-authored segment-run-time table. This is
// the "graph build" step an async compile job (internal/worker) runs.
//
// It adapts the seeded model onto CompilableService and hands off to
// CompileServices, so the co-located-stop merge that turns a set of services
// into a network runs here too. For the seeded model that merge is a no-op on
// the keys — services sharing a Station already carry the identical station
// slug, so a cluster's key is the slug they already shared — but running it
// keeps one compile path rather than two, and it does report the shared
// stations as realised clusters.
func CompileScenario(routes []Route, stations []Station, services []Service, vehicleTypes []VehicleType) (TransitGraph, error) {
	routesByID := make(map[string]Route, len(routes))
	for _, rt := range routes {
		routesByID[rt.ID] = rt
	}
	vehiclesByID := make(map[string]VehicleType, len(vehicleTypes))
	for _, vt := range vehicleTypes {
		vehiclesByID[vt.ID] = vt
	}

	var compilables []CompilableService
	for _, svc := range services {
		if !svc.Active {
			continue
		}
		rt, ok := routesByID[svc.RouteID]
		if !ok {
			return TransitGraph{}, fmt.Errorf("compile: service %q references unknown route %q", svc.ID, svc.RouteID)
		}
		vt, ok := vehiclesByID[svc.VehicleTypeID]
		if !ok {
			return TransitGraph{}, fmt.Errorf("compile: service %q references unknown vehicle type %q", svc.ID, svc.VehicleTypeID)
		}

		cs, err := CompilableFromService(rt, stations, svc, vt)
		if err != nil {
			return TransitGraph{}, err
		}
		compilables = append(compilables, cs)
	}
	return CompileServices(compilables)
}

// CompileServices compiles a set of services that share a scenario into one
// TransitGraph, resolving interchange between them.
//
// It is the seam a single-service compile and a multi-service scenario compile
// share, which is what makes the single-service case fall out with no special
// case: MergeColocatedStops runs regardless, and over a lone service every
// cluster is a singleton, so every key is the service's own stop slug —
// byte-identical to compiling it with no merge at all. A curated set of several
// services is the same call with more stops in front of the merge.
//
// The order is load-bearing: merge first, compile second. The merge rewrites
// stop slugs to cluster keys, and CompileServicePhysics keys its edges on
// whatever slug it is handed — so two services' stops that merged onto one key
// emit edges naming that one key, which is the whole of how graphDijkstra later
// finds a path between them. Compiling first would bake in the per-service
// identities and leave nothing to connect.
//
// The merge is cross-service only and never hands one service two stops with
// the same key, so CompileServicePhysics' duplicate-slug check still guards
// each service exactly as before.
func CompileServices(svcs []CompilableService) (TransitGraph, error) {
	merged, report := MergeColocatedStops(svcs)

	graph := TransitGraph{Merge: report}
	for _, cs := range merged {
		sg, err := CompileServicePhysics(cs)
		if err != nil {
			return TransitGraph{}, err
		}
		graph.Services = append(graph.Services, sg)
	}
	return graph, nil
}
