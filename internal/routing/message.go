package routing

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

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

func MessageFor(job transit.RoutingJob, graph *transit.TransitGraph, traceID string) Message {
	return Message{
		SchemaVersion: SchemaVersion,
		RoutingJobID:  job.ID,
		CompileJobID:  job.CompileJobID,
		Graph:         graph,
		Lat:           job.Lat,
		Lng:           job.Lng,
		BudgetMins:    job.BudgetMins,
		Mode:          job.Mode,
		TraceID:       traceID,
	}
}

type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}
