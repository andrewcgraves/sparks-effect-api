package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// --- User scenarios (curated membership over user_services) ---

const userScenarioColumns = `id, slug, owner_id, name, description, interchange_pairs, created_at, updated_at`

func (r *Repo) CreateUserScenario(ctx context.Context, sc transit.UserScenario) error {
	pairs, err := marshalInterchangePairs(sc)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("CreateUserScenario begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_scenarios (id, slug, owner_id, name, description, interchange_pairs)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		sc.ID, sc.Slug, sc.OwnerID, sc.Name, sc.Description, pairs); err != nil {
		return wrap("CreateUserScenario", err)
	}
	if err := insertUserScenarioMembership(ctx, tx, sc.ID, sc.ServiceIDs); err != nil {
		return err
	}
	return wrap("CreateUserScenario commit", tx.Commit(ctx))
}

// UpdateUserScenario rewrites scalar fields and replaces the membership set
// wholesale — membership has no client-visible identity to diff against.
func (r *Repo) UpdateUserScenario(ctx context.Context, sc transit.UserScenario) error {
	pairs, err := marshalInterchangePairs(sc)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UpdateUserScenario begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tag, err := tx.Exec(ctx,
		`UPDATE user_scenarios SET name = $2, description = $3, interchange_pairs = $4, updated_at = now() WHERE id = $1`,
		sc.ID, sc.Name, sc.Description, pairs)
	if err != nil {
		return wrap("UpdateUserScenario", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: UpdateUserScenario: no scenario with id %q", sc.ID)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM user_scenario_services WHERE user_scenario_id = $1`, sc.ID); err != nil {
		return wrap("UpdateUserScenario clear membership", err)
	}
	if err := insertUserScenarioMembership(ctx, tx, sc.ID, sc.ServiceIDs); err != nil {
		return err
	}
	return wrap("UpdateUserScenario commit", tx.Commit(ctx))
}

func (r *Repo) DeleteUserScenario(ctx context.Context, id string) error {
	// Membership rows cascade via their FK.
	tag, err := r.pool.Exec(ctx, `DELETE FROM user_scenarios WHERE id = $1`, id)
	if err != nil {
		return wrap("DeleteUserScenario", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: DeleteUserScenario: no scenario with id %q", id)
	}
	return nil
}

func (r *Repo) GetUserScenarioByID(ctx context.Context, id string) (transit.UserScenario, bool, error) {
	return r.getUserScenarioBy(ctx, "GetUserScenarioByID",
		`SELECT `+userScenarioColumns+` FROM user_scenarios WHERE id = $1`, id)
}

func (r *Repo) GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error) {
	return r.getUserScenarioBy(ctx, "GetUserScenarioBySlug",
		`SELECT `+userScenarioColumns+` FROM user_scenarios WHERE slug = $1`, slug)
}

func (r *Repo) getUserScenarioBy(ctx context.Context, op, query, arg string) (transit.UserScenario, bool, error) {
	sc, err := scanUserScenario(r.pool.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.UserScenario{}, false, nil
	}
	if err != nil {
		return transit.UserScenario{}, false, wrap(op, err)
	}

	ids, err := r.listUserScenarioServiceIDs(ctx, sc.ID)
	if err != nil {
		return transit.UserScenario{}, false, err
	}
	sc.ServiceIDs = ids
	return sc, true, nil
}

func (r *Repo) ListUserScenariosByOwner(ctx context.Context, ownerID string) ([]transit.UserScenario, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+userScenarioColumns+` FROM user_scenarios
		 WHERE owner_id = $1 ORDER BY created_at, id`, ownerID)
	if err != nil {
		return nil, wrap("ListUserScenariosByOwner", err)
	}
	defer rows.Close()

	out := []transit.UserScenario{}
	for rows.Next() {
		sc, err := scanUserScenario(rows)
		if err != nil {
			return nil, wrap("ListUserScenariosByOwner scan", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("ListUserScenariosByOwner rows", err)
	}

	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	memberships, err := r.serviceIDsByScenario(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ServiceIDs = memberships[out[i].ID]
	}
	return out, nil
}

// UserServiceIDsOwnedBy reports which of ids are user_services rows owned by
// ownerID, in one round trip — the bulk check a scenario write validates
// membership against before it persists anything.
func (r *Repo) UserServiceIDsOwnedBy(ctx context.Context, ownerID string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	// id::text = ANY($1): ids may include a caller-supplied value that is not
	// a well-formed uuid at all (an unknown or malicious service_id), which
	// would otherwise fail the query with a cast error instead of simply not
	// matching.
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM user_services WHERE id::text = ANY($1) AND owner_id = $2`, ids, ownerID)
	if err != nil {
		return nil, wrap("UserServiceIDsOwnedBy", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap("UserServiceIDsOwnedBy scan", err)
		}
		out[id] = true
	}
	return out, wrap("UserServiceIDsOwnedBy rows", rows.Err())
}

// serviceIDsByScenario reads membership for many scenarios in one query, so
// listing N scenarios costs two round trips rather than N+1.
func (r *Repo) serviceIDsByScenario(ctx context.Context, scenarioIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(scenarioIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT user_scenario_id, user_service_id FROM user_scenario_services
		  WHERE user_scenario_id = ANY($1)
		  ORDER BY user_scenario_id, user_service_id`, scenarioIDs)
	if err != nil {
		return nil, wrap("serviceIDsByScenario", err)
	}
	defer rows.Close()

	for rows.Next() {
		var scenarioID, serviceID string
		if err := rows.Scan(&scenarioID, &serviceID); err != nil {
			return nil, wrap("serviceIDsByScenario scan", err)
		}
		out[scenarioID] = append(out[scenarioID], serviceID)
	}
	return out, wrap("serviceIDsByScenario rows", rows.Err())
}

func (r *Repo) listUserScenarioServiceIDs(ctx context.Context, scenarioID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_service_id FROM user_scenario_services
		 WHERE user_scenario_id = $1 ORDER BY user_service_id`, scenarioID)
	if err != nil {
		return nil, wrap("listUserScenarioServiceIDs", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap("listUserScenarioServiceIDs scan", err)
		}
		out = append(out, id)
	}
	return out, wrap("listUserScenarioServiceIDs rows", rows.Err())
}

func insertUserScenarioMembership(ctx context.Context, tx pgx.Tx, scenarioID string, serviceIDs []string) error {
	for _, serviceID := range serviceIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_scenario_services (user_scenario_id, user_service_id) VALUES ($1, $2)`,
			scenarioID, serviceID); err != nil {
			return wrap("insertUserScenarioMembership", err)
		}
	}
	return nil
}

// scanUserScenario reads one user_scenarios row in userScenarioColumns order.
// pgx.Row is satisfied by both QueryRow results and pgx.Rows, so the single
// and list read paths share it.
func scanUserScenario(row pgx.Row) (transit.UserScenario, error) {
	var (
		sc    transit.UserScenario
		pairs []byte
	)
	if err := row.Scan(&sc.ID, &sc.Slug, &sc.OwnerID, &sc.Name, &sc.Description, &pairs, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
		return transit.UserScenario{}, err
	}
	if err := json.Unmarshal(pairs, &sc.InterchangePairs); err != nil {
		return transit.UserScenario{}, fmt.Errorf("decoding interchange pairs for scenario %q: %w", sc.ID, err)
	}
	return sc, nil
}

// marshalInterchangePairs marshals a scenario's declared interchange pairs,
// normalising a nil slice to [] so an empty declaration round-trips as an
// empty JSON array rather than null.
func marshalInterchangePairs(sc transit.UserScenario) ([]byte, error) {
	pairs := sc.InterchangePairs
	if pairs == nil {
		pairs = []transit.InterchangePair{}
	}
	b, err := json.Marshal(pairs)
	if err != nil {
		return nil, wrap("marshal interchange pairs", err)
	}
	return b, nil
}
