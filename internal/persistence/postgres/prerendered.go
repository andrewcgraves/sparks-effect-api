package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// --- Scenario service membership ---

// ListServiceMembershipByScenario returns the scenario's curated members and
// when each last changed, the two inputs transit.MembershipStale compares.
//
// It reads the same scenario_service join ListServiceIDsByScenario does — the
// curated membership, not services.scenario_id — so "what this scenario
// exposes" means one thing across the codebase. updated_at comes from the
// services rows themselves, which is where the only timestamp a member has
// lives.
func (r *Repo) ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]transit.ServiceMembership, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT s.id, s.updated_at
		 FROM scenario_service ss
		 JOIN services s ON s.id = ss.service_id
		 WHERE ss.scenario_id = $1
		 ORDER BY s.id`, scenarioID)
	if err != nil {
		return nil, wrap("ListServiceMembershipByScenario", err)
	}
	defer rows.Close()

	out := []transit.ServiceMembership{}
	for rows.Next() {
		var m transit.ServiceMembership
		if err := rows.Scan(&m.ServiceID, &m.UpdatedAt); err != nil {
			return nil, wrap("ListServiceMembershipByScenario scan", err)
		}
		out = append(out, m)
	}
	return out, wrap("ListServiceMembershipByScenario rows", rows.Err())
}

// --- Prerendered isochrones ---

// prerenderedMetaColumns is every column of a prerendered isochrone except
// result. It is the whole of the list read, and the reason the list read
// exists as its own query: a payload is 300-500KB, so selecting it to build a
// list of labels would move megabytes the response then discards.
const prerenderedMetaColumns = `id, scenario_slug, label, lat, lng, budget_mins, mode,
	compiled_service_ids, created_at, updated_at`

// ListPrerenderedIsochronesByScenario returns a scenario's curated isochrones
// in creation order, without their payloads.
//
// The ordering breaks ties on id. Seeded entries are written in one boot and
// can share a created_at to the microsecond, and a list whose order changed
// between two identical requests would be a page that reshuffles itself for no
// reason.
//
// It returns an empty slice rather than nil so the handler encodes [] — a
// client distinguishing "no entries" from "null" is a distinction with no
// meaning here.
func (r *Repo) ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]transit.PrerenderedIsochrone, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+prerenderedMetaColumns+` FROM prerendered_isochrones
		 WHERE scenario_slug = $1 ORDER BY created_at, id`, scenarioSlug)
	if err != nil {
		return nil, wrap("ListPrerenderedIsochronesByScenario", err)
	}
	defer rows.Close()

	out := []transit.PrerenderedIsochrone{}
	for rows.Next() {
		var p transit.PrerenderedIsochrone
		if err := rows.Scan(&p.ID, &p.ScenarioSlug, &p.Label, &p.Lat, &p.Lng, &p.BudgetMins,
			&p.Mode, &p.CompiledServiceIDs, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, wrap("ListPrerenderedIsochronesByScenario scan", err)
		}
		out = append(out, p)
	}
	return out, wrap("ListPrerenderedIsochronesByScenario rows", rows.Err())
}

// GetPrerenderedIsochrone loads one entry whole, payload included — the one
// read that is meant to move a payload, since serving it is the point.
func (r *Repo) GetPrerenderedIsochrone(ctx context.Context, id string) (transit.PrerenderedIsochrone, bool, error) {
	var p transit.PrerenderedIsochrone
	err := r.pool.QueryRow(ctx,
		`SELECT `+prerenderedMetaColumns+`, result FROM prerendered_isochrones WHERE id = $1`, id).
		Scan(&p.ID, &p.ScenarioSlug, &p.Label, &p.Lat, &p.Lng, &p.BudgetMins,
			&p.Mode, &p.CompiledServiceIDs, &p.CreatedAt, &p.UpdatedAt, &p.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.PrerenderedIsochrone{}, false, nil
	}
	if err != nil {
		return transit.PrerenderedIsochrone{}, false, wrap("GetPrerenderedIsochrone", err)
	}
	return p, true, nil
}

// CreatePrerenderedIsochrone inserts an entry, filling in the timestamps the
// database assigned so the caller can answer 201 with a complete row rather
// than one carrying zero times.
//
// result is written through untouched. Nothing here parses it: the column is
// jsonb, so Postgres is the only thing that ever looks at its shape, and it
// looks only for "is this JSON at all".
func (r *Repo) CreatePrerenderedIsochrone(ctx context.Context, p *transit.PrerenderedIsochrone) error {
	compiled := p.CompiledServiceIDs
	if compiled == nil {
		// The column is NOT NULL DEFAULT '{}'; a nil slice would be sent as
		// NULL and violate it. An entry curated against a scenario with no
		// services is a real (if odd) thing, and it stores as the empty array.
		compiled = []string{}
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO prerendered_isochrones
		   (id, scenario_slug, label, lat, lng, budget_mins, mode, result, compiled_service_ids)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING created_at, updated_at`,
		p.ID, p.ScenarioSlug, p.Label, p.Lat, p.Lng, p.BudgetMins, string(p.Mode),
		p.Result, compiled,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	return wrap("CreatePrerenderedIsochrone", err)
}
