package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Expiry is lazy (00032): a row still 'pending' on disk past its expires_at
// reads as 'expired'. These two are the only spellings of that boundary.
const (
	expiredPending = `h.status = 'pending' AND h.expires_at <= now()`
	livePending    = `h.status = 'pending' AND h.expires_at > now()`
)

const handoverSelect = `
	SELECT h.id, h.user_service_id, s.slug, s.name,
	       h.from_user_id, f.name, h.to_user_id, t.name,
	       CASE WHEN ` + expiredPending + ` THEN 'expired' ELSE h.status END,
	       h.created_at, h.decided_at, h.expires_at
	  FROM service_handovers h
	  JOIN user_services s ON s.id = h.user_service_id
	  JOIN users f ON f.id = h.from_user_id
	  JOIN users t ON t.id = h.to_user_id`

func scanHandover(row pgx.Row) (transit.ServiceHandover, error) {
	var h transit.ServiceHandover
	err := row.Scan(&h.ID, &h.UserServiceID, &h.ServiceSlug, &h.ServiceName,
		&h.FromUserID, &h.FromName, &h.ToUserID, &h.ToName,
		&h.Status, &h.CreatedAt, &h.DecidedAt, &h.ExpiresAt)
	return h, err
}

func (r *Repo) HasPendingServiceHandover(ctx context.Context, serviceID string) (bool, error) {
	var pending bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM service_handovers h WHERE h.user_service_id = $1 AND `+livePending+`)`,
		serviceID).Scan(&pending)
	return pending, wrap("HasPendingServiceHandover", err)
}

func (r *Repo) OfferServiceHandover(ctx context.Context, h transit.ServiceHandover, ttl time.Duration) (transit.ServiceHandover, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return transit.ServiceHandover{}, wrap("OfferServiceHandover begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// An expired offer is still 'pending' on disk and would hold the
	// one-pending index against this insert, so it is closed first.
	if _, err := tx.Exec(ctx,
		`UPDATE service_handovers h SET status = 'expired', decided_at = h.expires_at
		  WHERE h.user_service_id = $1 AND `+expiredPending,
		h.UserServiceID); err != nil {
		return transit.ServiceHandover{}, wrap("OfferServiceHandover expire", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO service_handovers (id, user_service_id, from_user_id, to_user_id, expires_at)
		 VALUES ($1, $2, $3, $4, now() + make_interval(secs => $5))`,
		h.ID, h.UserServiceID, h.FromUserID, h.ToUserID, ttl.Seconds())
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "service_handovers_one_pending" {
		return transit.ServiceHandover{}, handler.ErrHandoverPending
	}
	if err != nil {
		return transit.ServiceHandover{}, wrap("OfferServiceHandover insert", err)
	}

	stored, err := scanHandover(tx.QueryRow(ctx, handoverSelect+` WHERE h.id = $1`, h.ID))
	if err != nil {
		return transit.ServiceHandover{}, wrap("OfferServiceHandover read", err)
	}
	return stored, wrap("OfferServiceHandover commit", tx.Commit(ctx))
}

func (r *Repo) ListPendingServiceHandovers(ctx context.Context, userID string) ([]transit.ServiceHandover, error) {
	rows, err := r.pool.Query(ctx,
		handoverSelect+` WHERE (h.from_user_id = $1 OR h.to_user_id = $1) AND `+livePending+`
		 ORDER BY h.created_at DESC, h.id`, userID)
	if err != nil {
		return nil, wrap("ListPendingServiceHandovers", err)
	}
	defer rows.Close()

	var out []transit.ServiceHandover
	for rows.Next() {
		h, err := scanHandover(rows)
		if err != nil {
			return nil, wrap("ListPendingServiceHandovers scan", err)
		}
		out = append(out, h)
	}
	return out, wrap("ListPendingServiceHandovers rows", rows.Err())
}

func (r *Repo) CancelServiceHandover(ctx context.Context, id, fromUserID string) (transit.ServiceHandover, error) {
	return r.decideHandover(ctx, "CancelServiceHandover", sender, id, fromUserID, transit.HandoverCancelled)
}

func (r *Repo) DeclineServiceHandover(ctx context.Context, id, toUserID string) (transit.ServiceHandover, error) {
	return r.decideHandover(ctx, "DeclineServiceHandover", recipient, id, toUserID, transit.HandoverDeclined)
}

// The column a decision belongs to. It is spliced into SQL, so it is only
// ever one of these two constants.
type handoverParty string

const (
	sender    handoverParty = "from_user_id"
	recipient handoverParty = "to_user_id"
)

func (r *Repo) decideHandover(ctx context.Context, op string, party handoverParty, id, callerID, status string) (transit.ServiceHandover, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE service_handovers h SET status = $3, decided_at = now()
		  WHERE h.id = $1 AND h.`+string(party)+` = $2 AND `+livePending,
		id, callerID, status)
	if err != nil {
		return transit.ServiceHandover{}, wrap(op, err)
	}
	if tag.RowsAffected() == 1 {
		h, err := scanHandover(r.pool.QueryRow(ctx, handoverSelect+` WHERE h.id = $1`, id))
		return h, wrap(op+" read", err)
	}

	// Nothing closed: either the caller is not this offer's party (or there
	// is no such offer), which reads as not found, or it is already closed.
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM service_handovers WHERE id = $1 AND `+string(party)+` = $2)`,
		id, callerID).Scan(&exists); err != nil {
		return transit.ServiceHandover{}, wrap(op+" lookup", err)
	}
	if !exists {
		return transit.ServiceHandover{}, handler.ErrHandoverNotFound
	}
	return transit.ServiceHandover{}, handler.ErrHandoverNotPending
}
