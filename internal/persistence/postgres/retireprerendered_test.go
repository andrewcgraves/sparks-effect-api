package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

var retiredPrerenderedIDs = []string{
	"00000000-0000-4006-8001-000000000001", // San Jose - 240 min by bike
	"00000000-0000-4006-8001-000000000002", // Burbank Airport - 240 min on foot
}

const keptPrerenderedID = "00000000-0000-4006-8001-000000000003"

func insertCaHSRPrerendered(t *testing.T, repo *postgres.Repo, url string) {
	t.Helper()
	exec(t, url,
		`INSERT INTO scenarios (id, slug, name) VALUES ('`+phase1ScenarioID+`', 'ca-hsr', 'CA HSR')`)
	for _, id := range append(append([]string{}, retiredPrerenderedIDs...), keptPrerenderedID) {
		entry := transit.PrerenderedIsochrone{
			ID: id, ScenarioSlug: "ca-hsr", Label: id,
			Lat: 37.3, Lng: -121.9, BudgetMins: 60, Mode: transit.TravelModeBike,
			Result: json.RawMessage(`{"opaque":true}`),
		}
		if err := repo.CreatePrerenderedIsochrone(context.Background(), &entry); err != nil {
			t.Fatalf("CreatePrerenderedIsochrone %s: %v", id, err)
		}
	}
}

func assertOnlyKeptPrerendered(t *testing.T, url string) {
	t.Helper()
	for _, id := range retiredPrerenderedIDs {
		if got := scalarCount(t, url,
			`SELECT count(*) FROM prerendered_isochrones WHERE id = '`+id+`'`); got != 0 {
			t.Errorf("retired prerendered isochrone %s survived the migration", id)
		}
	}
	if got := scalarCount(t, url,
		`SELECT count(*) FROM prerendered_isochrones WHERE id = '`+keptPrerenderedID+`'`); got != 1 {
		t.Error("the migration deleted a prerendered isochrone it does not name")
	}
}

func TestRetirePrerenderedMigrationDeletesTheTwoOutdatedEntries(t *testing.T) {
	repo, url := freshRepo(t)
	insertCaHSRPrerendered(t, repo, url)

	rewindTo(t, url, 35)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertOnlyKeptPrerendered(t, url)
}

func TestRetirePrerenderedMigrationIsSafeToReRun(t *testing.T) {
	repo, url := freshRepo(t)
	insertCaHSRPrerendered(t, repo, url)

	for pass := 1; pass <= 2; pass++ {
		rewindTo(t, url, 35)
		if err := postgres.Migrate(context.Background(), url); err != nil {
			t.Fatalf("Migrate pass %d: %v", pass, err)
		}
	}
	assertOnlyKeptPrerendered(t, url)
}
