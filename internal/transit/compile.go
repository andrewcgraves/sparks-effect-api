package transit

import (
	"fmt"
	"sort"
)

type Edge struct {
	FromSlug string `json:"from_slug"`
	ToSlug   string `json:"to_slug"`
	Seconds  int    `json:"seconds"`
}

type ServiceGraph struct {
	ServiceID string `json:"service_id"`
	Edges     []Edge `json:"edges"`
	WaitSecs  int    `json:"wait_secs"`
}

// TransitGraph is a compiled, Dijkstra-ready representation of a scenario's
// active services — either hand-authored (Compile) or physics-derived
// (CompileScenario) — and is what an async compile job persists as its result
// (see Job.Result).
//
// Merge records how interchange was resolved when several services were
// compiled together: which stops were folded onto one node and which nearly
// were. It is empty for a hand-authored compile and for a physics compile with
// nothing to merge. It rides on the job result rather than a separate endpoint
// so the poller contract is unchanged — a client already reading the graph
// reads the report from the same payload.
type TransitGraph struct {
	Services []ServiceGraph `json:"services"`
	Merge    MergeReport    `json:"merge,omitempty"`
}

func Compile(
	_ Scenario,
	_ []Route,
	stations []Station,
	services []Service,
	vehicleTypes []VehicleType,
	segmentRunTimes TravelTimes,
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

	graph := &TransitGraph{}
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

		sg := ServiceGraph{ServiceID: svc.ID, WaitSecs: bestHeadwayOver2(svc.FrequencyWindows)}
		for i := 0; i+1 < len(slugs); i++ {
			fromSlug, toSlug := slugs[i], slugs[i+1]
			runSecs, path, pathErr := segmentPathSeconds(adj, fromSlug, toSlug)
			if pathErr != nil {
				return nil, fmt.Errorf("compile: service %q: %w", svc.ID, pathErr)
			}
			fwd := runSecs + pathDwellSecs(path, stationsBySlug, stopByStationID, vt)
			rev := runSecs + pathDwellSecs(reversePath(path), stationsBySlug, stopByStationID, vt)
			sg.Edges = append(sg.Edges,
				Edge{FromSlug: fromSlug, ToSlug: toSlug, Seconds: fwd},
				Edge{FromSlug: toSlug, ToSlug: fromSlug, Seconds: rev},
			)
		}
		graph.Services = append(graph.Services, sg)
	}
	return graph, nil
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
		adj[seg.FromSlug] = append(adj[seg.FromSlug], segEdge{seg.ToSlug, secs})
		adj[seg.ToSlug] = append(adj[seg.ToSlug], segEdge{seg.FromSlug, secs})
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

func bestHeadwayOver2(windows []FrequencyWindow) int {
	if len(windows) == 0 {
		return 0
	}
	best := windows[0].HeadwayS
	for _, w := range windows[1:] {
		if w.HeadwayS < best {
			best = w.HeadwayS
		}
	}
	return best / 2
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
