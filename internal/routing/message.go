package routing

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	secontract "github.com/andrewcgraves/sparks-effect-contract/routing"
)

type Message = secontract.Message

const SchemaVersion = secontract.SchemaVersion

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
