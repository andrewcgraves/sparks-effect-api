package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// The curated reads are the containment boundary for owned content: LoadStore
// compiles everything ListCuratedScenarios returns into the public in-memory
// store, and ListCuratedRouteSummaries is the unauthenticated route picker. A
// leak here is not a wrong list, it is publishing someone's private work.

const (
	ownershipOwnerID    = "00000000-0000-4900-8000-000000000001"
	ownershipCuratedID  = "00000000-0000-4901-8000-000000000001"
	ownershipOwnedScID  = "00000000-0000-4901-8000-000000000002"
	ownershipCuratedRt  = "00000000-0000-4902-8000-000000000001"
	ownershipOwnedRtID  = "00000000-0000-4902-8000-000000000002"
	ownershipCuratedStn = "00000000-0000-4903-8000-000000000001"
)

// ownershipFixture writes one curated scenario and one owned scenario, each
// with a route of its own, and returns the repo.
func ownershipFixture(t *testing.T) (repo interface {
	ListCuratedScenarios(context.Context) ([]transit.Scenario, error)
	ListCuratedRouteSummaries(context.Context) ([]transit.RouteSummary, error)
	ListRouteSummariesByOwner(context.Context, string) ([]transit.RouteSummary, error)
	ListScenariosByOwner(context.Context, string) ([]transit.Scenario, error)
}, url string) {
	t.Helper()
	r, u := freshRepo(t)
	ctx := context.Background()

	mustCreateUser(t, r, transit.User{ID: ownershipOwnerID, Email: "owner@example.com"}, "pw")

	for _, sc := range []transit.Scenario{
		{ID: ownershipCuratedID, Slug: "curated-baseline", Name: "Curated Baseline"},
		{ID: ownershipOwnedScID, Slug: "owned-draft", Name: "Owned Draft", OwnerID: ptr(ownershipOwnerID)},
	} {
		if err := r.CreateScenario(ctx, sc); err != nil {
			t.Fatalf("CreateScenario %s: %v", sc.Slug, err)
		}
	}

	for _, rt := range []transit.Route{
		{
			ID: ownershipCuratedRt, ScenarioID: ptr(ownershipCuratedID),
			Slug: "curated-alignment", Name: "Curated Alignment", Mode: "rail",
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-121.9, 37.3}, {-121.8, 37.4}}},
		},
		{
			ID: ownershipOwnedRtID, ScenarioID: ptr(ownershipOwnedScID),
			OwnerID: ptr(ownershipOwnerID),
			Slug:    "owned-alignment", Name: "Owned Alignment",
			Description: "a draft", Mode: "rail",
			Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122.1, 37.7}, {-122.0, 37.8}}},
		},
	} {
		if err := r.CreateRoute(ctx, rt); err != nil {
			t.Fatalf("CreateRoute %s: %v", rt.Slug, err)
		}
	}

	return r, u
}

func TestListCuratedScenariosExcludesOwnedRows(t *testing.T) {
	repo, _ := ownershipFixture(t)

	got, err := repo.ListCuratedScenarios(context.Background())
	if err != nil {
		t.Fatalf("ListCuratedScenarios: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "curated-baseline" {
		t.Fatalf("want only the curated scenario, got %+v", slugsOf(got))
	}
}

func TestListScenariosByOwnerIsTheComplement(t *testing.T) {
	repo, _ := ownershipFixture(t)

	got, err := repo.ListScenariosByOwner(context.Background(), ownershipOwnerID)
	if err != nil {
		t.Fatalf("ListScenariosByOwner: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "owned-draft" {
		t.Fatalf("want only the owned scenario, got %+v", slugsOf(got))
	}
}

func TestListCuratedRouteSummariesExcludesOwnedRoutes(t *testing.T) {
	repo, _ := ownershipFixture(t)

	got, err := repo.ListCuratedRouteSummaries(context.Background())
	if err != nil {
		t.Fatalf("ListCuratedRouteSummaries: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "curated-alignment" {
		t.Fatalf("want only the curated route, got %+v", got)
	}
}

func TestListRouteSummariesByOwnerReturnsOnlyTheCallersRoutes(t *testing.T) {
	repo, _ := ownershipFixture(t)

	got, err := repo.ListRouteSummariesByOwner(context.Background(), ownershipOwnerID)
	if err != nil {
		t.Fatalf("ListRouteSummariesByOwner: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "owned-alignment" {
		t.Fatalf("want only the owned route, got %+v", got)
	}
	// The summary carries description so an owner's own list reads as more
	// than a slug; curated summaries leave it empty.
	if got[0].Description != "a draft" {
		t.Errorf("description: want %q, got %q", "a draft", got[0].Description)
	}
}

// Deleting an account must take its content with it. Under SET NULL the rows
// would survive as unowned — which now means curated and public.
func TestDeletingUserCascadesToOwnedDomainRows(t *testing.T) {
	repo, url := freshRepo(t)
	ctx := context.Background()

	mustCreateUser(t, repo, transit.User{ID: ownershipOwnerID, Email: "owner@example.com"}, "pw")
	if err := repo.CreateScenario(ctx, transit.Scenario{
		ID: ownershipOwnedScID, Slug: "owned-draft", Name: "Owned Draft",
		OwnerID: ptr(ownershipOwnerID),
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}

	exec(t, url, `DELETE FROM users WHERE id = '`+ownershipOwnerID+`'`)

	if got := scalarCount(t, url, `SELECT count(*) FROM scenarios WHERE id = '`+ownershipOwnedScID+`'`); got != 0 {
		t.Errorf("owned scenario after its owner was deleted: want it gone, got %d rows", got)
	}
}

func slugsOf(scenarios []transit.Scenario) []string {
	out := make([]string, len(scenarios))
	for i, sc := range scenarios {
		out[i] = sc.Slug
	}
	return out
}
