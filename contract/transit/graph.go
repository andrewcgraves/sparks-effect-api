package transit

type Edge struct {
	FromSlug      string  `json:"from_slug"`
	ToSlug        string  `json:"to_slug"`
	Seconds       int     `json:"seconds"`
	DwellS        int     `json:"dwell_s,omitempty"`
	RouteID       string  `json:"route_id,omitempty"`
	FromChainageM float64 `json:"from_chainage_m,omitempty"`
	ToChainageM   float64 `json:"to_chainage_m,omitempty"`
}

type ServiceGraph struct {
	ServiceID  string `json:"service_id"`
	Edges      []Edge `json:"edges"`
	WaitSecs   int    `json:"wait_secs"`
	WaitPolicy string `json:"wait_policy,omitempty"`
}

type GraphNode struct {
	Slug       string   `json:"slug"`
	Lat        float64  `json:"lat"`
	Lng        float64  `json:"lng"`
	RoutingLat *float64 `json:"routing_lat,omitempty"`
	RoutingLng *float64 `json:"routing_lng,omitempty"`
	Names      []string `json:"names"`
}

type TransitGraph struct {
	Services []ServiceGraph `json:"services"`
	Merge    MergeReport    `json:"merge,omitempty"`
	Nodes    []GraphNode    `json:"nodes,omitempty"`
}

type StopRef struct {
	ServiceID string `json:"service_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

type StopCluster struct {
	Key     string    `json:"key"`
	Names   []string  `json:"names"`
	Members []StopRef `json:"members"`
}

type NearMiss struct {
	A         StopRef `json:"a"`
	B         StopRef `json:"b"`
	DistanceM float64 `json:"distance_m"`
}

type MergeReport struct {
	Clusters   []StopCluster `json:"clusters,omitempty"`
	NearMisses []NearMiss    `json:"near_misses,omitempty"`
}
