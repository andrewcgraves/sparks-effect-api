// Package worker runs async compile jobs to completion: given a job, it loads
// the job's target composition (a seeded scenario, a user-authored scenario, or
// a single user-authored service), compiles it, and records the outcome on the
// job row so a poller sees queued -> running -> succeeded/failed.
//
// The two kinds of target compile differently, by what data they have: a
// user-authored target is compiled from track geometry and vehicle kinematics,
// while a seeded scenario is compiled from its calibrated segment run times
// (see transit.CompileSeededScenario).
package worker

import (
	"context"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Store is the slice of the repository a compile job needs: composition reads
// for each kind of target, plus the job status/result writes.
//
// The seeded reads (…ByScenario, ListVehicleTypes) and the user-authored reads
// (GetUserScenarioByID, GetUserServiceByID, ListUserServicesByIDs,
// ListRoutesByIDs) are one interface rather than two because a single job store
// backs every compile kind — the whole queued → running → succeeded/failed
// surface is shared, and Compile dispatches to the right loader by job kind.
type Store interface {
	// Seeded scenario composition. The scenario itself is read by id because a
	// job names its target that way, while its calibrated run times — the edge
	// weights a seeded compile is built from — are addressed by slug.
	GetScenarioByID(ctx context.Context, id string) (transit.Scenario, bool, error)
	transit.SeededCompileSource

	// User-authored composition. A user scenario resolves to its member
	// services; a single-service compile is the one-member degenerate case.
	// Both need the routes their services run on, loaded by id.
	GetUserScenarioByID(ctx context.Context, id string) (transit.UserScenario, bool, error)
	GetUserServiceByID(ctx context.Context, id string) (transit.UserService, bool, error)
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]transit.UserService, error)
	ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error)

	// Job lifecycle writes.
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	// CompleteJob marks a job succeeded, stores its compiled result, and records
	// which member service ids it compiled (see transit.Job.CompiledServiceIDs).
	CompleteJob(ctx context.Context, id string, result transit.TransitGraph, compiledServiceIDs []string) error
}

// Compile runs one compile job to completion: marks it running, loads and
// compiles the job's target by kind (see the package comment for what each kind
// compiles from), then stores the result or records the error.
//
// A compile failure (bad target data) is recorded on the job and does not make
// Compile itself return an error — that only happens when the job store itself
// is unreachable, which is what a caller running this in a goroutine needs to
// log.
func Compile(ctx context.Context, store Store, job transit.Job, boardingWait transit.BoardingWaitPolicy) error {
	if err := store.UpdateJobStatus(ctx, job.ID, transit.JobStatusRunning, ""); err != nil {
		return fmt.Errorf("worker: marking job %s running: %w", job.ID, err)
	}

	graph, err := compile(ctx, store, job, boardingWait)
	if err != nil {
		if failErr := store.UpdateJobStatus(ctx, job.ID, transit.JobStatusFailed, err.Error()); failErr != nil {
			return fmt.Errorf("worker: recording failure for job %s: %w", job.ID, failErr)
		}
		return nil
	}

	if err := store.CompleteJob(ctx, job.ID, graph, transit.CompiledServiceIDs(graph)); err != nil {
		return fmt.Errorf("worker: completing job %s: %w", job.ID, err)
	}
	return nil
}

// compile dispatches to the loader for the job's kind. The target FK the kind
// selects must be set — a job with a mismatched or missing target is a
// programming error on the enqueue side, surfaced as a failed job rather than a
// panic in the background goroutine.
func compile(ctx context.Context, store Store, job transit.Job, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	switch job.Kind {
	case transit.JobKindCompileScenario:
		if job.ScenarioID == nil {
			return transit.TransitGraph{}, fmt.Errorf("worker: %s job has no scenario_id", job.Kind)
		}
		return compileScenario(ctx, store, *job.ScenarioID, boardingWait)
	case transit.JobKindCompileUserScenario:
		if job.UserScenarioID == nil {
			return transit.TransitGraph{}, fmt.Errorf("worker: %s job has no user_scenario_id", job.Kind)
		}
		return compileUserScenario(ctx, store, *job.UserScenarioID, boardingWait)
	case transit.JobKindCompileUserService:
		if job.UserServiceID == nil {
			return transit.TransitGraph{}, fmt.Errorf("worker: %s job has no user_service_id", job.Kind)
		}
		return compileUserService(ctx, store, *job.UserServiceID, boardingWait)
	default:
		return transit.TransitGraph{}, fmt.Errorf("worker: unknown job kind %q", job.Kind)
	}
}

// compileScenario compiles a seeded scenario through transit's one seeded
// compile path — the same call the boot-time compile makes (SPA-181), so a
// graph produced here and one produced on startup are the same graph.
func compileScenario(ctx context.Context, store Store, scenarioID string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	sc, found, err := store.GetScenarioByID(ctx, scenarioID)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading scenario: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("worker: scenario %q not found", scenarioID)
	}
	return transit.CompileSeededScenario(ctx, store, sc, boardingWait)
}

// compileUserScenario loads a user scenario's current member services and the
// routes they run on, then compiles them as a network. A member deleted since
// the scenario last changed is already gone from its membership (the join row
// CASCADEs away), so it is simply absent here — which is exactly what makes
// the recorded compiled-service set the durable snapshot SPA-116 compares
// against.
func compileUserScenario(ctx context.Context, store Store, id string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	sc, found, err := store.GetUserScenarioByID(ctx, id)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading user scenario: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("worker: user scenario %q not found", id)
	}

	services, err := store.ListUserServicesByIDs(ctx, sc.ServiceIDs)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading member services: %w", err)
	}
	return compileUserServices(ctx, store, services, sc.InterchangePairs, sc.BoardingWait, boardingWait)
}

// compileUserService compiles a single user-authored service alone — the
// degenerate scenario compile (one member, only singleton clusters). There is
// no scenario here, so no declared interchange (SPA-120) to pass through:
// interchange is stated between two services, and there is only one.
func compileUserService(ctx context.Context, store Store, id string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	svc, found, err := store.GetUserServiceByID(ctx, id)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading user service: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("worker: user service %q not found", id)
	}
	return compileUserServices(ctx, store, []transit.UserService{svc}, nil, nil, boardingWait)
}

// compileUserServices is the shared tail of both user-authored paths: load the
// routes the services reference and hand off to the compiler.
func compileUserServices(ctx context.Context, store Store, services []transit.UserService, pairs []transit.InterchangePair, scenarioWait *transit.BoardingWaitOverride, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	routes, err := store.ListRoutesByIDs(ctx, routeIDsOf(services))
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading routes: %w", err)
	}
	return transit.CompileUserScenario(routes, services, pairs, scenarioWait, boardingWait)
}

// routeIDsOf collects the distinct route ids the services reference, so the
// routes load in one query rather than one per member.
func routeIDsOf(services []transit.UserService) []string {
	seen := make(map[string]bool, len(services))
	var ids []string
	for _, svc := range services {
		if svc.RouteID == "" || seen[svc.RouteID] {
			continue
		}
		seen[svc.RouteID] = true
		ids = append(ids, svc.RouteID)
	}
	return ids
}
