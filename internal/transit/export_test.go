package transit

import (
	"container/heap"

	segraph "github.com/andrewcgraves/sparks-effect-contract/transit"
)

// Test-only surface. These types and adapters used to be the package's public
// isochrone API; production stopped computing isochrones in SPA-182. They
// remain so the equivalence test can compare the embedded store against a
// compiled graph. go doc of this package does not include this file.

type Node = segraph.Node

type IsochroneData interface {
	Nodes(scenarioSlug string) ([]Node, bool)
	TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool)
}

type CompiledGraphData struct {
	Graph *TransitGraph
}

func (d CompiledGraphData) Nodes(_ string) ([]Node, bool) {
	if d.Graph == nil {
		return nil, false
	}
	nodes := make([]Node, len(d.Graph.Nodes))
	for i, n := range d.Graph.Nodes {
		nodes[i] = Node{Slug: n.Slug, Lat: n.Lat, Lng: n.Lng}
	}
	return nodes, true
}

func (d CompiledGraphData) TravelTimeBetween(_, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool) {
	if d.Graph == nil {
		return 0, 0, "", false
	}
	if fromSlug == toSlug {
		return 0, 0, "", true
	}
	return graphDijkstra(d.Graph, fromSlug, toSlug)
}

func (s *Store) Nodes(scenarioSlug string) ([]Node, bool) {
	sc, ok := s.GetScenarioBySlug(scenarioSlug)
	if !ok {
		return nil, false
	}
	stations := s.GetStationsByScenario(sc.ID)
	nodes := make([]Node, len(stations))
	for i, st := range stations {
		nodes[i] = Node{Slug: st.Slug, Lat: st.Location.Coordinates[1], Lng: st.Location.Coordinates[0]}
	}
	return nodes, true
}

func (s *Store) Graph(scenarioSlug string) (*TransitGraph, bool) {
	g, ok := s.graphs[scenarioSlug]
	return g, ok
}

func (s *Store) TravelTimeBetween(scenarioSlug, fromSlug, toSlug string) (seconds, waitSecs int, serviceID string, ok bool) {
	g, gOK := s.graphs[scenarioSlug]
	if !gOK {
		return 0, 0, "", false
	}
	if fromSlug == toSlug {
		return 0, 0, "", true
	}
	return graphDijkstra(g, fromSlug, toSlug)
}

type dijkNode struct {
	slug string
	secs int
}

type dijkHeap []dijkNode

func (h dijkHeap) Len() int           { return len(h) }
func (h dijkHeap) Less(i, j int) bool { return h[i].secs < h[j].secs }
func (h dijkHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dijkHeap) Push(x any)        { *h = append(*h, x.(dijkNode)) }
func (h *dijkHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func graphDijkstra(g *TransitGraph, from, to string) (int, int, string, bool) {
	type neighbor struct {
		slug      string
		secs      int
		serviceID string
		waitSecs  int
	}
	adj := make(map[string][]neighbor)
	for _, sg := range g.Services {
		for _, e := range sg.Edges {
			adj[e.FromSlug] = append(adj[e.FromSlug], neighbor{
				slug:      e.ToSlug,
				secs:      e.Seconds,
				serviceID: sg.ServiceID,
				waitSecs:  sg.WaitSecs,
			})
		}
	}

	type pathState struct {
		vehicleSecs int
		waitSecs    int
		serviceID   string
	}
	best := map[string]pathState{from: {}}
	h := &dijkHeap{{from, 0}}
	heap.Init(h)

	for h.Len() > 0 {
		cur := heap.Pop(h).(dijkNode)
		curState := best[cur.slug]
		if cur.secs > curState.vehicleSecs+curState.waitSecs {
			continue
		}
		if cur.slug == to {
			return curState.vehicleSecs, curState.waitSecs, curState.serviceID, true
		}
		for _, nb := range adj[cur.slug] {
			nextVehicle := curState.vehicleSecs + nb.secs
			nextWait := curState.waitSecs
			nextService := curState.serviceID
			if cur.slug == from {
				nextWait = nb.waitSecs
				nextService = nb.serviceID
			}
			nextTotal := nextVehicle + nextWait
			if prev, seen := best[nb.slug]; !seen || nextTotal < prev.vehicleSecs+prev.waitSecs {
				best[nb.slug] = pathState{nextVehicle, nextWait, nextService}
				heap.Push(h, dijkNode{nb.slug, nextTotal})
			}
		}
	}
	return 0, 0, "", false
}
