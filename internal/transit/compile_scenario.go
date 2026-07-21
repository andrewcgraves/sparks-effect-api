package transit

import "fmt"

// CompileScenario builds a scenario's TransitGraph directly from track
// geometry and vehicle kinematics — CompileServicePhysics per active service —
// as an alternative to Compile's hand-authored segment-run-time table. This is
// the "graph build" step an async compile job (internal/worker) runs.
func CompileScenario(routes []Route, stations []Station, services []Service, vehicleTypes []VehicleType) (TransitGraph, error) {
	routesByID := make(map[string]Route, len(routes))
	for _, rt := range routes {
		routesByID[rt.ID] = rt
	}
	vehiclesByID := make(map[string]VehicleType, len(vehicleTypes))
	for _, vt := range vehicleTypes {
		vehiclesByID[vt.ID] = vt
	}

	var graph TransitGraph
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

		sg, err := CompileServicePhysics(rt, stations, svc, vt)
		if err != nil {
			return TransitGraph{}, err
		}
		graph.Services = append(graph.Services, sg)
	}
	return graph, nil
}
