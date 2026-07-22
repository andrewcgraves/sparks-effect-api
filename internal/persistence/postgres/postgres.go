// Package postgres provides a Postgres-backed implementation of
// transit.Repository, plus schema migrations and connection helpers. It uses
// pgx/v5 (pure Go, so CGO_ENABLED=0 static builds are preserved) and stores
// geometry as GeoJSON in jsonb columns. Native Postgres types are used
// throughout — this repository exists for testability, not engine-swapping.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Repo is a Postgres-backed transit.Repository over a pgx connection pool.
type Repo struct {
	pool *pgxpool.Pool
}

// compile-time assertion that Repo satisfies the storage-agnostic seam.
var _ transit.Repository = (*Repo)(nil)

// Connect opens a pgx connection pool against databaseURL. If maxConns > 0 it
// overrides the pool's max connection count.
func Connect(ctx context.Context, databaseURL string, maxConns int) (*Repo, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parsing DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: creating pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &Repo{pool: pool}, nil
}

// Close releases the connection pool.
func (r *Repo) Close() { r.pool.Close() }

// Migrate runs all pending goose migrations against databaseURL. It opens a
// short-lived database/sql handle via the pgx stdlib driver (goose speaks
// database/sql) and closes it before returning; the app itself uses the pgx
// pool. Safe to run on every boot — already-applied migrations are skipped.
func Migrate(ctx context.Context, databaseURL string) error {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("postgres: parsing DATABASE_URL for migrations: %w", err)
	}
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("postgres: setting goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("postgres: running migrations: %w", err)
	}
	return nil
}

// --- Scenarios ---

func (r *Repo) CreateScenario(ctx context.Context, sc transit.Scenario) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO scenarios (id, slug, name, description, status, owner_id)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		sc.ID, sc.Slug, sc.Name, sc.Description, sc.Status, sc.OwnerID)
	return wrap("CreateScenario", err)
}

func (r *Repo) GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error) {
	var sc transit.Scenario
	err := r.pool.QueryRow(ctx,
		`SELECT id, slug, name, description, status, owner_id FROM scenarios WHERE slug = $1`,
		slug).Scan(&sc.ID, &sc.Slug, &sc.Name, &sc.Description, &sc.Status, &sc.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.Scenario{}, false, nil
	}
	if err != nil {
		return transit.Scenario{}, false, wrap("GetScenarioBySlug", err)
	}
	return sc, true, nil
}

func (r *Repo) ListScenarios(ctx context.Context) ([]transit.Scenario, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scenarioColumns+` FROM scenarios ORDER BY slug`)
	if err != nil {
		return nil, wrap("ListScenarios", err)
	}
	return scanScenarios(rows, "ListScenarios")
}

// ListScenariosByOwner backs "my scenarios". As with services, ownership is
// enforced in SQL so unowned rows are never loaded.
func (r *Repo) ListScenariosByOwner(ctx context.Context, ownerID string) ([]transit.Scenario, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scenarioColumns+` FROM scenarios WHERE owner_id = $1 ORDER BY slug`, ownerID)
	if err != nil {
		return nil, wrap("ListScenariosByOwner", err)
	}
	return scanScenarios(rows, "ListScenariosByOwner")
}

const scenarioColumns = `id, slug, name, description, status, owner_id`

func scanScenarios(rows pgx.Rows, op string) ([]transit.Scenario, error) {
	defer rows.Close()

	var out []transit.Scenario
	for rows.Next() {
		var sc transit.Scenario
		if err := rows.Scan(&sc.ID, &sc.Slug, &sc.Name, &sc.Description, &sc.Status, &sc.OwnerID); err != nil {
			return nil, wrap(op+" scan", err)
		}
		out = append(out, sc)
	}
	return out, wrap(op+" rows", rows.Err())
}

// --- Routes ---

const routeColumns = `id, scenario_id, slug, name, mode, geometry, bidirectional, segments`

func (r *Repo) CreateRoute(ctx context.Context, rt transit.Route) error {
	// segments is NOT NULL, so a route with no authored physics stores an empty
	// array rather than NULL — the two would otherwise read back differently.
	segments := rt.Segments
	if segments == nil {
		segments = []transit.RouteSegment{}
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO routes (id, scenario_id, slug, name, mode, geometry, bidirectional, segments)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rt.ID, rt.ScenarioID, rt.Slug, rt.Name, rt.Mode, rt.Geometry, rt.Bidirectional, segments)
	return wrap("CreateRoute", err)
}

// GetRouteBySlug reads a single route by its globally unique slug, which is how
// an ingested route is addressed — it need not belong to any scenario.
func (r *Repo) GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+routeColumns+` FROM routes WHERE slug = $1`, slug)

	rt, err := scanRoute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.Route{}, false, nil
	}
	if err != nil {
		return transit.Route{}, false, wrap("GetRouteBySlug", err)
	}
	return rt, true, nil
}

// ListRouteSummaries returns every route reduced to the fields needed to choose
// one. The projection is done in SQL rather than after the fact: geometry and
// segments are the bulk of a route row and no caller of this list wants them.
// Ordered by slug so the list a client renders is stable between calls.
func (r *Repo) ListRouteSummaries(ctx context.Context) ([]transit.RouteSummary, error) {
	rows, err := r.pool.Query(ctx, `SELECT slug, name, mode FROM routes ORDER BY slug`)
	if err != nil {
		return nil, wrap("ListRouteSummaries", err)
	}
	defer rows.Close()

	var out []transit.RouteSummary
	for rows.Next() {
		var rs transit.RouteSummary
		if err := rows.Scan(&rs.Slug, &rs.Name, &rs.Mode); err != nil {
			return nil, wrap("ListRouteSummaries scan", err)
		}
		out = append(out, rs)
	}
	return out, wrap("ListRouteSummaries rows", rows.Err())
}

func (r *Repo) ListRoutesByScenario(ctx context.Context, scenarioID string) ([]transit.Route, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+routeColumns+` FROM routes WHERE scenario_id = $1 ORDER BY id`, scenarioID)
	if err != nil {
		return nil, wrap("ListRoutesByScenario", err)
	}
	defer rows.Close()

	var out []transit.Route
	for rows.Next() {
		rt, err := scanRoute(rows)
		if err != nil {
			return nil, wrap("ListRoutesByScenario scan", err)
		}
		out = append(out, rt)
	}
	return out, wrap("ListRoutesByScenario rows", rows.Err())
}

// scanRoute reads one row of routeColumns. It takes a pgx.Row so both the
// single-row and multi-row readers share one column order.
func scanRoute(row pgx.Row) (transit.Route, error) {
	var rt transit.Route
	err := row.Scan(&rt.ID, &rt.ScenarioID, &rt.Slug, &rt.Name, &rt.Mode,
		&rt.Geometry, &rt.Bidirectional, &rt.Segments)
	return rt, err
}

// --- Stations ---

func (r *Repo) CreateStation(ctx context.Context, st transit.Station) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO stations (id, scenario_id, slug, name, location, platform_height)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		st.ID, st.ScenarioID, st.Slug, st.Name, st.Location, st.PlatformHeight)
	return wrap("CreateStation", err)
}

func (r *Repo) ListStationsByScenario(ctx context.Context, scenarioID string) ([]transit.Station, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, scenario_id, slug, name, location, platform_height
		 FROM stations WHERE scenario_id = $1 ORDER BY slug`, scenarioID)
	if err != nil {
		return nil, wrap("ListStationsByScenario", err)
	}
	defer rows.Close()

	var out []transit.Station
	for rows.Next() {
		var st transit.Station
		if err := rows.Scan(&st.ID, &st.ScenarioID, &st.Slug, &st.Name, &st.Location, &st.PlatformHeight); err != nil {
			return nil, wrap("ListStationsByScenario scan", err)
		}
		out = append(out, st)
	}
	return out, wrap("ListStationsByScenario rows", rows.Err())
}

// --- Vehicle types ---

func (r *Repo) CreateVehicleType(ctx context.Context, vt transit.VehicleType) error {
	// Idempotent on ID: the same vehicle type may be seeded by multiple scenarios.
	_, err := r.pool.Exec(ctx,
		`INSERT INTO vehicle_types
		   (id, name, propulsion, max_speed_kmh, acceleration_ms2, deceleration_ms2,
		    floor_height, dwell_level_s, dwell_step_s)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (id) DO NOTHING`,
		vt.ID, vt.Name, vt.Propulsion, vt.MaxSpeedKMH, vt.AccelerationMS2,
		vt.DecelerationMS2, vt.FloorHeight, vt.DwellLevelS, vt.DwellStepS)
	return wrap("CreateVehicleType", err)
}

func (r *Repo) ListVehicleTypes(ctx context.Context) ([]transit.VehicleType, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, propulsion, max_speed_kmh, acceleration_ms2, deceleration_ms2,
		        floor_height, dwell_level_s, dwell_step_s
		 FROM vehicle_types ORDER BY id`)
	if err != nil {
		return nil, wrap("ListVehicleTypes", err)
	}
	defer rows.Close()

	var out []transit.VehicleType
	for rows.Next() {
		var vt transit.VehicleType
		if err := rows.Scan(&vt.ID, &vt.Name, &vt.Propulsion, &vt.MaxSpeedKMH,
			&vt.AccelerationMS2, &vt.DecelerationMS2, &vt.FloorHeight,
			&vt.DwellLevelS, &vt.DwellStepS); err != nil {
			return nil, wrap("ListVehicleTypes scan", err)
		}
		out = append(out, vt)
	}
	return out, wrap("ListVehicleTypes rows", rows.Err())
}

// --- Services (with embedded stops and frequency windows) ---

func (r *Repo) CreateService(ctx context.Context, svc transit.Service) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("CreateService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	_, err = tx.Exec(ctx,
		`INSERT INTO services
		   (id, scenario_id, route_id, vehicle_type_id, name, direction, active, provenance, owner_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		svc.ID, svc.ScenarioID, svc.RouteID, svc.VehicleTypeID, svc.Name,
		svc.Direction, svc.Active, svc.Provenance, svc.OwnerID)
	if err != nil {
		return wrap("CreateService", err)
	}

	for _, stop := range svc.Stops {
		if _, err := tx.Exec(ctx,
			`INSERT INTO service_stops (service_id, station_id, sequence, dwell_s)
			 VALUES ($1, $2, $3, $4)`,
			svc.ID, stop.StationID, stop.Sequence, stop.DwellS); err != nil {
			return wrap("CreateService stops", err)
		}
	}

	for _, fw := range svc.FrequencyWindows {
		// The row PK is persistence identity, not domain data: nothing
		// references a frequency window by id, so it is minted here rather
		// than carried on the type.
		fwID, err := ids.NewUUID()
		if err != nil {
			return wrap("CreateService frequency window id", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO frequency_windows (id, service_id, start_time, end_time, headway_s)
			 VALUES ($1, $2, $3, $4, $5)`,
			fwID, svc.ID, fw.StartTime, fw.EndTime, fw.HeadwayS); err != nil {
			return wrap("CreateService frequency windows", err)
		}
	}

	return wrap("CreateService commit", tx.Commit(ctx))
}

func (r *Repo) ListServicesByScenario(ctx context.Context, scenarioID string) ([]transit.Service, error) {
	return r.listServicesBy(ctx, "ListServicesByScenario", "scenario_id", scenarioID)
}

// ListServicesByOwner backs "my services". Ownership is a WHERE clause, not a
// post-query filter, so services the caller does not own never leave the
// database.
func (r *Repo) ListServicesByOwner(ctx context.Context, ownerID string) ([]transit.Service, error) {
	return r.listServicesBy(ctx, "ListServicesByOwner", "owner_id", ownerID)
}

// listServicesBy loads services matching a single equality predicate and
// hydrates each one's embedded stops and frequency windows. column is a
// trusted internal identifier, never caller input.
func (r *Repo) listServicesBy(ctx context.Context, op, column, value string) ([]transit.Service, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, scenario_id, route_id, vehicle_type_id, name, direction, active, provenance, owner_id
		 FROM services WHERE `+column+` = $1 ORDER BY id`, value)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()

	var out []transit.Service
	for rows.Next() {
		var svc transit.Service
		if err := rows.Scan(&svc.ID, &svc.ScenarioID, &svc.RouteID, &svc.VehicleTypeID,
			&svc.Name, &svc.Direction, &svc.Active, &svc.Provenance, &svc.OwnerID); err != nil {
			return nil, wrap(op+" scan", err)
		}
		out = append(out, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(op+" rows", err)
	}

	for i := range out {
		stops, err := r.listServiceStops(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Stops = stops

		windows, err := r.listFrequencyWindows(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].FrequencyWindows = windows
	}
	return out, nil
}

func (r *Repo) listServiceStops(ctx context.Context, serviceID string) ([]transit.ServiceStop, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT station_id, sequence, dwell_s FROM service_stops
		 WHERE service_id = $1 ORDER BY sequence`, serviceID)
	if err != nil {
		return nil, wrap("listServiceStops", err)
	}
	defer rows.Close()

	var out []transit.ServiceStop
	for rows.Next() {
		var s transit.ServiceStop
		if err := rows.Scan(&s.StationID, &s.Sequence, &s.DwellS); err != nil {
			return nil, wrap("listServiceStops scan", err)
		}
		out = append(out, s)
	}
	return out, wrap("listServiceStops rows", rows.Err())
}

func (r *Repo) listFrequencyWindows(ctx context.Context, serviceID string) ([]transit.FrequencyWindow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT start_time, end_time, headway_s FROM frequency_windows
		 WHERE service_id = $1 ORDER BY start_time`, serviceID)
	if err != nil {
		return nil, wrap("listFrequencyWindows", err)
	}
	defer rows.Close()

	var out []transit.FrequencyWindow
	for rows.Next() {
		var fw transit.FrequencyWindow
		if err := rows.Scan(&fw.StartTime, &fw.EndTime, &fw.HeadwayS); err != nil {
			return nil, wrap("listFrequencyWindows scan", err)
		}
		out = append(out, fw)
	}
	return out, wrap("listFrequencyWindows rows", rows.Err())
}

// --- Scenario/service curated membership ---

func (r *Repo) AddServiceToScenario(ctx context.Context, scenarioID, serviceID string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO scenario_service (scenario_id, service_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, scenarioID, serviceID)
	return wrap("AddServiceToScenario", err)
}

func (r *Repo) ListServiceIDsByScenario(ctx context.Context, scenarioID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT service_id FROM scenario_service WHERE scenario_id = $1 ORDER BY service_id`, scenarioID)
	if err != nil {
		return nil, wrap("ListServiceIDsByScenario", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap("ListServiceIDsByScenario scan", err)
		}
		out = append(out, id)
	}
	return out, wrap("ListServiceIDsByScenario rows", rows.Err())
}

// --- Travel times (adjacent segment run times) ---

func (r *Repo) UpsertTravelTimes(ctx context.Context, tt transit.TravelTimes) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UpsertTravelTimes begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var scenarioID string
	err = tx.QueryRow(ctx, `SELECT id FROM scenarios WHERE slug = $1`, tt.ScenarioSlug).Scan(&scenarioID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: UpsertTravelTimes: unknown scenario slug %q", tt.ScenarioSlug)
	}
	if err != nil {
		return wrap("UpsertTravelTimes resolve scenario", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO travel_time_sets (scenario_id, provenance, source)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (scenario_id) DO UPDATE SET provenance = EXCLUDED.provenance, source = EXCLUDED.source`,
		scenarioID, tt.Provenance, tt.Source); err != nil {
		return wrap("UpsertTravelTimes set", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM segments WHERE scenario_id = $1`, scenarioID); err != nil {
		return wrap("UpsertTravelTimes clear segments", err)
	}
	for _, seg := range tt.Segments {
		if _, err := tx.Exec(ctx,
			`INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds)
			 VALUES ($1, $2, $3, $4)`,
			scenarioID, seg.FromSlug, seg.ToSlug, seg.RunSeconds); err != nil {
			return wrap("UpsertTravelTimes insert segment", err)
		}
	}

	return wrap("UpsertTravelTimes commit", tx.Commit(ctx))
}

func (r *Repo) GetTravelTimes(ctx context.Context, scenarioSlug string) (transit.TravelTimes, bool, error) {
	var scenarioID string
	err := r.pool.QueryRow(ctx, `SELECT id FROM scenarios WHERE slug = $1`, scenarioSlug).Scan(&scenarioID)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.TravelTimes{}, false, nil
	}
	if err != nil {
		return transit.TravelTimes{}, false, wrap("GetTravelTimes resolve scenario", err)
	}

	tt := transit.TravelTimes{ScenarioSlug: scenarioSlug}
	err = r.pool.QueryRow(ctx,
		`SELECT provenance, source FROM travel_time_sets WHERE scenario_id = $1`, scenarioID).
		Scan(&tt.Provenance, &tt.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.TravelTimes{}, false, nil
	}
	if err != nil {
		return transit.TravelTimes{}, false, wrap("GetTravelTimes set", err)
	}

	rows, err := r.pool.Query(ctx,
		`SELECT from_slug, to_slug, run_seconds FROM segments WHERE scenario_id = $1 ORDER BY id`, scenarioID)
	if err != nil {
		return transit.TravelTimes{}, false, wrap("GetTravelTimes segments", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seg transit.SegmentTime
		if err := rows.Scan(&seg.FromSlug, &seg.ToSlug, &seg.RunSeconds); err != nil {
			return transit.TravelTimes{}, false, wrap("GetTravelTimes segments scan", err)
		}
		tt.Segments = append(tt.Segments, seg)
	}
	if err := rows.Err(); err != nil {
		return transit.TravelTimes{}, false, wrap("GetTravelTimes segments rows", err)
	}
	return tt, true, nil
}

// --- Users ---

func (r *Repo) CreateUser(ctx context.Context, u transit.User, passwordHash string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users (id, email, name, is_admin, password_hash) VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.Email, u.Name, u.IsAdmin, passwordHash)
	return wrap("CreateUser", err)
}

// GetUserCredentialsByEmail returns the user together with their stored
// password hash. Only the login handler should call it.
func (r *Repo) GetUserCredentialsByEmail(ctx context.Context, email string) (transit.User, string, bool, error) {
	var u transit.User
	var hash string
	err := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+`, password_hash FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.User{}, "", false, nil
	}
	if err != nil {
		return transit.User{}, "", false, wrap("GetUserCredentialsByEmail", err)
	}
	return u, hash, true, nil
}

const userColumns = `id, email, name, is_admin, created_at, updated_at`

// userColumnsU is the same list qualified for joins against sessions, where a
// bare `id` would be ambiguous. Kept in step with userColumns by hand — both
// feed the same scanUser, so a mismatch fails loudly at the first query.
const userColumnsU = `u.id, u.email, u.name, u.is_admin, u.created_at, u.updated_at`

func (r *Repo) GetUserByID(ctx context.Context, id string) (transit.User, bool, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

func (r *Repo) GetUserByEmail(ctx context.Context, email string) (transit.User, bool, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func scanUser(row pgx.Row) (transit.User, bool, error) {
	var u transit.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.User{}, false, nil
	}
	if err != nil {
		return transit.User{}, false, wrap("scanUser", err)
	}
	return u, true, nil
}

func (r *Repo) ListUsers(ctx context.Context) ([]transit.User, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY email`)
	if err != nil {
		return nil, wrap("ListUsers", err)
	}
	defer rows.Close()

	var out []transit.User
	for rows.Next() {
		var u transit.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, wrap("ListUsers scan", err)
		}
		out = append(out, u)
	}
	return out, wrap("ListUsers rows", rows.Err())
}

// --- Sessions ---

func (r *Repo) CreateSession(ctx context.Context, s transit.Session) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		s.TokenHash, s.UserID, s.ExpiresAt)
	return wrap("CreateSession", err)
}

// GetSessionUser resolves a token hash to the user it authenticates. Expiry is
// part of the WHERE clause rather than a follow-up check in Go, so an expired
// session is indistinguishable from a missing one and no caller can skip the
// comparison.
func (r *Repo) GetSessionUser(ctx context.Context, tokenHash string) (transit.User, bool, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumnsU+`
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash))
}

func (r *Repo) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return wrap("DeleteSession", err)
}

func (r *Repo) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, wrap("DeleteExpiredSessions", err)
	}
	return tag.RowsAffected(), nil
}

// --- Jobs ---

func (r *Repo) CreateJob(ctx context.Context, j transit.Job) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO jobs (id, kind, status, scenario_id, owner_id, error)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		j.ID, j.Kind, j.Status, j.ScenarioID, j.OwnerID, j.Error)
	return wrap("CreateJob", err)
}

const jobColumns = `id, kind, status, scenario_id, owner_id, error, result, created_at, updated_at`

func (r *Repo) GetJobByID(ctx context.Context, id string) (transit.Job, bool, error) {
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id))
}

func (r *Repo) UpdateJobStatus(ctx context.Context, id, status, errMsg string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET status = $2, error = $3, updated_at = now() WHERE id = $1`,
		id, status, errMsg)
	if err != nil {
		return wrap("UpdateJobStatus", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateJobStatus: job %q not found", id)
	}
	return nil
}

// CompleteJob marks a job succeeded and stores its compiled result in one
// write, so a poller never observes "succeeded" with no result yet to read.
func (r *Repo) CompleteJob(ctx context.Context, id string, result transit.TransitGraph) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET status = $2, error = '', result = $3, updated_at = now() WHERE id = $1`,
		id, transit.JobStatusSucceeded, result)
	if err != nil {
		return wrap("CompleteJob", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: CompleteJob: job %q not found", id)
	}
	return nil
}

func (r *Repo) ListJobs(ctx context.Context) ([]transit.Job, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, wrap("ListJobs", err)
	}
	defer rows.Close()

	var out []transit.Job
	for rows.Next() {
		j, err := scanJobRow(rows)
		if err != nil {
			return nil, wrap("ListJobs scan", err)
		}
		out = append(out, j)
	}
	return out, wrap("ListJobs rows", rows.Err())
}

// GetLatestSucceededJob is the "result, retrievable by slug" read: it joins
// through to scenarios by slug rather than requiring the caller to know a
// scenario id, matching GetTravelTimes's slug-addressed convention.
func (r *Repo) GetLatestSucceededJob(ctx context.Context, scenarioSlug, kind string) (transit.Job, bool, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT j.id, j.kind, j.status, j.scenario_id, j.owner_id, j.error, j.result, j.created_at, j.updated_at
		 FROM jobs j JOIN scenarios s ON s.id = j.scenario_id
		 WHERE s.slug = $1 AND j.kind = $2 AND j.status = $3
		 ORDER BY j.created_at DESC LIMIT 1`,
		scenarioSlug, kind, transit.JobStatusSucceeded)
	return scanJob(row)
}

// scanJob reads one jobColumns row, translating "no such row" into ok=false
// rather than an error.
func scanJob(row pgx.Row) (transit.Job, bool, error) {
	var j transit.Job
	err := row.Scan(&j.ID, &j.Kind, &j.Status, &j.ScenarioID, &j.OwnerID, &j.Error, &j.Result, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.Job{}, false, nil
	}
	if err != nil {
		return transit.Job{}, false, wrap("scanJob", err)
	}
	return j, true, nil
}

// scanJobRow is scanJob's pgx.Rows counterpart, for the multi-row ListJobs
// reader.
func scanJobRow(rows pgx.Rows) (transit.Job, error) {
	var j transit.Job
	err := rows.Scan(&j.ID, &j.Kind, &j.Status, &j.ScenarioID, &j.OwnerID, &j.Error, &j.Result, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("postgres: %s: %w", op, err)
}
