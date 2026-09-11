package transit

import segraph "github.com/andrewcgraves/sparks-effect-contract/transit"

type Edge = segraph.Edge
type ServiceGraph = segraph.ServiceGraph
type GraphNode = segraph.GraphNode
type TransitGraph = segraph.TransitGraph
type StopRef = segraph.StopRef
type StopCluster = segraph.StopCluster
type NearMiss = segraph.NearMiss
type MergeReport = segraph.MergeReport
type TravelMode = segraph.TravelMode

const (
	TravelModeWalk    = segraph.TravelModeWalk
	TravelModeBike    = segraph.TravelModeBike
	TravelModeDrive   = segraph.TravelModeDrive
	TravelModeTransit = segraph.TravelModeTransit
)

func TravelModes() []TravelMode { return segraph.TravelModes() }

func TravelModeList() string { return segraph.TravelModeList() }
