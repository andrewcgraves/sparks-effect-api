package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestReconcileSeedRestoresADriftedSeededStation(t *testing.T) {
	repo, _ := freshRepo(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after seed: %v", err)
	}
	if written != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows on a just-seeded database, want 0", written)
	}

	sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
	}
	st, ok, err := repo.GetStationBySlug(ctx, sc.ID, "las-vegas")
	if err != nil || !ok {
		t.Fatalf("GetStationBySlug las-vegas: ok=%v err=%v", ok, err)
	}
	want := st.Location
	st.Location = transit.GeoPoint{Type: "Point", Coordinates: []float64{-115.136, 36.174}}
	if err := repo.UpdateStation(ctx, st); err != nil {
		t.Fatalf("UpdateStation: %v", err)
	}

	written, err = transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after drift: %v", err)
	}
	if written == 0 {
		t.Fatal("ReconcileSeed wrote nothing; the YAML correction did not reach the database")
	}

	got, ok, err := repo.GetStationBySlug(ctx, sc.ID, "las-vegas")
	if err != nil || !ok {
		t.Fatalf("GetStationBySlug after reconcile: ok=%v err=%v", ok, err)
	}
	if len(got.Location.Coordinates) != 2 ||
		got.Location.Coordinates[0] != want.Coordinates[0] ||
		got.Location.Coordinates[1] != want.Coordinates[1] {
		t.Errorf("las-vegas location = %v, want %v", got.Location.Coordinates, want.Coordinates)
	}

	again, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after restore: %v", err)
	}
	if again != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows after restore, want 0", again)
	}
}

func TestReconcileSeedFillsAnExistingScenarioSubtext(t *testing.T) {
	repo, _ := freshRepo(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
	}
	const want = "Electrified · High-speed rail · Greenfield"
	if sc.Subtext != want {
		t.Fatalf("seeded subtext = %q, want %q", sc.Subtext, want)
	}

	// A database seeded before the column existed holds the column default.
	sc.Subtext = ""
	if err := repo.UpdateScenario(ctx, sc); err != nil {
		t.Fatalf("UpdateScenario: %v", err)
	}

	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written != 1 {
		t.Fatalf("ReconcileSeed wrote %d rows, want 1 (the scenario)", written)
	}
	got, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug after reconcile: ok=%v err=%v", ok, err)
	}
	if got.Subtext != want {
		t.Errorf("subtext after reconcile = %q, want %q", got.Subtext, want)
	}
}

func TestReconcileSeedDoesNotClobberAnAuthoredScenario(t *testing.T) {
	repo, _ := freshRepo(t)
	ctx := context.Background()

	owner := account.User{ID: "00000000-0000-4007-8009-000000000001", Email: "author@example.com", Name: "Author"}
	if err := repo.CreateUser(ctx, owner, ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerID := owner.ID
	sc := transit.Scenario{
		ID: "00000000-0000-4001-8009-000000000001", Slug: "authored",
		Name: "Mine", OwnerID: &ownerID,
	}
	if err := repo.CreateScenario(ctx, sc); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}

	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written == 0 {
		t.Fatal("expected the seeded ca-hsr scenario to be inserted alongside the authored one")
	}

	got, ok, err := repo.GetScenarioBySlug(ctx, "authored")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug authored: ok=%v err=%v", ok, err)
	}
	if got.Name != "Mine" {
		t.Errorf("authored name = %q, want Mine", got.Name)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerID {
		t.Errorf("authored owner = %v, want %s", got.OwnerID, ownerID)
	}
}
