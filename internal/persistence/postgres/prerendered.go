package postgres

import (
	"context"
	"errors"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

// --- Scenario service membership ---

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

const prerenderedMetaColumns = `id, scenario_slug, label, lat, lng, budget_mins, mode,
	compiled_service_ids, created_at, updated_at`

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
