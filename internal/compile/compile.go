package compile

import (
	"context"
	"fmt"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/metrics"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type Store interface {
	GetScenarioByID(ctx context.Context, id string) (transit.Scenario, bool, error)
	transit.SeededCompileSource
	GetUserScenarioByID(ctx context.Context, id string) (transit.UserScenario, bool, error)
	GetUserServiceByID(ctx context.Context, id string) (transit.UserService, bool, error)
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]transit.UserService, error)
	ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error)
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	CompleteJob(ctx context.Context, id string, result transit.TransitGraph, compiledServiceIDs []string) error
}

// outcomeError is a job whose status could not be written, as distinct from
// a compile that ran and failed: the job is left wherever it was.
const outcomeError = "error"

func Compile(ctx context.Context, store Store, job transit.Job, boardingWait transit.BoardingWaitPolicy, m *metrics.Metrics) error {
	start := time.Now()
	outcome, err := run(ctx, store, job, boardingWait)
	m.Compile(ctx, kindLabel(job.Kind), outcome, time.Since(start))
	return err
}

func run(ctx context.Context, store Store, job transit.Job, boardingWait transit.BoardingWaitPolicy) (outcome string, err error) {
	if err := store.UpdateJobStatus(ctx, job.ID, transit.JobStatusRunning, ""); err != nil {
		return outcomeError, fmt.Errorf("compile: marking job %s running: %w", job.ID, err)
	}

	graph, err := compile(ctx, store, job, boardingWait)
	if err != nil {
		if failErr := store.UpdateJobStatus(ctx, job.ID, transit.JobStatusFailed, err.Error()); failErr != nil {
			return outcomeError, fmt.Errorf("compile: recording failure for job %s: %w", job.ID, failErr)
		}
		return transit.JobStatusFailed, nil
	}

	if err := store.CompleteJob(ctx, job.ID, graph, transit.CompiledServiceIDs(graph)); err != nil {
		return outcomeError, fmt.Errorf("compile: completing job %s: %w", job.ID, err)
	}
	return transit.JobStatusSucceeded, nil
}

// Kind comes from a database row, so an unrecognised one is folded into
// "other" rather than becoming a label value of its own.
func kindLabel(kind string) string {
	switch kind {
	case transit.JobKindCompileScenario, transit.JobKindCompileUserScenario, transit.JobKindCompileUserService:
		return kind
	default:
		return "other"
	}
}

func compile(ctx context.Context, store Store, job transit.Job, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	switch job.Kind {
	case transit.JobKindCompileScenario:
		if job.ScenarioID == nil {
			return transit.TransitGraph{}, fmt.Errorf("compile: %s job has no scenario_id", job.Kind)
		}
		return compileScenario(ctx, store, *job.ScenarioID, boardingWait)
	case transit.JobKindCompileUserScenario:
		if job.UserScenarioID == nil {
			return transit.TransitGraph{}, fmt.Errorf("compile: %s job has no user_scenario_id", job.Kind)
		}
		return compileUserScenario(ctx, store, *job.UserScenarioID, boardingWait)
	case transit.JobKindCompileUserService:
		if job.UserServiceID == nil {
			return transit.TransitGraph{}, fmt.Errorf("compile: %s job has no user_service_id", job.Kind)
		}
		return compileUserService(ctx, store, *job.UserServiceID, boardingWait)
	default:
		return transit.TransitGraph{}, fmt.Errorf("compile: unknown job kind %q", job.Kind)
	}
}

func compileScenario(ctx context.Context, store Store, scenarioID string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	sc, found, err := store.GetScenarioByID(ctx, scenarioID)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("compile: loading scenario: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("compile: scenario %q not found", scenarioID)
	}
	return transit.CompileSeededScenario(ctx, store, sc, boardingWait)
}

func compileUserScenario(ctx context.Context, store Store, id string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	sc, found, err := store.GetUserScenarioByID(ctx, id)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("compile: loading user scenario: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("compile: user scenario %q not found", id)
	}

	services, err := store.ListUserServicesByIDs(ctx, sc.ServiceIDs)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("compile: loading member services: %w", err)
	}
	return compileUserServices(ctx, store, services, sc.InterchangePairs, sc.BoardingWait, boardingWait)
}

func compileUserService(ctx context.Context, store Store, id string, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	svc, found, err := store.GetUserServiceByID(ctx, id)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("compile: loading user service: %w", err)
	}
	if !found {
		return transit.TransitGraph{}, fmt.Errorf("compile: user service %q not found", id)
	}
	return compileUserServices(ctx, store, []transit.UserService{svc}, nil, nil, boardingWait)
}

func compileUserServices(ctx context.Context, store Store, services []transit.UserService, pairs []transit.InterchangePair, scenarioWait *transit.BoardingWaitOverride, boardingWait transit.BoardingWaitPolicy) (transit.TransitGraph, error) {
	routes, err := store.ListRoutesByIDs(ctx, routeIDsOf(services))
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("compile: loading routes: %w", err)
	}
	return transit.CompileUserScenario(routes, services, pairs, scenarioWait, boardingWait)
}

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
