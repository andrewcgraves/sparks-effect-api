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
	idxServiceD = "00000000-0000-4008-8003-000000000014"
	idxServiceE = "00000000-0000-4008-8003-000000000015"
	idxJobA     = "00000000-0000-400a-8003-000000000011"
	idxJobB     = "00000000-0000-400a-8003-000000000012"
	idxJobC     = "00000000-0000-400a-8003-000000000013"
	idxJobD     = "00000000-0000-400a-8003-000000000014"
	idxJobE     = "00000000-0000-400a-8003-000000000015"
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
	page, err := repo.ListPublishedServiceSummaries(ctx, nil, 0)
	if err != nil {
		t.Fatalf("ListPublishedServiceSummaries: %v", err)
	}
	if page.Next != nil {
		t.Fatalf("unlimited read returned a next key %+v", page.Next)
	}
	return page.Items
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

	pub, found, err := repo.GetServicePublication(ctx, published.ID)
	if err != nil || !found {
		t.Fatalf("GetServicePublication: found=%v err=%v", found, err)
	}
	want := transit.PublishedServiceSummary{
		Slug: "published-line", Name: "Published Line",
		Subtext: "Published Line subtext", Description: "Published Line description",
		AuthorName: "Owner", PublishedAt: pub.PublishedAt,
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

func TestListPublishedServiceSummariesOrdersMostRecentlyFirstPublishedFirst(t *testing.T) {
	repo, ctx, dbURL := userServiceFixture(t)
	a := indexedService(idxServiceA, "middle-line", "Middle Line")
	b := indexedService(idxServiceB, "zeta-line", "Zeta Line")
	c := indexedService(idxServiceC, "alpha-line", "Alpha Line")
	createIndexedServices(t, repo, ctx, a, b, c)
	publishIndexed(t, repo, ctx, a.ID, idxJobA)
	publishIndexed(t, repo, ctx, b.ID, idxJobB)
	publishIndexed(t, repo, ctx, c.ID, idxJobC)

	// Pin the first-publish times so the order does not depend on how quickly
	// the three publishes above ran. b and c share an instant. All three are
	// in the past, so the republish below lands after them.
	pinFirstPublished(t, dbURL, map[string]string{
		a.ID: "2001-01-01T00:00:00Z",
		b.ID: "2001-02-01T00:00:00Z",
		c.ID: "2001-02-01T00:00:00Z",
	})

	got := indexSlugs(listIndex(t, repo, ctx))
	want := []string{"alpha-line", "zeta-line", "middle-line"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v (newest first, slug breaking the tie)", got, want)
	}

	// Republishing keeps a service's place. A cursor walk depends on it: a
	// service that jumped ahead of the cursor would be skipped (SPA-434).
	if _, err := publishUserService(ctx, repo, a.ID); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if got := indexSlugs(listIndex(t, repo, ctx)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order after republish = %v, want unchanged %v", got, want)
	}
}

// A card's date is the latest publish, the same instant the public page shows,
// while its place in the index stays at the first. Its byline is joined on
// read, so a rename or a transfer reaches it without a republish.
func TestListPublishedServiceSummariesCardDateAndBylineAreCurrent(t *testing.T) {
	repo, ctx, dbURL := userServiceFixture(t)
	svc := indexedService(idxServiceA, "card-line", "Card Line")
	createIndexedServices(t, repo, ctx, svc)
	publishIndexed(t, repo, ctx, svc.ID, idxJobA)
	pinFirstPublished(t, dbURL, map[string]string{svc.ID: "2001-01-01T00:00:00Z"})

	republished, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	card := func() transit.PublishedServiceSummary {
		t.Helper()
		got := listIndex(t, repo, ctx)
		if len(got) != 1 {
			t.Fatalf("index = %+v, want one card", got)
		}
		return got[0]
	}
	if got := card(); !got.PublishedAt.Equal(republished.PublishedAt) || got.AuthorName != "Owner" {
		t.Fatalf("card = %+v, want published_at %v by Owner", got, republished.PublishedAt)
	}

	if _, _, err := repo.UpdateUserName(ctx, usOwnerID, "Renamed Owner"); err != nil {
		t.Fatalf("UpdateUserName: %v", err)
	}
	if got := card().AuthorName; got != "Renamed Owner" {
		t.Fatalf("author_name after rename = %q, want %q", got, "Renamed Owner")
	}
	execSQL(t, dbURL, `UPDATE user_services SET owner_id = $1 WHERE id = $2`, usStrangerID, svc.ID)
	if got := card().AuthorName; got != "Stranger" {
		t.Fatalf("author_name after a transfer = %q, want %q", got, "Stranger")
	}
}

func TestListPublishedServiceSummariesPagesThroughEveryServiceOnce(t *testing.T) {
	repo, ctx, dbURL := userServiceFixture(t)
	svcs := []transit.UserService{
		indexedService(idxServiceA, "line-a", "Line A"),
		indexedService(idxServiceB, "line-b", "Line B"),
		indexedService(idxServiceC, "line-c", "Line C"),
		indexedService(idxServiceD, "line-d", "Line D"),
		indexedService(idxServiceE, "line-e", "Line E"),
	}
	createIndexedServices(t, repo, ctx, svcs...)
	for i, job := range []string{idxJobA, idxJobB, idxJobC, idxJobD, idxJobE} {
		publishIndexed(t, repo, ctx, svcs[i].ID, job)
	}
	// e is newest; c and d share an instant, so a page boundary falls inside
	// a tie and slug must break it.
	pinFirstPublished(t, dbURL, map[string]string{
		svcs[0].ID: "2001-01-01T00:00:00Z",
		svcs[1].ID: "2001-02-01T00:00:00Z",
		svcs[2].ID: "2001-03-01T00:00:00Z",
		svcs[3].ID: "2001-03-01T00:00:00Z",
		svcs[4].ID: "2001-04-01T00:00:00Z",
	})

	var walked []string
	var after *transit.PublishedIndexKey
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatalf("walk did not end; so far %v", walked)
		}
		page, err := repo.ListPublishedServiceSummaries(ctx, after, 2)
		if err != nil {
			t.Fatalf("ListPublishedServiceSummaries page %d: %v", pages, err)
		}
		if len(page.Items) > 2 {
			t.Fatalf("page %d has %d items, want at most 2", pages, len(page.Items))
		}
		walked = append(walked, indexSlugs(page.Items)...)
		if pages == 0 {
			// line-a has not been reached yet. Republishing it must not move
			// it ahead of the cursor, where the rest of the walk would miss it.
			if _, err := publishUserService(ctx, repo, svcs[0].ID); err != nil {
				t.Fatalf("republish mid-walk: %v", err)
			}
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}

	want := []string{"line-e", "line-c", "line-d", "line-b", "line-a"}
	if fmt.Sprint(walked) != fmt.Sprint(want) {
		t.Fatalf("walk = %v, want every service once in order %v", walked, want)
	}
}

func TestListPublishedServiceSummariesEndsWithoutAnEmptyPage(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	a := indexedService(idxServiceA, "line-a", "Line A")
	b := indexedService(idxServiceB, "line-b", "Line B")
	createIndexedServices(t, repo, ctx, a, b)
	publishIndexed(t, repo, ctx, a.ID, idxJobA)
	publishIndexed(t, repo, ctx, b.ID, idxJobB)

	page, err := repo.ListPublishedServiceSummaries(ctx, nil, 2)
	if err != nil {
		t.Fatalf("ListPublishedServiceSummaries: %v", err)
	}
	if len(page.Items) != 2 || page.Next != nil {
		t.Fatalf("page = %d items, next %+v; want both items and no next key", len(page.Items), page.Next)
	}
}

func pinFirstPublished(t *testing.T, dbURL string, at map[string]string) {
	t.Helper()
	stmts := make([]string, 0, len(at))
	for id, ts := range at {
		stmts = append(stmts, `UPDATE service_publications SET first_published_at = '`+ts+`' WHERE user_service_id = '`+id+`'`)
	}
	exec(t, dbURL, stmts...)
}

// The index is a list of cards. It must not read the draft's stops or vehicle
// documents, the publication's frozen routes, the draft's prose, or anything
// of the author's but their display name. Asserted
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
		`GRANT SELECT (id, slug, owner_id) ON user_services TO `+role,
		`GRANT SELECT (id, name) ON users TO `+role,
		`GRANT SELECT (user_service_id, name, subtext, description, published_at, first_published_at)
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

	page, err := restricted.ListPublishedServiceSummaries(ctx, nil, 1)
	if err != nil {
		t.Fatalf("ListPublishedServiceSummaries as a card-columns-only role: %v", err)
	}
	if got := page.Items; len(got) != 1 || got[0].Slug != "card-line" || got[0].Name != "Card Line" {
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
		`SELECT email FROM users`,
	} {
		var pgErr *pgconn.PgError
		_, err := conn.Exec(ctx, q)
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s as %s: err = %v, want insufficient_privilege", q, role, err)
		}
	}
}
