package postgres

import (
	"context"
	"fmt"

	"github.com/pressly/goose/v3"
)

// MigrateDownTo rolls back every migration recorded above version, running each
// one's own `-- +goose Down` block. It lives in export_test.go rather than
// postgres.go because rewinding is a test affordance: the production binary
// only ever migrates forward, and an exported rollback on the real API would be
// a foot-gun with no caller.
//
// It exists so a rewind helper in the package's tests can drive the migration
// file instead of hand-copying its Down block into Go — a copy that compiles
// and passes whatever it says, so it can drift from the migration it claims to
// undo without anything failing.
//
// Down-to rather than a single Down step, because the rewind chain calls its
// links more than once against the same database, sometimes when the version
// they unwind is already unrecorded. goose.Down rolls back whatever the current
// version happens to be, which in that state is an unrelated earlier migration;
// goose.DownTo compares against a fixed floor and returns without doing
// anything once the database is already at or below it. That makes a rewind
// helper safe to call twice, which is the property the chain is built on.
func MigrateDownTo(ctx context.Context, databaseURL string, version int64) error {
	db, err := openMigrationDB(databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := goose.DownToContext(ctx, db, migrationsDir, version); err != nil {
		return fmt.Errorf("postgres: rolling migrations back to %d: %w", version, err)
	}
	return nil
}
