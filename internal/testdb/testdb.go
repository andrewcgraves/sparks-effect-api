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

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

const namePrefix = "spa_testdb_"

const staleAfter = time.Hour

var counter atomic.Uint64

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

func Fresh(t *testing.T) string {
	t.Helper()
	admin := URL(t)
	tmpl, err := template()
	if err != nil {
		t.Fatalf("testdb: building template database: %v", err)
	}
	return create(t, admin, "TEMPLATE "+tmpl)
}

func Empty(t *testing.T) string {
	t.Helper()
	return create(t, URL(t), "")
}

func Main(m *testing.M) {
	code := m.Run()
	dropTemplate()
	os.Exit(code)
}

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

var template = sync.OnceValues(buildTemplate)

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

func newName() string {
	return fmt.Sprintf("%s%d_%d", namePrefix, time.Now().Unix(), counter.Add(1))
}

func createdAt(name string) time.Time {
	rest := strings.TrimPrefix(name, namePrefix)
	secs, _, _ := strings.Cut(rest, "_")
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

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
