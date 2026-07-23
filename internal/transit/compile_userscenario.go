package transit

import "fmt"

// CompileUserScenario builds a user-authored scenario's TransitGraph from its
// member services and the routes they run on — the user-authored counterpart to
// CompileScenario.
//
// The two mirror each other by design: each projects its own domain model onto
// CompilableService (via the SPA-102 adapters — CompilableFromUserService here,
// CompilableFromService there) and hands the set to CompileServices, so the
// co-located-stop merge that resolves interchange (SPA-109) and the node set
// that carries the graph's geometry (SPA-111) are produced by one shared seam
// rather than reimplemented per model.
//
// A single-service compile is just this with a one-member slice: under the
// anchored merge a lone service has only singleton clusters, so every cluster
// key is that service's own stop slug and the result is byte-identical to
// compiling it with no merge at all. There is deliberately no separate
// single-service path.
//
// Unlike CompileScenario there is no Active filter: membership in a user
// scenario is the explicit curated set the caller assembled, so every service
// handed in is compiled. A route a member references but that is absent from
// routes is a caller error, not a silent skip.
//
// pairs is the owning UserScenario's declared interchange (SPA-120), passed
// through to CompileServices rather than looked up here — the caller already
// has the UserScenario in hand (see worker.compileUserScenario) and this
// stays a pure function of routes and services otherwise.
func CompileUserScenario(routes []Route, services []UserService, pairs []InterchangePair) (TransitGraph, error) {
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
		compilables = append(compilables, cs)
	}
	return CompileServices(compilables, pairs)
}
