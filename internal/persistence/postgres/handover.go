package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
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

func (r *Repo) AcceptServiceHandover(ctx context.Context, id, toUserID string) (transit.ServiceHandover, error) {
	const op = "AcceptServiceHandover"
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return transit.ServiceHandover{}, wrap(op+" begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// An unlocked read only to learn which service this is: the locks below
	// are taken service first, then offer. Deleting a service locks its row
	// and then cascades into its offers, so taking them in the other order
	// here could deadlock a sender's delete against a recipient's accept.
	var serviceID string
	err = tx.QueryRow(ctx,
		`SELECT h.user_service_id FROM service_handovers h WHERE h.id = $1 AND h.to_user_id = $2`,
		id, toUserID).Scan(&serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.ServiceHandover{}, handler.ErrHandoverNotFound
	}
	if err != nil {
		return transit.ServiceHandover{}, wrap(op+" lookup", err)
	}
	ownerID, routeID, found, err := lockUserService(ctx, tx, op, serviceID)
	if err != nil {
		return transit.ServiceHandover{}, err
	}
	if !found {
		// Deleted since the read above; the cascade took the offer with it.
		return transit.ServiceHandover{}, handler.ErrHandoverNotFound
	}

	// Two accepts of one offer serialise on this lock: the second waits
	// here, then reads 'accepted' and answers not-pending. It also keeps a
	// cancel or decline from closing the offer underneath the move.
	var (
		fromUserID string
		live       bool
	)
	err = tx.QueryRow(ctx,
		`SELECT h.from_user_id, (`+livePending+`)
		   FROM service_handovers h
		  WHERE h.id = $1 AND h.to_user_id = $2
		    FOR UPDATE`,
		id, toUserID).Scan(&fromUserID, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return transit.ServiceHandover{}, handler.ErrHandoverNotFound
	}
	if err != nil {
		return transit.ServiceHandover{}, wrap(op+" lock", err)
	}
	if !live {
		return transit.ServiceHandover{}, handler.ErrHandoverNotPending
	}
	// The offer was made by whoever owned the service then; if it has changed
	// hands since (an admin move), the offer no longer describes anything.
	if ownerID != fromUserID {
		return transit.ServiceHandover{}, handler.ErrHandoverSenderNotOwner
	}

	// The session middleware already turns a disabled account away, so this
	// only matters for an account disabled since the request was authorised.
	var disabled bool
	if err := tx.QueryRow(ctx,
		`SELECT disabled_at IS NOT NULL FROM users WHERE id = $1`, toUserID).Scan(&disabled); err != nil {
		return transit.ServiceHandover{}, wrap(op+" recipient", err)
	}
	if disabled {
		return transit.ServiceHandover{}, handler.ErrHandoverRecipientDisabled
	}

	if err := moveLockedUserService(ctx, tx, op, serviceID, routeID, toUserID); err != nil {
		return transit.ServiceHandover{}, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE service_handovers SET status = $2, decided_at = now() WHERE id = $1`,
		id, transit.HandoverAccepted); err != nil {
		return transit.ServiceHandover{}, wrap(op+" close", err)
	}
	h, err := scanHandover(tx.QueryRow(ctx, handoverSelect+` WHERE h.id = $1`, id))
	if err != nil {
		return transit.ServiceHandover{}, wrap(op+" read", err)
	}
	return h, wrap(op+" commit", tx.Commit(ctx))
}

func (r *Repo) TransferUserService(ctx context.Context, serviceID, toUserID string) error {
	// The owner move without an offer, for the admin force-move (SPA-390).
	// It is the ticket's name for the method; the domain word is handover.
	// The same refusal for a service still in a scenario applies, so the
	// scenario-membership invariant does not depend on which path moved it.
	const op = "TransferUserService"
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap(op+" begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	_, routeID, found, err := lockUserService(ctx, tx, op, serviceID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("postgres: %s: no service with id %q", op, serviceID)
	}
	if err := moveLockedUserService(ctx, tx, op, serviceID, routeID, toUserID); err != nil {
		return err
	}
	return wrap(op+" commit", tx.Commit(ctx))
}

// Row-locks the service for the rest of the transaction and returns what the
// move needs from it.
func lockUserService(ctx context.Context, tx pgx.Tx, op, serviceID string) (ownerID, routeID string, found bool, err error) {
	err = tx.QueryRow(ctx,
		`SELECT owner_id, route_id FROM user_services WHERE id = $1 FOR UPDATE`, serviceID).Scan(&ownerID, &routeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, wrap(op+" service", err)
	}
	return ownerID, routeID, true, nil
}

const maxRouteCopyAttempts = 100

// What moves with a service and what cannot. The caller holds the service's
// row lock (lockUserService).
//
//   - service_publications is keyed by the service and has no owner column,
//     so the publication and its public URL come along untouched.
//   - jobs carry owner_id, and GET /api/jobs/{id} authorises on it, so the
//     service's compile jobs are re-owned or the recipient could not poll them.
//   - routing_jobs (draft isochrones) are short-lived and are left alone.
//   - A route the recipient cannot reference is copied, never moved: the
//     sender may use it elsewhere. "Cannot reference" is read as owned by
//     anyone but the recipient, which is wider than the ticket's "owned by
//     the sender": it also covers a service an admin once moved onto a third
//     party's route, and copies for an admin recipient who could have
//     referenced it anyway, which costs one spare route. The copy keeps the
//     geometry byte for byte, so snapped stops keep their chainage. It takes
//     no scenario: a route's scenario is one its owner owns, which the
//     recipient does not.
//   - A service in a scenario is refused. Every member of a scenario must
//     belong to the scenario's owner, so the scenarios found are the
//     sender's, and interchange pairs name the service's stops; silently
//     editing the sender's scenario is not an option this code takes.
func moveLockedUserService(ctx context.Context, tx pgx.Tx, op, serviceID, routeID, toUserID string) error {
	slugs, err := scenarioSlugsContaining(ctx, tx, op, serviceID)
	if err != nil {
		return err
	}
	if len(slugs) > 0 {
		return &handler.ServiceInScenariosError{Slugs: slugs}
	}

	var routeOwner *string
	if err := tx.QueryRow(ctx, `SELECT owner_id FROM routes WHERE id = $1`, routeID).Scan(&routeOwner); err != nil {
		return wrap(op+" route", err)
	}
	if routeOwner != nil && *routeOwner != toUserID {
		copyID, err := copyRouteTo(ctx, tx, op, routeID, toUserID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE user_services SET route_id = $2 WHERE id = $1`, serviceID, copyID); err != nil {
			return wrap(op+" repoint route", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE user_services SET owner_id = $2, updated_at = now() WHERE id = $1`,
		serviceID, toUserID); err != nil {
		return wrap(op+" owner", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE jobs SET owner_id = $2 WHERE user_service_id = $1`, serviceID, toUserID); err != nil {
		return wrap(op+" jobs", err)
	}
	return nil
}

func scenarioSlugsContaining(ctx context.Context, tx pgx.Tx, op, serviceID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT sc.slug
		   FROM user_scenario_services m
		   JOIN user_scenarios sc ON sc.id = m.user_scenario_id
		  WHERE m.user_service_id = $1
		  ORDER BY sc.slug`, serviceID)
	if err != nil {
		return nil, wrap(op+" scenarios", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, wrap(op+" scenarios scan", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs, wrap(op+" scenarios rows", rows.Err())
}

// The copy's slug is the original's with a numeric suffix, found by letting
// the unique index arbitrate rather than by a read-then-insert that two
// transactions could both pass.
func copyRouteTo(ctx context.Context, tx pgx.Tx, op, routeID, ownerID string) (string, error) {
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM routes WHERE id = $1`, routeID).Scan(&slug); err != nil {
		return "", wrap(op+" route slug", err)
	}
	copyID, err := ids.NewUUID()
	if err != nil {
		return "", wrap(op+" route id", err)
	}
	for attempt := 2; attempt <= maxRouteCopyAttempts; attempt++ {
		tag, err := tx.Exec(ctx,
			`INSERT INTO routes (id, scenario_id, owner_id, slug, name, description, mode,
			                     geometry, bidirectional, segments)
			 SELECT $2, NULL, $3, $4, name, description, mode, geometry, bidirectional, segments
			   FROM routes WHERE id = $1
			 ON CONFLICT (slug) DO NOTHING`,
			routeID, copyID, ownerID, fmt.Sprintf("%s-%d", slug, attempt))
		if err != nil {
			return "", wrap(op+" copy route", err)
		}
		if tag.RowsAffected() == 1 {
			return copyID, nil
		}
	}
	return "", fmt.Errorf("postgres: %s: no free slug for a copy of route %q after %d attempts", op, slug, maxRouteCopyAttempts)
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
