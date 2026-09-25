package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	idxServiceA = "00000000-0000-4008-8003-000000000011"
	idxServiceB = "00000000-0000-4008-8003-000000000012"
	idxServiceC = "00000000-0000-4008-8003-000000000013"
	idxJobA     = "00000000-0000-400a-8003-000000000011"
	idxJobB     = "00000000-0000-400a-8003-000000000012"
	idxJobC     = "00000000-0000-400a-8003-000000000013"
)

func indexedService(id, slug, name string) transit.UserService {
	svc := sampleUserService()
	svc.ID = id
	svc.Slug = slug
	svc.Name = name
	svc.Subtext = name + " subtext"
	svc.Description = name + " description"
	return svc
}

func createIndexedServices(t *testing.T, repo *postgres.Repo, ctx context.Context, svcs ...transit.UserService) {
	t.Helper()
	for _, svc := range svcs {
		if err := repo.CreateUserService(ctx, svc); err != nil {
			t.Fatalf("CreateUserService %s: %v", svc.Slug, err)
		}
	}
}

func publishIndexed(t *testing.T, repo *postgres.Repo, ctx context.Context, serviceID, jobID string) {
	t.Helper()
	succeedUserServiceCompile(t, repo, ctx, serviceID, jobID, nil)
	if _, err := publishUserService(ctx, repo, serviceID); err != nil {
		t.Fatalf("PublishUserService %s: %v", serviceID, err)
	}
}

func listIndex(t *testing.T, repo *postgres.Repo, ctx context.Context) []transit.PublishedServiceSummary {
	t.Helper()
	got, err := repo.ListPublishedServiceSummaries(ctx)
	if err != nil {
		t.Fatalf("ListPublishedServiceSummaries: %v", err)
	}
	return got
}

func indexSlugs(items []transit.PublishedServiceSummary) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Slug)
	}
	return out
}

func TestListPublishedServiceSummariesListsOnlyPublicationsAndTheirProse(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	published := indexedService(idxServiceA, "published-line", "Published Line")
	compiledOnly := indexedService(idxServiceB, "compiled-line", "Compiled Line")
	draftOnly := indexedService(idxServiceC, "draft-line", "Draft Line")
	createIndexedServices(t, repo, ctx, published, compiledOnly, draftOnly)

	if got := listIndex(t, repo, ctx); got == nil || len(got) != 0 {
		t.Fatalf("index before any publish = %#v, want an empty slice", got)
	}

	publishIndexed(t, repo, ctx, published.ID, idxJobA)
	// A fresh compile is not a publication.
	succeedUserServiceCompile(t, repo, ctx, compiledOnly.ID, idxJobB, nil)

	want := transit.PublishedServiceSummary{
		Slug: "published-line", Name: "Published Line",
		Subtext: "Published Line subtext", Description: "Published Line description",
	}
	if got := listIndex(t, repo, ctx); len(got) != 1 || got[0] != want {
		t.Fatalf("index = %+v, want only %+v", got, want)
	}

	// Editing the draft does not reach the index: the prose it lists is the
	// publication's copy.
	draft, found, err := repo.GetUserServiceByID(ctx, published.ID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	draft.Name = "Renamed draft"
	draft.Subtext = "Edited subtext"
	draft.Description = "Edited description"
	if err := repo.UpdateUserService(ctx, draft); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	if got := listIndex(t, repo, ctx); len(got) != 1 || got[0] != want {
		t.Fatalf("index after a draft edit = %+v, want the published prose %+v", got, want)
	}

	if err := repo.UnpublishUserService(ctx, published.ID); err != nil {
		t.Fatalf("UnpublishUserService: %v", err)
	}
	if got := listIndex(t, repo, ctx); got == nil || len(got) != 0 {
		t.Fatalf("index after unpublish = %#v, want an empty slice", got)
	}
}

func TestListPublishedServiceSummariesOrdersMostRecentlyPublishedFirst(t *testing.T) {
	repo, ctx, dbURL := userServiceFixture(t)
	a := indexedService(idxServiceA, "middle-line", "Middle Line")
	b := indexedService(idxServiceB, "zeta-line", "Zeta Line")
	c := indexedService(idxServiceC, "alpha-line", "Alpha Line")
	createIndexedServices(t, repo, ctx, a, b, c)
	publishIndexed(t, repo, ctx, a.ID, idxJobA)
	publishIndexed(t, repo, ctx, b.ID, idxJobB)
	publishIndexed(t, repo, ctx, c.ID, idxJobC)

	// Pin the publish times so the order does not depend on how quickly the
	// three publishes above ran. b and c share an instant. All three are in
	// the past, so the republish below lands after them.
	exec(t, dbURL,
		`UPDATE service_publications SET published_at = '2001-01-01T00:00:00Z' WHERE user_service_id = '`+a.ID+`'`,
		`UPDATE service_publications SET published_at = '2001-02-01T00:00:00Z' WHERE user_service_id = '`+b.ID+`'`,
		`UPDATE service_publications SET published_at = '2001-02-01T00:00:00Z' WHERE user_service_id = '`+c.ID+`'`)

	got := indexSlugs(listIndex(t, repo, ctx))
	want := []string{"alpha-line", "zeta-line", "middle-line"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v (newest first, slug breaking the tie)", got, want)
	}

	// Republishing is publishing again, so it moves to the front.
	if _, err := publishUserService(ctx, repo, a.ID); err != nil {
		t.Fatalf("republish: %v", err)
	}
	got = indexSlugs(listIndex(t, repo, ctx))
	want = []string{"middle-line", "alpha-line", "zeta-line"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order after republish = %v, want %v", got, want)
	}
}

// The index is a list of cards. It must not read the draft's stops or vehicle
// documents, the publication's frozen routes, or the draft's prose. Asserted
// by running it as a role that can see only the columns a card needs, so
// Postgres itself refuses the read if it names any other.
func TestListPublishedServiceSummariesReadsOnlyCardColumns(t *testing.T) {
	repo, ctx, dbURL := userServiceFixture(t)
	svc := indexedService(idxServiceA, "card-line", "Card Line")
	createIndexedServices(t, repo, ctx, svc)
	publishIndexed(t, repo, ctx, svc.ID, idxJobA)

	role := fmt.Sprintf("spa_index_reader_%d", time.Now().UnixNano())
	exec(t, dbURL,
		`CREATE ROLE `+role+` LOGIN PASSWORD 'index-reader'`,
		`GRANT SELECT (id, slug) ON user_services TO `+role,
		`GRANT SELECT (user_service_id, name, subtext, description, published_at)
		   ON service_publications TO `+role)
	// Registered before the restricted pool's Close, so it runs after it:
	// the role's privileges live in this database, and DROP OWNED clears them
	// so the cluster-wide role can go.
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), dbURL)
		if err != nil {
			t.Logf("dropping %s: %v", role, err)
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		for _, stmt := range []string{`DROP OWNED BY ` + role, `DROP ROLE ` + role} {
			if _, err := conn.Exec(context.Background(), stmt); err != nil {
				t.Logf("%s: %v", stmt, err)
			}
		}
	})

	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatalf("parse %s: %v", dbURL, err)
	}
	u.User = url.UserPassword(role, "index-reader")
	restricted, err := postgres.Connect(ctx, u.String(), 0)
	if err != nil {
		t.Fatalf("Connect as %s: %v", role, err)
	}
	t.Cleanup(restricted.Close)

	got, err := restricted.ListPublishedServiceSummaries(ctx)
	if err != nil {
		t.Fatalf("ListPublishedServiceSummaries as a card-columns-only role: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "card-line" || got[0].Name != "Card Line" {
		t.Fatalf("index = %+v, want card-line", got)
	}

	// Without this the test would pass just as well if the grants above
	// restricted nothing.
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect as %s: %v", role, err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, q := range []string{
		`SELECT stops FROM user_services`,
		`SELECT vehicle FROM user_services`,
		`SELECT name FROM user_services`,
		`SELECT routes FROM service_publications`,
	} {
		var pgErr *pgconn.PgError
		_, err := conn.Exec(ctx, q)
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s as %s: err = %v, want insufficient_privilege", q, role, err)
		}
	}
}
