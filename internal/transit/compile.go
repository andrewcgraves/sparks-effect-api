package transit

import (
	"fmt"
	"sort"
)

func placeEdge(e Edge, routeID string, fromChainageM, toChainageM float64) Edge {
	e.RouteID = routeID
	e.FromChainageM = fromChainageM
	e.ToChainageM = toChainageM
	return e
}

func Compile(
	_ Scenario,
	routes []Route,
	stations []Station,
	services []Service,
	vehicleTypes []VehicleType,
	segmentRunTimes TravelTimes,
	boardingWait BoardingWaitPolicy,
) (*TransitGraph, error) {
	stationsByID := make(map[string]Station, len(stations))
	stationsBySlug := make(map[string]Station, len(stations))
	for _, st := range stations {
		stationsByID[st.ID] = st
		stationsBySlug[st.Slug] = st
	}

	vehiclesByID := make(map[string]VehicleType, len(vehicleTypes))
	for _, vt := range vehicleTypes {
		vehiclesByID[vt.ID] = vt
	}

	adj, onPath, err := buildSegmentAdj(segmentRunTimes, stationsBySlug)
	if err != nil {
		return nil, err
	}

	nodes, err := seededNodes(stations)
	if err != nil {
		return nil, err
	}

	// routes used to be discarded here. SPA-264 uses them to record, per edge,
	// which corridor the hop runs over and where its endpoints sit along it —
	// best-effort, and never a reason to fail a compile. A caller with no routes
	// to hand still compiles the same graph, minus that decoration.
	placer := newRoutePlacer(routes, stationsBySlug, segmentRunTimes)

	graph := &TransitGraph{Nodes: nodes}
	for _, svc := range services {
		if !svc.Active {
			continue
		}
		vt, ok := vehiclesByID[svc.VehicleTypeID]
		if !ok {
			return nil, fmt.Errorf("compile: service %q references unknown vehicle type %q", svc.ID, svc.VehicleTypeID)
		}

		stops := append([]ServiceStop(nil), svc.Stops...)
		sort.Slice(stops, func(i, j int) bool { return stops[i].Sequence < stops[j].Sequence })

		stopByStationID := make(map[string]ServiceStop, len(stops))
		slugs := make([]string, 0, len(stops))
		for _, stop := range stops {
			st, ok := stationsByID[stop.StationID]
			if !ok {
				return nil, fmt.Errorf("compile: service %q references unknown station id %q", svc.ID, stop.StationID)
			}
			if !onPath[st.Slug] {
				return nil, fmt.Errorf("compile: service %q stop %q is not on any segment path", svc.ID, st.Slug)
			}
			stopByStationID[stop.StationID] = stop
			slugs = append(slugs, st.Slug)
		}

		sg := ServiceGraph{ServiceID: svc.ID}
		policy, _, err := ResolveBoardingWait(svc.BoardingWait, nil, boardingWait)
		if err != nil {
			return nil, fmt.Errorf("compile: service %q: %w", svc.ID, err)
		}
		if err := applyBoardingWait(&sg, policy, svc.FrequencyWindows); err != nil {
			return nil, fmt.Errorf("compile: service %q: %w", svc.ID, err)
		}
		for i := 0; i+1 < len(slugs); i++ {
			fromSlug, toSlug := slugs[i], slugs[i+1]
			runSecs, path, pathErr := segmentPathSeconds(adj, fromSlug, toSlug)
			if pathErr != nil {
				return nil, fmt.Errorf("compile: service %q: %w", svc.ID, pathErr)
			}
			// Reverse run time is the same physical path walked backwards, not
			// a second search: on a diamond the two BFS traversals can pick
			// different equal-hop routes because adjacency-list order is not
			// symmetric. The adjacency is built bidirectionally per segment,
			// so a path the forward search found is always walkable in reverse.
			revPath := reversePath(path)
			revRunSecs := pathRunSeconds(adj, revPath)
			fwdDwell := pathDwellSecs(path, stationsBySlug, stopByStationID, vt)
			revDwell := pathDwellSecs(revPath, stationsBySlug, stopByStationID, vt)
			fwd := Edge{FromSlug: fromSlug, ToSlug: toSlug, Seconds: runSecs + fwdDwell, DwellS: fwdDwell}
			rev := Edge{FromSlug: toSlug, ToSlug: fromSlug, Seconds: revRunSecs + revDwell, DwellS: revDwell}
			// The reverse edge is the same hop the other way, so it carries the
			// same two chainages swapped — descending, which nothing that reads
			// them treats as a special case.
			if routeID, fromChainageM, toChainageM, placed := placer.place(path); placed {
				fwd = placeEdge(fwd, routeID, fromChainageM, toChainageM)
				rev = placeEdge(rev, routeID, toChainageM, fromChainageM)
			}
			sg.Edges = append(sg.Edges, fwd, rev)
		}
		graph.Services = append(graph.Services, sg)
	}
	return graph, nil
}

func seededNodes(stations []Station) ([]GraphNode, error) {
	if len(stations) == 0 {
		return nil, nil
	}
	nodes := make([]GraphNode, len(stations))
	for i, st := range stations {
		if len(st.Location.Coordinates) != 2 {
			return nil, fmt.Errorf("compile: station %q has no usable location: %v",
				st.Slug, st.Location.Coordinates)
		}
		node := GraphNode{
			Slug:  st.Slug,
			Lat:   st.Location.Coordinates[1],
			Lng:   st.Location.Coordinates[0],
			Names: []string{st.Name},
		}
		if rl := st.RoutingLocation; rl != nil {
			if len(rl.Coordinates) != 2 {
				return nil, fmt.Errorf("compile: station %q has an unusable routing_location: %v",
					st.Slug, rl.Coordinates)
			}
			node.RoutingLat = &rl.Coordinates[1]
			node.RoutingLng = &rl.Coordinates[0]
		}
		nodes[i] = node
	}
	return nodes, nil
}

func CompiledServiceIDs(g TransitGraph) []string {
	if len(g.Services) == 0 {
		return nil
	}
	ids := make([]string, len(g.Services))
	for i, sg := range g.Services {
		ids[i] = sg.ServiceID
	}
	return ids
}

type segEdge struct {
	to      string
	seconds int
}

func buildSegmentAdj(tt TravelTimes, stationsBySlug map[string]Station) (map[string][]segEdge, map[string]bool, error) {
	adj := make(map[string][]segEdge, len(tt.Segments)*2)
	onPath := make(map[string]bool)
	for _, seg := range tt.Segments {
		if _, ok := stationsBySlug[seg.FromSlug]; !ok {
			return nil, nil, fmt.Errorf("compile: unknown station slug %q in segment times", seg.FromSlug)
		}
		if _, ok := stationsBySlug[seg.ToSlug]; !ok {
			return nil, nil, fmt.Errorf("compile: unknown station slug %q in segment times", seg.ToSlug)
		}
		secs := seg.RunSeconds
		revSecs := secs
		if seg.ReverseRunSeconds != nil {
			if *seg.ReverseRunSeconds <= 0 {
				return nil, nil, fmt.Errorf("compile: non-positive reverse_run_seconds on segment %s→%s",
					seg.FromSlug, seg.ToSlug)
			}
			revSecs = *seg.ReverseRunSeconds
		}
		adj[seg.FromSlug] = append(adj[seg.FromSlug], segEdge{seg.ToSlug, secs})
		adj[seg.ToSlug] = append(adj[seg.ToSlug], segEdge{seg.FromSlug, revSecs})
		onPath[seg.FromSlug] = true
		onPath[seg.ToSlug] = true
	}
	return adj, onPath, nil
}

func segmentPathSeconds(adj map[string][]segEdge, from, to string) (int, []string, error) {
	if from == to {
		return 0, []string{from}, nil
	}
	type node struct {
		slug string
		secs int
	}
	prev := map[string]string{}
	visited := map[string]bool{from: true}
	queue := []node{{from, 0}}
	found := false
	var total int
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur.slug] {
			if visited[e.to] {
				continue
			}
			visited[e.to] = true
			prev[e.to] = cur.slug
			nextSecs := cur.secs + e.seconds
			if e.to == to {
				found = true
				total = nextSecs
				queue = nil
				break
			}
			queue = append(queue, node{e.to, nextSecs})
		}
	}
	if !found {
		return 0, nil, fmt.Errorf("no segment path from %q to %q", from, to)
	}
	path := []string{to}
	for cur := to; cur != from; cur = prev[cur] {
		path = append(path, prev[cur])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return total, path, nil
}

func pathRunSeconds(adj map[string][]segEdge, path []string) int {
	secs := 0
	for i := 0; i+1 < len(path); i++ {
		from, to := path[i], path[i+1]
		for _, e := range adj[from] {
			if e.to == to {
				secs += e.seconds
				break
			}
		}
	}
	return secs
}

func pathDwellSecs(path []string, stationsBySlug map[string]Station, stopByStationID map[string]ServiceStop, vt VehicleType) int {
	dwellSecs := 0
	for _, slug := range path[1:] {
		st := stationsBySlug[slug]
		stop, calls := stopByStationID[st.ID]
		if !calls {
			continue
		}
		dwellSecs += resolveDwell(stop, st, vt)
	}
	return dwellSecs
}

func reversePath(path []string) []string {
	out := make([]string, len(path))
	for i, slug := range path {
		out[len(path)-1-i] = slug
	}
	return out
}

func resolveDwell(stop ServiceStop, st Station, vt VehicleType) int {
	if stop.DwellS != nil {
		return *stop.DwellS
	}
	if st.PlatformHeight == vt.FloorHeight {
		return vt.DwellLevelS
	}
	return vt.DwellStepS
}
