package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
)

// The author is the one thing a publication reads through its draft besides
// the slug: user_services.owner_id, joined on to users.name. It is live, not
// part of the snapshot, so a rename or a transfer changes the byline without a
// republish (ADR-0005). The owner's email and id are never selected.
const (
	publicationColumns = `p.user_service_id, p.compile_job_id, p.name, p.subtext, p.description, p.routes, p.published_at, u.name`
	publicationJoins   = `
		   JOIN user_services s ON s.id = p.user_service_id
		   JOIN users u ON u.id = s.owner_id`
)

// publishAfterDecide is a test seam. When non-nil, PublishUserService calls it
// after decide returns a publication and before the snapshot upsert.
var publishAfterDecide func()

// PublishUserService locks the draft, runs decide while that lock is held, and
// upserts the publication decide returns in the same transaction.
// UpdateUserService takes its own transaction and blocks on this row lock, so
// an edit cannot land between the staleness check and the write.
func (r *Repo) PublishUserService(ctx context.Context, serviceID string, decide handler.PublicationDecide) (transit.ServicePublication, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return transit.ServicePublication{}, wrap("PublishUserService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// OF s keeps the lock on the draft. The join exists so the column list,
	// which also reads the route, still scans; it must not lock the route.
	svc, err := scanUserService(tx.QueryRow(ctx,
		`SELECT `+userServiceColumns+` FROM `+userServiceFrom+` WHERE s.id = $1 FOR UPDATE OF s`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.ServicePublication{}, fmt.Errorf("postgres: PublishUserService: no service with id %q", serviceID)
	}
	if err != nil {
		return transit.ServicePublication{}, wrap("PublishUserService lock", err)
	}

	pub, err := decide(ctx, svc, txPublicationRead{tx})
	if err != nil {
		return transit.ServicePublication{}, err
	}
	if publishAfterDecide != nil {
		publishAfterDecide()
	}

	stored, err := upsertPublication(ctx, tx, pub)
	if err != nil {
		return transit.ServicePublication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return transit.ServicePublication{}, wrap("PublishUserService commit", err)
	}
	return stored, nil
}

// UnpublishUserService locks the draft and deletes its publication in that
// same transaction, so a publish already holding the row cannot commit a
// snapshot after this delete. A missing publication, or a service row that
// is already gone, is success: unpublish is idempotent.
func (r *Repo) UnpublishUserService(ctx context.Context, serviceID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("UnpublishUserService begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var id string
	err = tx.QueryRow(ctx,
		`SELECT id FROM user_services WHERE id = $1 FOR UPDATE`, serviceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return wrap("UnpublishUserService lock", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM service_publications WHERE user_service_id = $1`, serviceID); err != nil {
		return wrap("UnpublishUserService delete", err)
	}
	return wrap("UnpublishUserService commit", tx.Commit(ctx))
}

func (r *Repo) GetServicePublication(ctx context.Context, serviceID string) (transit.ServicePublication, bool, error) {
	pub, err := scanPublication(r.pool.QueryRow(ctx,
		`SELECT `+publicationColumns+` FROM service_publications p`+publicationJoins+`
		  WHERE p.user_service_id = $1`, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.ServicePublication{}, false, nil
	}
	if err != nil {
		return transit.ServicePublication{}, false, wrap("GetServicePublication", err)
	}
	return pub, true, nil
}

// GetServicePublicationBySlug is the public read. It joins user_services only
// to turn the slug into an id and to reach the owner's name, and selects none
// of the draft's prose or geometry, so what it returns cannot carry an
// unpublished edit (ADR-0005). An unpublished slug and an unknown one both
// come back as not found.
func (r *Repo) GetServicePublicationBySlug(ctx context.Context, slug string) (transit.ServicePublication, bool, error) {
	pub, err := scanPublication(r.pool.QueryRow(ctx,
		`SELECT `+publicationColumns+` FROM service_publications p`+publicationJoins+`
		  WHERE s.slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.ServicePublication{}, false, nil
	}
	if err != nil {
		return transit.ServicePublication{}, false, wrap("GetServicePublicationBySlug", err)
	}
	return pub, true, nil
}

func (r *Repo) ListPublishedServiceSummaries(ctx context.Context, after *transit.PublishedIndexKey, limit int) (transit.PublishedIndexPage, error) {
	// Every service_publications row is a published service, so there is no
	// filter to write: an unpublished service has no row to find.
	//
	// The prose comes from the publication, never the draft. The join reaches
	// user_services for the slug, which the publication does not copy because
	// it cannot change: a slug is never re-minted, so reading it off the draft
	// cannot leak an edit. It also reaches the owner, for the byline's live
	// author name (see publicationColumns). Neither the draft's stops or vehicle
	// documents nor the publication's routes payload are selected — this is a
	// list of cards, and those columns are what make a row heavy.
	//
	// By first publication, newest first. Slug breaks a tie, so the order is
	// total, and neither key changes while a service stays published, so a
	// keyset cursor over it never skips or repeats a row (00033).
	//
	// A limit of zero or less reads everything. Otherwise one row more than
	// the limit is read: it is the evidence that a next page exists, so the
	// last page carries no next key and nobody fetches an empty one.
	var afterAt *time.Time
	var afterSlug string
	if after != nil {
		afterAt, afterSlug = &after.FirstPublishedAt, after.Slug
	}
	var fetch *int
	if limit > 0 {
		n := limit + 1
		fetch = &n
	}
	rows, err := r.pool.Query(ctx,
		`SELECT s.slug, p.name, p.subtext, p.description, u.name, p.published_at, p.first_published_at
		   FROM service_publications p`+publicationJoins+`
		  WHERE $1::timestamptz IS NULL
		     OR p.first_published_at < $1
		     OR (p.first_published_at = $1 AND s.slug > $2)
		  ORDER BY p.first_published_at DESC, s.slug
		  LIMIT $3`, afterAt, afterSlug, fetch)
	if err != nil {
		return transit.PublishedIndexPage{}, wrap("ListPublishedServiceSummaries", err)
	}
	defer rows.Close()

	page := transit.PublishedIndexPage{Items: []transit.PublishedServiceSummary{}}
	var last transit.PublishedIndexKey
	for rows.Next() {
		var s transit.PublishedServiceSummary
		var at time.Time
		if err := rows.Scan(&s.Slug, &s.Name, &s.Subtext, &s.Description, &s.AuthorName, &s.PublishedAt, &at); err != nil {
			return transit.PublishedIndexPage{}, wrap("ListPublishedServiceSummaries scan", err)
		}
		if limit > 0 && len(page.Items) == limit {
			// The extra row: only its existence matters.
			page.Next = &last
			continue
		}
		page.Items = append(page.Items, s)
		last = transit.PublishedIndexKey{FirstPublishedAt: at, Slug: s.Slug}
	}
	if err := rows.Err(); err != nil {
		return transit.PublishedIndexPage{}, wrap("ListPublishedServiceSummaries rows", err)
	}
	return page, nil
}

func (r *Repo) GetSucceededCompileJob(ctx context.Context, id string) (transit.Job, bool, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs
		  WHERE id = $1 AND status = $2 AND kind = $3`,
		id, transit.JobStatusSucceeded, transit.JobKindCompileUserService)
	return scanJob(row)
}

type txPublicationRead struct{ tx pgx.Tx }

func (q txPublicationRead) LatestSucceededCompileJob(ctx context.Context, serviceID string) (transit.Job, bool, error) {
	row := q.tx.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs
		  WHERE user_service_id = $1 AND kind = $2 AND status = $3
		  ORDER BY created_at DESC, id DESC
		  LIMIT 1`,
		serviceID, transit.JobKindCompileUserService, transit.JobStatusSucceeded)
	return scanJob(row)
}

func (q txPublicationRead) ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.tx.Query(ctx,
		`SELECT `+routeColumns+` FROM routes WHERE id::text = ANY($1)`, ids)
	if err != nil {
		return nil, wrap("publication routes", err)
	}
	defer rows.Close()

	var out []transit.Route
	for rows.Next() {
		rt, err := scanRoute(rows)
		if err != nil {
			return nil, wrap("publication routes scan", err)
		}
		out = append(out, rt)
	}
	return out, wrap("publication routes rows", rows.Err())
}

func upsertPublication(ctx context.Context, tx pgx.Tx, pub transit.ServicePublication) (transit.ServicePublication, error) {
	routes := pub.Routes
	if routes == nil {
		routes = []transit.Route{}
	}
	payload, err := json.Marshal(routes)
	if err != nil {
		return transit.ServicePublication{}, wrap("marshal publication routes", err)
	}

	stored, err := scanPublication(tx.QueryRow(ctx,
		`WITH p AS (
		 INSERT INTO service_publications
		    (user_service_id, compile_job_id, name, subtext, description, routes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (user_service_id) DO UPDATE SET
		    compile_job_id = EXCLUDED.compile_job_id,
		    name = EXCLUDED.name,
		    subtext = EXCLUDED.subtext,
		    description = EXCLUDED.description,
		    routes = EXCLUDED.routes,
		    published_at = now()
		 RETURNING *)
		 SELECT `+publicationColumns+` FROM p`+publicationJoins,
		pub.UserServiceID, pub.CompileJobID, pub.Name, pub.Subtext, pub.Description, payload))
	if err != nil {
		return transit.ServicePublication{}, wrap("upsert publication", err)
	}
	return stored, nil
}

func scanPublication(row pgx.Row) (transit.ServicePublication, error) {
	var (
		pub    transit.ServicePublication
		routes []byte
	)
	if err := row.Scan(&pub.UserServiceID, &pub.CompileJobID, &pub.Name, &pub.Subtext,
		&pub.Description, &routes, &pub.PublishedAt, &pub.AuthorName); err != nil {
		return transit.ServicePublication{}, err
	}
	if err := json.Unmarshal(routes, &pub.Routes); err != nil {
		return transit.ServicePublication{}, fmt.Errorf("decoding publication routes: %w", err)
	}
	if pub.Routes == nil {
		pub.Routes = []transit.Route{}
	}
	return pub, nil
}
