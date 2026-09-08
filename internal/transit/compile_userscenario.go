package transit

import "fmt"

func CompileUserScenario(routes []Route, services []UserService, pairs []InterchangePair, scenarioWait *BoardingWaitOverride, boardingWait BoardingWaitPolicy) (TransitGraph, error) {
	routesByID := make(map[string]Route, len(routes))
	for _, rt := range routes {
		routesByID[rt.ID] = rt
	}

	var compilables []CompilableService
	for _, svc := range services {
		rt, ok := routesByID[svc.RouteID]
		if !ok {
			return TransitGraph{}, fmt.Errorf("compile: user service %q references unknown route %q", svc.ID, svc.RouteID)
		}
		cs, err := CompilableFromUserService(rt, svc)
		if err != nil {
			return TransitGraph{}, err
		}
		cs.ScenarioBoardingWait = scenarioWait
		compilables = append(compilables, cs)
	}
	return CompileServices(compilables, pairs, boardingWait)
}
