// Package testdb hands every integration test its own Postgres database.
//
// The suite used to share one database and reset it per test with
//
//	DROP SCHEMA public CASCADE; CREATE SCHEMA public;
//
// followed by a full goose migration run. That is ~160ms of pure setup on every
// test — and because it reset a *shared* database, two packages doing it at the
// same time wiped each other's schema mid-test. `go test` therefore had to be
// pinned to `-p 1`, serialising the two slowest packages in the repository.
//
// Instead, this package migrates once per test binary into a template database
// and then gives each test a copy of it with
//
//	CREATE DATABASE <test> TEMPLATE <template>
//
// which Postgres satisfies with a file-level copy: ~35ms rather than ~160ms,
// and, more importantly, no shared mutable state. Nothing any test does is
// visible to any other test, in its own package or another, so the packages can
// run concurrently again.
//
// A template is a fully-migrated database, which is what all but one of the
// migration tests want: they rewind the specific migration under test and let
// goose re-apply it. The exception — proving the migrations run cleanly against
// nothing at all — takes Empty instead.
package testdb

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
)

// namePrefix marks every database this package creates, so a sweep can
// recognise its own leftovers and leave anything else in the server alone.
const namePrefix = "spa_testdb_"

// staleAfter is how old a leftover database must be before a later run will
// drop it. Databases are named with the Unix second they were created, so this
// needs no bookkeeping table.
//
// The window exists because a run killed hard — SIGKILL, a panicking CI
// runner — never reaches its cleanup, and the throwaway container often
// outlives a single `go test`. An hour is far longer than any test run and far
// shorter than the gap between sessions, so a sweep can never collect a
// database that a concurrently-running package is still using.
const staleAfter = time.Hour

// counter disambiguates databases created within the same second.
var counter atomic.Uint64

// URL resolves the throwaway Postgres connection string. It reads
// TEST_DATABASE_URL (falling back to DATABASE_URL). When neither is set the
// integration tests skip locally so `go test ./...` stays green without a DB —
// but in CI (where the CI env var is present) a missing URL is a hard failure,
// so a misconfigured pipeline can never pass green by silently skipping.
func URL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		u = os.Getenv("DATABASE_URL")
	}
	if u == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL (or DATABASE_URL) must be set for integration tests in CI")
		}
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests (see `make db-up`)")
	}
	return u
}

// Fresh returns the URL of a database that has had every migration applied and
// that no other test will touch. It is dropped when the test finishes.
func Fresh(t *testing.T) string {
	t.Helper()
	admin := URL(t)
	tmpl, err := template()
	if err != nil {
		t.Fatalf("testdb: building template database: %v", err)
	}
	return create(t, admin, "TEMPLATE "+tmpl)
}

// Empty returns the URL of a database with no schema and no migrations applied,
// for the tests that are about running the migrations themselves.
func Empty(t *testing.T) string {
	t.Helper()
	return create(t, URL(t), "")
}

// Main is the TestMain body for a package that uses this one:
//
//	func TestMain(m *testing.M) { testdb.Main(m) }
//
// It exists to drop the template database, which outlives every individual test
// and so cannot be cleaned up with t.Cleanup.
func Main(m *testing.M) {
	code := m.Run()
	dropTemplate()
	os.Exit(code)
}

// create makes a database and registers its removal. extra is appended to the
// CREATE DATABASE statement — "TEMPLATE x", or empty for a bare database.
func create(t *testing.T, admin, extra string) string {
	t.Helper()
	name := newName()
	if err := exec(admin, "CREATE DATABASE "+name+" "+extra); err != nil {
		t.Fatalf("testdb: creating %s: %v", name, err)
	}
	t.Cleanup(func() {
		// FORCE terminates any connection the test left open. Cleanups run in
		// reverse order of registration, so a pool closed by a later-registered
		// cleanup is already gone by the time this runs; FORCE is here for the
		// case where a test failed before it could close one.
		if err := exec(admin, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("testdb: dropping %s: %v", name, err)
		}
	})
	u, err := withDatabase(admin, name)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	return u
}

// template builds the migrated template database, once per test binary, and
// returns its name. Concurrent packages each build their own: they are separate
// processes, so there is no shared state to coordinate, and one migration run
// per package is cheap next to one per test.
var template = sync.OnceValues(buildTemplate)

// templateName is recorded so Main can drop it. Read only after template has
// run.
var templateName string

func buildTemplate() (string, error) {
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		admin = os.Getenv("DATABASE_URL")
	}
	if admin == "" {
		return "", fmt.Errorf("no TEST_DATABASE_URL")
	}

	sweep(admin)

	name := newName() + "_tmpl"
	if err := exec(admin, "CREATE DATABASE "+name); err != nil {
		return "", fmt.Errorf("creating template %s: %w", name, err)
	}
	u, err := withDatabase(admin, name)
	if err != nil {
		return "", err
	}
	// Migrate opens its own handle and closes it before returning. That matters
	// more than usual here: CREATE DATABASE ... TEMPLATE is refused while any
	// session is connected to the template.
	if err := postgres.Migrate(context.Background(), u); err != nil {
		return "", fmt.Errorf("migrating template %s: %w", name, err)
	}
	templateName = name
	return name, nil
}

func dropTemplate() {
	if templateName == "" {
		return
	}
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		admin = os.Getenv("DATABASE_URL")
	}
	if admin == "" {
		return
	}
	_ = exec(admin, "DROP DATABASE IF EXISTS "+templateName+" WITH (FORCE)")
}

// sweep drops databases this package left behind in an earlier run. See
// staleAfter for why age is the right test and why it cannot race a live run.
func sweep(admin string) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1`, namePrefix+"%")
	if err != nil {
		return
	}
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		if createdAt(name).Before(time.Now().Add(-staleAfter)) {
			stale = append(stale, name)
		}
	}
	rows.Close()

	for _, name := range stale {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}
}

// newName returns a fresh database identifier carrying the time it was made.
// Postgres lower-cases unquoted identifiers, so everything here stays lower
// case and the name needs no quoting.
func newName() string {
	return fmt.Sprintf("%s%d_%d", namePrefix, time.Now().Unix(), counter.Add(1))
}

// createdAt reads back the timestamp newName encoded. An unparseable name is
// reported as the zero time, which makes it stale — but only names carrying
// this package's own prefix ever reach here.
func createdAt(name string) time.Time {
	rest := strings.TrimPrefix(name, namePrefix)
	secs, _, _ := strings.Cut(rest, "_")
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// exec runs one statement on a short-lived connection. CREATE DATABASE and DROP
// DATABASE cannot run inside a transaction and must be issued from a connection
// to some *other* database, which is what the admin URL provides.
func exec(admin, stmt string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return err
	}
	return nil
}

// withDatabase rewrites a connection string to point at a different database on
// the same server. It goes through pgx's parser so that both URL and
// keyword/value DSN forms are accepted, and emits a URL either way.
func withDatabase(admin, database string) (string, error) {
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		return "", fmt.Errorf("parsing database URL: %w", err)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:   "/" + database,
	}
	// Only the one parameter is carried over. A throwaway test server either
	// speaks TLS or does not; anything else in the original string is tuning
	// that no test depends on.
	if cfg.TLSConfig == nil {
		u.RawQuery = "sslmode=disable"
	}
	return u.String(), nil
}
