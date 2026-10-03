package postgres

import (
	"context"
	"errors"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/jackc/pgx/v5"
)

func (r *Repo) CreateInvite(ctx context.Context, u account.User, t account.Token) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("CreateInvite begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// password_hash '' is "no password yet": login rejects it outright (00002).
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, email, name, is_admin, password_hash) VALUES ($1, $2, $3, $4, '')`,
		u.ID, u.Email, u.Name, u.IsAdmin); err != nil {
		return wrap("CreateInvite user", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		t.TokenHash, u.ID, t.Purpose, t.ExpiresAt); err != nil {
		return wrap("CreateInvite token", err)
	}
	return wrap("CreateInvite commit", tx.Commit(ctx))
}

func (r *Repo) CreateResetToken(ctx context.Context, t account.Token) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("CreateResetToken begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Only the newest reset link works, so an admin who reissues one because
	// the first went astray has also revoked the first.
	if _, err := tx.Exec(ctx,
		`DELETE FROM account_tokens WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`,
		t.UserID, account.TokenPurposeReset); err != nil {
		return wrap("CreateResetToken invalidate", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		t.TokenHash, t.UserID, account.TokenPurposeReset, t.ExpiresAt); err != nil {
		return wrap("CreateResetToken", err)
	}
	return wrap("CreateResetToken commit", tx.Commit(ctx))
}

// Used, expired, and disabled-account tokens are filtered here rather than in
// the handler, so every one of them takes the unknown-token path.
const liveAccountToken = `t.used_at IS NULL AND t.expires_at > now() AND u.disabled_at IS NULL`

func (r *Repo) GetAccountToken(ctx context.Context, tokenHash string) (account.Token, account.User, bool, error) {
	var t account.Token
	var u account.User
	err := r.pool.QueryRow(ctx,
		`SELECT t.token_hash, t.user_id, t.purpose, t.expires_at, t.used_at, `+userColumnsU+`
		 FROM account_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = $1 AND `+liveAccountToken, tokenHash).
		Scan(&t.TokenHash, &t.UserID, &t.Purpose, &t.ExpiresAt, &t.UsedAt,
			&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt, &u.DisabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.Token{}, account.User{}, false, nil
	}
	if err != nil {
		return account.Token{}, account.User{}, false, wrap("GetAccountToken", err)
	}
	return t, u, true, nil
}

// session.UserID is ignored and taken from the token, which is only known
// once the token has been claimed.
func (r *Repo) RedeemAccountToken(ctx context.Context, tokenHash, passwordHash string, session account.Session) (account.User, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return account.User{}, false, wrap("RedeemAccountToken begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// The claim is the single-use guarantee: the UPDATE row-locks the token, so
	// a concurrent redeem waits, re-checks used_at IS NULL, and matches nothing.
	var userID string
	err = tx.QueryRow(ctx,
		`UPDATE account_tokens t SET used_at = now()
		 FROM users u
		 WHERE t.token_hash = $1 AND u.id = t.user_id AND `+liveAccountToken+`
		 RETURNING t.user_id`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return account.User{}, false, nil
	}
	if err != nil {
		return account.User{}, false, wrap("RedeemAccountToken claim", err)
	}

	u, _, err := scanUser(tx.QueryRow(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1 RETURNING `+userColumns,
		userID, passwordHash))
	if err != nil {
		return account.User{}, false, wrap("RedeemAccountToken password", err)
	}
	// Whoever held the old password, or an old session, is signed out.
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return account.User{}, false, wrap("RedeemAccountToken sessions", err)
	}
	// Any other link still outstanding for this account (an unused invite
	// beside a reset, say) would otherwise overwrite the password just set.
	if _, err := tx.Exec(ctx,
		`DELETE FROM account_tokens WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return account.User{}, false, wrap("RedeemAccountToken other tokens", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		session.TokenHash, userID, session.ExpiresAt); err != nil {
		return account.User{}, false, wrap("RedeemAccountToken session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return account.User{}, false, wrap("RedeemAccountToken commit", err)
	}
	return u, true, nil
}
