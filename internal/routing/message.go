// Package routing carries isochrone work from the API to the routing worker.
//
// Nothing in this repository computes an isochrone any more. Valhalla runs
// ClusterIP-only on the home cluster with no ingress, so the API — deployed
// elsewhere — cannot reach it, and the queue is the only transport between
// them. What lives here is one side of that transport: the message contract,
// and a publisher that will not report success until the broker has confirmed
// the message is safely stored.
//
// The API resolves everything before publishing: authentication, ownership, the
// target slug, and the stale-graph check. By the time a Message exists it names
// one specific immutable compiled graph and carries it inline, so the worker
// needs no database of its own and no piece of resolution logic crosses the
// repository boundary.
package routing

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// SchemaVersion is the version of the Message wire format. It rides on every
// message so the worker can reject — loudly — a shape it was not built for,
// rather than silently reading zero values out of fields that moved.
//
// Bump it for any change that an existing consumer would misread. Adding an
// optional field a worker may ignore is not such a change; renaming, removing,
// or repurposing one is.
const SchemaVersion = 1

// Message is one isochrone for the worker to compute. It is a contract between
// two repositories with no compiler checking either side, so its shape is
// pinned by a golden fixture (testdata/message.golden.json) that this repo
// asserts it produces and the worker repo asserts it consumes. Change the
// struct and that test fails until the fixture is updated deliberately.
//
// Graph travels inline rather than by reference. A compiled graph is immutable
// once its compile job succeeds, so there is no staleness to worry about, and
// the sizes involved are ordinary: 2,894 bytes for the seeded CA HSR scenario,
// around 30 KB for a large authored one. Carrying it costs a few kilobytes and
// saves the worker a database it would otherwise need.
//
// Field order here is the key order on the wire, since encoding/json emits
// struct fields in declaration order. The golden fixture depends on it.
type Message struct {
	SchemaVersion int    `json:"schema_version"`
	RoutingJobID  string `json:"routing_job_id"`
	// CompileJobID identifies the graph below: a compiled graph's identity is
	// the job that produced it. The worker keys its result cache on it.
	CompileJobID string                `json:"compile_job_id"`
	Graph        *transit.TransitGraph `json:"graph"`
	Lat          float64               `json:"lat"`
	Lng          float64               `json:"lng"`
	BudgetMins   int                   `json:"budget_mins"`
	Mode         transit.TravelMode    `json:"mode"`
	// TraceID is the trace the request that created this job carries (see
	// internal/traceid) — a caller-supplied id, or one the API minted when it
	// received none. The worker logs it alongside its own output, so one
	// isochrone's logs can be followed across both services rather than
	// stopping at the queue. Added as a plain field a worker built before it
	// existed can simply ignore, so it does not require a SchemaVersion bump.
	TraceID string `json:"trace_id"`
}

// MessageFor builds the message for a routing job over the graph it names,
// carrying traceID (see internal/traceid) so the worker can log under the
// same trace as the request that created the job.
//
// It lives here, beside the contract, rather than in the handler that calls it:
// the job and the message carry the same six request fields, and copying them
// by hand at the call site is where a field silently stops being sent. Anything
// that mints a routing job gets the message for it the one way.
//
// graph is passed separately because a RoutingJob records only the compile job
// that identifies the graph, never the graph itself — the bytes are resolved
// once, at the point the job is created, and travel no further.
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

// Publisher hands a Message to the queue and reports whether the broker
// accepted responsibility for it.
//
// Publish must not return nil until the message is confirmed durable. The API
// inserts the routing job row first and publishes second; a publish that
// reported success without being confirmed would leave that row queued forever,
// polled by a client, seen by no worker.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}
