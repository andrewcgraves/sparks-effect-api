package routing

import "github.com/andrewcgraves/sparks-effect-contract/transit"

const SchemaVersion = 1

type Message struct {
	SchemaVersion int                   `json:"schema_version"`
	RoutingJobID  string                `json:"routing_job_id"`
	CompileJobID  string                `json:"compile_job_id"`
	Graph         *transit.TransitGraph `json:"graph"`
	Lat           float64               `json:"lat"`
	Lng           float64               `json:"lng"`
	BudgetMins    int                   `json:"budget_mins"`
	Mode          transit.TravelMode    `json:"mode"`
	TraceID       string                `json:"trace_id"`
}
