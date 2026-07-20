package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// --- User services (embedded stops, inline vehicle params) ---

const userServiceColumns = `id, slug, route_id, owner_id, name, description,
	vehicle, stops, created_at, updated_at`

func (r *Repo) CreateUserService(ctx context.Context, svc transit.UserService) error {
	vehicle, stops, err := marshalUserServiceDocs(svc)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("CreateUserService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_services
		   (id, slug, route_id, owner_id, name, description, vehicle, stops)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		svc.ID, svc.Slug, svc.RouteID, svc.OwnerID, svc.Name, svc.Description,
		vehicle, stops); err != nil {
		return wrap("CreateUserService", err)
	}

	if err := insertFrequencyWindows(ctx, tx, svc.ID, svc.FrequencyWindows); err != nil {
		return err
	}
	return wrap("CreateUserService commit", tx.Commit(ctx))
}

// UpdateUserService rewrites the whole aggregate — scalar fields, the embedded
// stop pattern, and the frequency windows — in one transaction. Windows are
// replaced rather than diffed: they have no identity a client can address.
func (r *Repo) UpdateUserService(ctx context.Context, svc transit.UserService) error {
	vehicle, stops, err := marshalUserServiceDocs(svc)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UpdateUserService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tag, err := tx.Exec(ctx,
		`UPDATE user_services
		    SET route_id = $2, name = $3, description = $4, vehicle = $5,
		        stops = $6, updated_at = now()
		  WHERE id = $1`,
		svc.ID, svc.RouteID, svc.Name, svc.Description, vehicle, stops)
	if err != nil {
		return wrap("UpdateUserService", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateUserService: no service with id %q", svc.ID)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM user_service_frequency_windows WHERE user_service_id = $1`,
		svc.ID); err != nil {
		return wrap("UpdateUserService clear frequency windows", err)
	}
	if err := insertFrequencyWindows(ctx, tx, svc.ID, svc.FrequencyWindows); err != nil {
		return err
	}
	return wrap("UpdateUserService commit", tx.Commit(ctx))
}

func (r *Repo) DeleteUserService(ctx context.Context, id string) error {
	// Frequency windows cascade via their FK.
	tag, err := r.pool.Exec(ctx, `DELETE FROM user_services WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteUserService", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteUserService: no service with id %q", id)
	}
	return nil
}

func (r *Repo) GetUserServiceByID(ctx context.Context, id string) (transit.UserService, bool, error) {
	return r.getUserServiceBy(ctx, "GetUserServiceByID",
		`SELECT `+userServiceColumns+` FROM user_services WHERE id = $1`, id)
}

func (r *Repo) GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error) {
	return r.getUserServiceBy(ctx, "GetUserServiceBySlug",
		`SELECT `+userServiceColumns+` FROM user_services WHERE slug = $1`, slug)
}

func (r *Repo) getUserServiceBy(ctx context.Context, op, query, arg string) (transit.UserService, bool, error) {
	svc, err := scanUserService(r.pool.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.UserService{}, false, nil
	}
	if err != nil {
		return transit.UserService{}, false, wrap(op, err)
	}

	windows, err := r.listUserServiceFrequencyWindows(ctx, svc.ID)
	if err != nil {
		return transit.UserService{}, false, err
	}
	svc.FrequencyWindows = windows
	return svc, true, nil
}

func (r *Repo) ListUserServicesByOwner(ctx context.Context, ownerID string) ([]transit.UserService, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userServiceColumns+` FROM user_services
		 WHERE owner_id = $1 ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, wrap("ListUserServicesByOwner", err)
	}
	defer rows.Close()

	out := []transit.UserService{}
	for rows.Next() {
		svc, err := scanUserService(rows)
		if err != nil {
			return nil, wrap("ListUserServicesByOwner scan", err)
		}
		out = append(out, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("ListUserServicesByOwner rows", err)
	}

	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	windows, err := r.frequencyWindowsByService(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].FrequencyWindows = windows[out[i].ID]
	}
	return out, nil
}

// frequencyWindowsByService reads the windows for many services in one query,
// so listing N services costs two round trips rather than N+1.
func (r *Repo) frequencyWindowsByService(ctx context.Context, serviceIDs []string) (map[string][]transit.ServiceFrequencyWindow, error) {
	out := map[string][]transit.ServiceFrequencyWindow{}
	if len(serviceIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT user_service_id, start_time, end_time, headway_s
		   FROM user_service_frequency_windows
		  WHERE user_service_id = ANY($1)
		  ORDER BY user_service_id, seq`, serviceIDs)
	if err != nil {
		return nil, wrap("frequencyWindowsByService", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id string
			fw transit.ServiceFrequencyWindow
		)
		if err := rows.Scan(&id, &fw.StartTime, &fw.EndTime, &fw.HeadwayS); err != nil {
			return nil, wrap("frequencyWindowsByService scan", err)
		}
		out[id] = append(out[id], fw)
	}
	return out, wrap("frequencyWindowsByService rows", rows.Err())
}

// scanUserService reads one user_services row in userServiceColumns order.
// pgx.Row is satisfied by both QueryRow results and pgx.Rows, so the single
// and list read paths share it.
func scanUserService(row pgx.Row) (transit.UserService, error) {
	var (
		svc            transit.UserService
		vehicle, stops []byte
	)
	if err := row.Scan(&svc.ID, &svc.Slug, &svc.RouteID, &svc.OwnerID, &svc.Name,
		&svc.Description, &vehicle, &stops, &svc.CreatedAt, &svc.UpdatedAt); err != nil {
		return transit.UserService{}, err
	}
	if err := unmarshalUserServiceDocs(&svc, vehicle, stops); err != nil {
		return transit.UserService{}, err
	}
	return svc, nil
}

// RouteExists reports whether a route is present, so a service can be rejected
// with 422 rather than surfacing a raw foreign-key violation.
func (r *Repo) RouteExists(ctx context.Context, routeID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM routes WHERE id = $1)`, routeID).Scan(&exists)
	return exists, wrap("RouteExists", err)
}

func (r *Repo) listUserServiceFrequencyWindows(ctx context.Context, serviceID string) ([]transit.ServiceFrequencyWindow, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT start_time, end_time, headway_s FROM user_service_frequency_windows
		 WHERE user_service_id = $1 ORDER BY seq`, serviceID)
	if err != nil {
		return nil, wrap("listUserServiceFrequencyWindows", err)
	}
	defer rows.Close()

	out := []transit.ServiceFrequencyWindow{}
	for rows.Next() {
		var fw transit.ServiceFrequencyWindow
		if err := rows.Scan(&fw.StartTime, &fw.EndTime, &fw.HeadwayS); err != nil {
			return nil, wrap("listUserServiceFrequencyWindows scan", err)
		}
		out = append(out, fw)
	}
	return out, wrap("listUserServiceFrequencyWindows rows", rows.Err())
}

// insertFrequencyWindows writes windows in slice order, persisting that order
// as seq so reads return them as the author arranged them.
func insertFrequencyWindows(ctx context.Context, tx pgx.Tx, serviceID string, windows []transit.ServiceFrequencyWindow) error {
	for i, fw := range windows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_service_frequency_windows
			   (user_service_id, seq, start_time, end_time, headway_s)
			 VALUES ($1, $2, $3, $4, $5)`,
			serviceID, i, fw.StartTime, fw.EndTime, fw.HeadwayS); err != nil {
			return wrap("insertFrequencyWindows", err)
		}
	}
	return nil
}

func marshalUserServiceDocs(svc transit.UserService) (vehicle, stops []byte, err error) {
	if vehicle, err = json.Marshal(svc.Vehicle); err != nil {
		return nil, nil, wrap("marshal vehicle params", err)
	}
	// Marshal a non-nil slice so an empty pattern round-trips as [] not null.
	pattern := svc.Stops
	if pattern == nil {
		pattern = []transit.ServiceStopPoint{}
	}
	if stops, err = json.Marshal(pattern); err != nil {
		return nil, nil, wrap("marshal stops", err)
	}
	return vehicle, stops, nil
}

func unmarshalUserServiceDocs(svc *transit.UserService, vehicle, stops []byte) error {
	if err := json.Unmarshal(vehicle, &svc.Vehicle); err != nil {
		return fmt.Errorf("decoding vehicle params for service %q: %w", svc.ID, err)
	}
	if err := json.Unmarshal(stops, &svc.Stops); err != nil {
		return fmt.Errorf("decoding stops for service %q: %w", svc.ID, err)
	}
	return nil
}
