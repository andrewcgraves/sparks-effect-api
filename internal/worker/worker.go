// Package worker runs async compile jobs to completion: given a scenario id,
// it loads the scenario's composition, physics-compiles it, and records the
// outcome on the job row so a poller sees queued -> running -> succeeded/failed.
package worker

import (
	"context"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Store is the slice of the repository a compile job needs: scenario
// composition reads, plus the job status/result writes.
type Store interface {
	ListRoutesByScenario(ctx context.Context, scenarioID string) ([]transit.Route, error)
	ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error)
	ListServicesByScenario(ctx context.Context, scenarioID string) ([]transit.Service, error)
	ListVehicleTypes(ctx context.Context) ([]transit.VehicleType, error)
	UpdateJobStatus(ctx context.Context, id, status, errMsg string) error
	CompleteJob(ctx context.Context, id string, result transit.TransitGraph) error
}

// Compile runs one compile job to completion: marks it running, loads and
// compiles the named scenario's routes/stations/services/vehicle types (the
// physics compile — stop-snapping, speed limits, profile integration — and
// graph build, via transit.CompileScenario), then stores the result or
// records the error.
//
// A compile failure (bad scenario data) is recorded on the job and does not
// make Compile itself return an error — that only happens when the job store
// itself is unreachable, which is what a caller running this in a goroutine
// needs to log.
func Compile(ctx context.Context, store Store, jobID, scenarioID string) error {
	if err := store.UpdateJobStatus(ctx, jobID, transit.JobStatusRunning, ""); err != nil {
		return fmt.Errorf("worker: marking job %s running: %w", jobID, err)
	}

	graph, err := compileScenario(ctx, store, scenarioID)
	if err != nil {
		if failErr := store.UpdateJobStatus(ctx, jobID, transit.JobStatusFailed, err.Error()); failErr != nil {
			return fmt.Errorf("worker: recording failure for job %s: %w", jobID, failErr)
		}
		return nil
	}

	if err := store.CompleteJob(ctx, jobID, graph); err != nil {
		return fmt.Errorf("worker: completing job %s: %w", jobID, err)
	}
	return nil
}

func compileScenario(ctx context.Context, store Store, scenarioID string) (transit.TransitGraph, error) {
	routes, err := store.ListRoutesByScenario(ctx, scenarioID)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading routes: %w", err)
	}
	stations, err := store.ListStationsByScenario(ctx, scenarioID)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading stations: %w", err)
	}
	services, err := store.ListServicesByScenario(ctx, scenarioID)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading services: %w", err)
	}
	vehicleTypes, err := store.ListVehicleTypes(ctx)
	if err != nil {
		return transit.TransitGraph{}, fmt.Errorf("worker: loading vehicle types: %w", err)
	}
	return transit.CompileScenario(routes, stations, services, vehicleTypes)
}
