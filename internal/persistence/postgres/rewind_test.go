package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// rewindTo forgets migration `version` and every later one so the next
// Migrate will re-apply them. Schema those later migrations created is left
// in place — use the per-migration rewind helpers when the schema must go
// too. Versions come from `>=`, not a hard-coded list, so adding a migration
// does not edit this helper or the tests that call it.
func rewindTo(t *testing.T, url string, version int) {
	t.Helper()
	exec(t, url, fmt.Sprintf(`DELETE FROM goose_db_version WHERE version_id >= %d`, version))
}

func TestRewindToUnrecordsEveryLaterMigration(t *testing.T) {
	versions := migrationVersions(t)
	if len(versions) < 2 {
		t.Fatalf("need at least two migrations, got %v", versions)
	}
	latest := versions[len(versions)-1]
	previous := versions[len(versions)-2]

	_, url := freshRepo(t)
	rewindTo(t, url, previous)
	for _, v := range recordedGooseVersions(t, url) {
		if v >= previous {
			t.Errorf("version %d still recorded after rewindTo(%d); latest on disk is %d",
				v, previous, latest)
		}
	}
}

func migrationVersions(t *testing.T) []int {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var versions []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		versions = append(versions, n)
	}
	if len(versions) == 0 {
		t.Fatal("migrations/ has no numbered .sql files")
	}
	sort.Ints(versions)
	return versions
}

func recordedGooseVersions(t *testing.T, url string) []int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT version_id FROM goose_db_version WHERE version_id > 0 ORDER BY version_id`)
	if err != nil {
		t.Fatalf("query goose versions: %v", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan goose version: %v", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("goose versions: %v", err)
	}
	return out
}
