package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

// --- Scenarios ---

func (r *Repo) UpdateScenario(ctx context.Context, sc transit.Scenario) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE scenarios
		    SET name = $2, description = $3, status = $4, updated_at = now()
		  WHERE id = $1`,
		sc.ID, sc.Name, sc.Description, sc.Status)
	if err != nil {
		return wrap("UpdateScenario", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateScenario: no scenario with id %q", sc.ID)
	}
	return nil
}

func (r *Repo) DeleteScenario(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM scenarios WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteScenario", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteScenario: no scenario with id %q", id)
	}
	return nil
}

func (r *Repo) CountUnownedScenarioChildren(ctx context.Context, scenarioID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM routes   WHERE scenario_id = $1 AND owner_id IS NULL)
		      + (SELECT count(*) FROM stations WHERE scenario_id = $1 AND owner_id IS NULL)
		      + (SELECT count(*) FROM services WHERE scenario_id = $1 AND owner_id IS NULL)`,
		scenarioID).Scan(&n)
	if err != nil {
		return 0, wrap("CountUnownedScenarioChildren", err)
	}
	return n, nil
}

// --- Routes ---

func (r *Repo) UpdateRoute(ctx context.Context, rt transit.Route) error {
	// segments is NOT NULL; mirror CreateRoute so a route edited down to no
	// authored physics stores an empty array rather than NULL.
	segments := rt.Segments
	if segments == nil {
		segments = []transit.RouteSegment{}
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE routes
		    SET scenario_id = $2, slug = $3, name = $4, description = $5, mode = $6,
		        geometry = $7, bidirectional = $8, segments = $9, updated_at = now()
		  WHERE id = $1`,
		rt.ID, rt.ScenarioID, rt.Slug, rt.Name, rt.Description, rt.Mode,
		rt.Geometry, rt.Bidirectional, segments)
	if err != nil {
		return wrap("UpdateRoute", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateRoute: no route with id %q", rt.ID)
	}
	return nil
}

func (r *Repo) DeleteRoute(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteRoute", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteRoute: no route with id %q", id)
	}
	return nil
}

func (r *Repo) CountRouteDependents(ctx context.Context, routeID string) (transit.RouteDependents, error) {
	var d transit.RouteDependents
	err := r.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM services      WHERE route_id = $1),
		        (SELECT count(*) FROM user_services WHERE route_id = $1),
		        (SELECT count(*) FROM segments      WHERE route_id = $1)`,
		routeID).Scan(&d.Services, &d.UserServices, &d.Segments)
	if err != nil {
		return transit.RouteDependents{}, wrap("CountRouteDependents", err)
	}
	return d, nil
}

// --- Stations ---

func (r *Repo) GetStationBySlug(ctx context.Context, scenarioID, slug string) (transit.Station, bool, error) {
	var st transit.Station
	err := r.pool.QueryRow(ctx,
		`SELECT `+stationColumns+` FROM stations WHERE scenario_id = $1 AND slug = $2`,
		scenarioID, slug).Scan(&st.ID, &st.ScenarioID, &st.OwnerID, &st.Slug, &st.Name,
		&st.Location, &st.RoutingLocation, &st.PlatformHeight)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.Station{}, false, nil
	}
	if err != nil {
		return transit.Station{}, false, wrap("GetStationBySlug", err)
	}
	return st, true, nil
}

func (r *Repo) UpdateStation(ctx context.Context, st transit.Station) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE stations
		    SET slug = $2, name = $3, location = $4, routing_location = $5,
		        platform_height = $6, updated_at = now()
		  WHERE id = $1`,
		st.ID, st.Slug, st.Name, st.Location, st.RoutingLocation, st.PlatformHeight)
	if err != nil {
		return wrap("UpdateStation", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateStation: no station with id %q", st.ID)
	}
	return nil
}

func (r *Repo) DeleteStation(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM stations WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteStation", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteStation: no station with id %q", id)
	}
	return nil
}

func (r *Repo) CountStationDependents(ctx context.Context, stationID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM service_stops WHERE station_id = $1`, stationID).Scan(&n); err != nil {
		return 0, wrap("CountStationDependents", err)
	}
	return n, nil
}

// --- Vehicle types ---

func (r *Repo) GetVehicleTypeByID(ctx context.Context, id string) (transit.VehicleType, bool, error) {
	var vt transit.VehicleType
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, propulsion, max_speed_kmh, acceleration_ms2, deceleration_ms2,
		        floor_height, dwell_level_s, dwell_step_s
		 FROM vehicle_types WHERE id = $1`, id).
		Scan(&vt.ID, &vt.Name, &vt.Propulsion, &vt.MaxSpeedKMH, &vt.AccelerationMS2,
			&vt.DecelerationMS2, &vt.FloorHeight, &vt.DwellLevelS, &vt.DwellStepS)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.VehicleType{}, false, nil
	}
	if err != nil {
		return transit.VehicleType{}, false, wrap("GetVehicleTypeByID", err)
	}
	return vt, true, nil
}

func (r *Repo) UpdateVehicleType(ctx context.Context, vt transit.VehicleType) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE vehicle_types
		    SET name = $2, propulsion = $3, max_speed_kmh = $4,
		        acceleration_ms2 = $5, deceleration_ms2 = $6,
		        floor_height = $7, dwell_level_s = $8, dwell_step_s = $9
		  WHERE id = $1`,
		vt.ID, vt.Name, vt.Propulsion, vt.MaxSpeedKMH, vt.AccelerationMS2,
		vt.DecelerationMS2, vt.FloorHeight, vt.DwellLevelS, vt.DwellStepS)
	if err != nil {
		return wrap("UpdateVehicleType", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateVehicleType: no vehicle type with id %q", vt.ID)
	}
	return nil
}

// --- Services ---

func (r *Repo) GetServiceByID(ctx context.Context, id string) (transit.Service, bool, error) {
	svcs, err := r.listServicesBy(ctx, "GetServiceByID", "id", id)
	if err != nil {
		return transit.Service{}, false, err
	}
	if len(svcs) == 0 {
		return transit.Service{}, false, nil
	}
	return svcs[0], true, nil
}

func (r *Repo) UpdateService(ctx context.Context, svc transit.Service) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UpdateService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// The boarding-wait override is written here as well as on create (SPA-237),
	// or an owner could set one and never change it again: the handler resolves
	// omitted-vs-null-vs-set before this point, so whatever arrives is what the
	// row should end up holding, nil included.
	kind, secs := boardingWaitArgs(svc.BoardingWait)
	tag, err := tx.Exec(ctx,
		`UPDATE services
		    SET route_id = $2, vehicle_type_id = $3, name = $4, direction = $5,
		        active = $6, boarding_wait_policy = $7, boarding_wait_fixed_secs = $8,
		        updated_at = now()
		  WHERE id = $1`,
		svc.ID, svc.RouteID, svc.VehicleTypeID, svc.Name, svc.Direction, svc.Active,
		kind, secs)
	if err != nil {
		return wrap("UpdateService", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateService: no service with id %q", svc.ID)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM service_stops WHERE service_id = $1`, svc.ID); err != nil {
		return wrap("UpdateService clearing stops", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM frequency_windows WHERE service_id = $1`, svc.ID); err != nil {
		return wrap("UpdateService clearing frequency windows", err)
	}
	if err := insertServiceChildren(ctx, tx, svc); err != nil {
		return err
	}

	return wrap("UpdateService commit", tx.Commit(ctx))
}

func (r *Repo) DeleteService(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM services WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteService", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteService: no service with id %q", id)
	}
	return nil
}

func insertServiceChildren(ctx context.Context, tx pgx.Tx, svc transit.Service) error {
	for _, stop := range svc.Stops {
		if _, err := tx.Exec(ctx,
			`INSERT INTO service_stops (service_id, station_id, sequence, dwell_s)
			 VALUES ($1, $2, $3, $4)`,
			svc.ID, stop.StationID, stop.Sequence, stop.DwellS); err != nil {
			return wrap("inserting service stops", err)
		}
	}

	for _, fw := range svc.FrequencyWindows {
		// The row PK is persistence identity, not domain data: nothing
		// references a frequency window by id, so it is minted here rather
		// than carried on the type.
		fwID, err := ids.NewUUID()
		if err != nil {
			return wrap("minting frequency window id", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO frequency_windows (id, service_id, start_time, end_time, headway_s)
			 VALUES ($1, $2, $3, $4, $5)`,
			fwID, svc.ID, fw.StartTime, fw.EndTime, fw.HeadwayS); err != nil {
			return wrap("inserting service frequency windows", err)
		}
	}
	return nil
}
