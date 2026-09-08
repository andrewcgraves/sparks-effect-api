package transit

import "fmt"

func CompileScenario(routes []Route, stations []Station, services []Service, vehicleTypes []VehicleType, boardingWait BoardingWaitPolicy) (TransitGraph, error) {
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
	return CompileServices(compilables, nil, boardingWait)
}

func CompileServices(svcs []CompilableService, pairs []InterchangePair, boardingWait BoardingWaitPolicy) (TransitGraph, error) {
	if err := validateInterchangePairs(svcs, pairs); err != nil {
		return TransitGraph{}, err
	}
	merged, report, nodes := MergeColocatedStops(svcs, pairs)

	// nodes come straight from the same clustering that rewrote the slugs, so
	// they carry exactly one node per key the edges below emit — the closure
	// the graph would otherwise lack, and which the hand-authored Compile does
	// not provide (its seeded isochrone sources positions elsewhere).
	graph := TransitGraph{Merge: report, Nodes: nodes}
	for _, cs := range merged {
		sg, err := CompileServicePhysics(cs, boardingWait)
		if err != nil {
			return TransitGraph{}, err
		}
		graph.Services = append(graph.Services, sg)
	}
	return graph, nil
}
