package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	prerenderedScenarioID = "00000000-0000-4001-8003-000000000001"
	prerenderedSlug       = "prerendered-test-scenario"
	prerenderedEntryA     = "00000000-0000-400c-8003-000000000001"
	prerenderedEntryB     = "00000000-0000-400c-8003-000000000002"
)

func bigPayload(t *testing.T, marker string) json.RawMessage {
	t.Helper()
	coords := make([][]float64, 0, 4096)
	for i := range 4096 {
		coords = append(coords, []float64{-121.9 + float64(i)*1e-6, 37.3 + float64(i)*1e-6})
	}
	b, err := json.Marshal(map[string]any{"marker": marker, "coordinates": coords})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

func rewindPrerenderedIsochronesMigration(t *testing.T, url string) {
	t.Helper()
	rewindBoardingWaitOverrideMigration(t, url)
	exec(t, url,
		`DROP TABLE IF EXISTS prerendered_isochrones`)
	rewindTo(t, url, 19)
}

func TestPrerenderedIsochronesMigrationIsSafeToReRun(t *testing.T) {
	repo, url := freshRepo(t)
	seedPrerenderedScenario(t, repo)

	entry := transit.PrerenderedIsochrone{
		ID: prerenderedEntryA, ScenarioSlug: prerenderedSlug, Label: "survivor",
		Lat: 37.3, Lng: -121.9, BudgetMins: 30, Mode: transit.TravelModeWalk,
		Result: json.RawMessage(`{"opaque":true}`),
	}
	if err := repo.CreatePrerenderedIsochrone(context.Background(), &entry); err != nil {
		t.Fatalf("CreatePrerenderedIsochrone: %v", err)
	}

	// Unrecord the version without dropping the table, so the next Migrate
	// re-runs 00019 over the schema it already created. 00020 must be forgotten
	// first — goose refuses to re-apply 19 while a later version is recorded.
	rewindBoardingWaitOverrideMigration(t, url)
	rewindTo(t, url, 19)
	if err := postgres.Migrate(context.Background(), url); err == nil {
		t.Error("re-running 00019 over its own table succeeded; CREATE TABLE should have refused it")
	}

	// The rewind the rest of this package's tests rely on must leave the
	// database in a state Migrate can bring forward again.
	rewindPrerenderedIsochronesMigration(t, url)
	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate after rewinding 00019: %v", err)
	}
	if _, ok, err := repo.GetPrerenderedIsochrone(context.Background(), entry.ID); err != nil {
		t.Fatalf("GetPrerenderedIsochrone after re-migrate: %v", err)
	} else if ok {
		t.Error("the rewind dropped the table; the entry should not have come back")
	}
}

func seedPrerenderedScenario(t *testing.T, repo interface {
	CreateScenario(context.Context, transit.Scenario) error
}) {
	t.Helper()
	if err := repo.CreateScenario(context.Background(), transit.Scenario{
		ID: prerenderedScenarioID, Slug: prerenderedSlug, Name: "Prerendered Fixture",
	}); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}
}

func TestPrerenderedIsochronesRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedPrerenderedScenario(t, repo)

	entry := transit.PrerenderedIsochrone{
		ID:                 prerenderedEntryA,
		ScenarioSlug:       prerenderedSlug,
		Label:              "San Jose — 240 min by bike",
		Lat:                37.3297,
		Lng:                -121.9020,
		BudgetMins:         240,
		Mode:               transit.TravelModeBike,
		Result:             bigPayload(t, "sj-240-bike"),
		CompiledServiceIDs: []string{"00000000-0000-4002-8003-000000000001"},
	}
	if err := repo.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
		t.Fatalf("CreatePrerenderedIsochrone: %v", err)
	}
	// The insert fills in the database-assigned timestamps, because the 201
	// carries the row straight back to the caller.
	if entry.CreatedAt.IsZero() || entry.UpdatedAt.IsZero() {
		t.Error("CreatePrerenderedIsochrone left the timestamps unset")
	}

	got, ok, err := repo.GetPrerenderedIsochrone(ctx, entry.ID)
	if err != nil || !ok {
		t.Fatalf("GetPrerenderedIsochrone: ok=%v err=%v", ok, err)
	}
	if got.ScenarioSlug != prerenderedSlug || got.Label != entry.Label {
		t.Errorf("round trip = %+v", got)
	}
	if got.Mode != transit.TravelModeBike || got.BudgetMins != 240 {
		t.Errorf("mode/budget = %v/%d", got.Mode, got.BudgetMins)
	}
	if len(got.CompiledServiceIDs) != 1 || got.CompiledServiceIDs[0] != entry.CompiledServiceIDs[0] {
		t.Errorf("compiled_service_ids = %v, want the snapshot that was written", got.CompiledServiceIDs)
	}

	var want, have any
	if err := json.Unmarshal(entry.Result, &want); err != nil {
		t.Fatalf("unmarshal written payload: %v", err)
	}
	if err := json.Unmarshal(got.Result, &have); err != nil {
		t.Fatalf("unmarshal read payload: %v", err)
	}
	wantJSON, _ := json.Marshal(want)
	haveJSON, _ := json.Marshal(have)
	if string(wantJSON) != string(haveJSON) {
		t.Error("the stored payload is not what was written")
	}

	if _, ok, err := repo.GetPrerenderedIsochrone(ctx, "00000000-0000-400c-8003-0000000000ff"); err != nil || ok {
		t.Errorf("unknown id: ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestPrerenderedIsochronesListOmitsPayloads(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedPrerenderedScenario(t, repo)

	for _, e := range []struct {
		id, label, marker string
	}{
		{prerenderedEntryA, "first", "a"},
		{prerenderedEntryB, "second", "b"},
	} {
		entry := transit.PrerenderedIsochrone{
			ID: e.id, ScenarioSlug: prerenderedSlug, Label: e.label,
			Lat: 37.3, Lng: -121.9, BudgetMins: 30, Mode: transit.TravelModeWalk,
			Result: bigPayload(t, e.marker),
		}
		if err := repo.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
			t.Fatalf("CreatePrerenderedIsochrone(%s): %v", e.id, err)
		}
	}

	list, err := repo.ListPrerenderedIsochronesByScenario(ctx, prerenderedSlug)
	if err != nil {
		t.Fatalf("ListPrerenderedIsochronesByScenario: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d entries, want 2", len(list))
	}
	for _, p := range list {
		if len(p.Result) != 0 {
			t.Errorf("entry %s carries a %d-byte payload; the list query must not select it",
				p.ID, len(p.Result))
		}
		if p.Label == "" || p.BudgetMins == 0 || p.CreatedAt.IsZero() {
			t.Errorf("entry %s lost metadata the list is for: %+v", p.ID, p)
		}
		// NOT NULL DEFAULT '{}' means an unset snapshot reads back as empty,
		// never as a nil the caller has to tell apart from "we never recorded it".
		if p.CompiledServiceIDs == nil {
			t.Errorf("entry %s: compiled_service_ids came back nil, want an empty array", p.ID)
		}
	}

	// A scenario with nothing curated lists as an empty slice, not nil.
	other, err := repo.ListPrerenderedIsochronesByScenario(ctx, "no-such-scenario")
	if err != nil {
		t.Fatalf("ListPrerenderedIsochronesByScenario (unknown): %v", err)
	}
	if other == nil || len(other) != 0 {
		t.Errorf("unknown scenario = %v, want an empty slice", other)
	}
}

func TestPrerenderedIsochronesCascadeOnScenarioDelete(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedPrerenderedScenario(t, repo)

	entry := transit.PrerenderedIsochrone{
		ID: prerenderedEntryA, ScenarioSlug: prerenderedSlug, Label: "doomed",
		Lat: 37.3, Lng: -121.9, BudgetMins: 30, Mode: transit.TravelModeWalk,
		Result: json.RawMessage(`{"opaque":true}`),
	}
	if err := repo.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
		t.Fatalf("CreatePrerenderedIsochrone: %v", err)
	}
	if _, ok, err := repo.GetPrerenderedIsochrone(ctx, entry.ID); err != nil || !ok {
		t.Fatalf("precondition: ok=%v err=%v", ok, err)
	}

	// There is no DeleteScenario on the repository — a seeded scenario is not
	// something the API removes — so the delete is issued directly. The
	// cascade is the database's behaviour, and that is what is under test.
	exec(t, url, `DELETE FROM scenarios WHERE slug = '`+prerenderedSlug+`'`)

	if _, ok, err := repo.GetPrerenderedIsochrone(ctx, entry.ID); err != nil {
		t.Fatalf("GetPrerenderedIsochrone after delete: %v", err)
	} else if ok {
		t.Error("the scenario was deleted and its prerendered isochrone survived")
	}

	list, err := repo.ListPrerenderedIsochronesByScenario(ctx, prerenderedSlug)
	if err != nil {
		t.Fatalf("ListPrerenderedIsochronesByScenario: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d entries after the scenario was deleted, want 0", len(list))
	}
}

func TestPrerenderedIsochroneRejectsUnknownScenarioSlug(t *testing.T) {
	repo, _ := freshRepo(t)
	entry := transit.PrerenderedIsochrone{
		ID: prerenderedEntryA, ScenarioSlug: "no-such-scenario", Label: "orphan",
		Lat: 1, Lng: 2, BudgetMins: 30, Mode: transit.TravelModeWalk,
		Result: json.RawMessage(`{}`),
	}
	if err := repo.CreatePrerenderedIsochrone(context.Background(), &entry); err == nil {
		t.Error("an entry was stored against a scenario slug with no scenario behind it")
	}
}

func TestListServiceMembershipByScenario(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}

	sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
	}

	members, err := repo.ListServiceMembershipByScenario(ctx, sc.ID)
	if err != nil {
		t.Fatalf("ListServiceMembershipByScenario: %v", err)
	}
	ids, err := repo.ListServiceIDsByScenario(ctx, sc.ID)
	if err != nil {
		t.Fatalf("ListServiceIDsByScenario: %v", err)
	}
	if len(members) != len(ids) {
		t.Fatalf("membership = %d rows, curated ids = %d: the two must read the same join",
			len(members), len(ids))
	}
	if len(members) == 0 {
		t.Fatal("the seeded scenario has no curated services")
	}
	for _, m := range members {
		if m.ServiceID == "" {
			t.Error("a membership row has no service id")
		}
		if m.UpdatedAt.IsZero() {
			t.Errorf("member %s has a zero updated_at; staleness would never fire on it", m.ServiceID)
		}
	}

	// A freshly seeded scenario cannot make a just-curated entry outdated.
	// "Just curated" means after every member's updated_at, not after the
	// first one's — the seed writes them in one pass and their timestamps
	// differ by microseconds in whatever order the inserts landed.
	latest := members[0].UpdatedAt
	for _, m := range members[1:] {
		if m.UpdatedAt.After(latest) {
			latest = m.UpdatedAt
		}
	}
	entry := transit.PrerenderedIsochrone{
		CompiledServiceIDs: transit.MembershipIDs(members),
		CreatedAt:          latest.Add(time.Millisecond),
	}
	if transit.PrerenderedOutdated(entry, members) {
		t.Error("an entry snapshotted from this very membership reports outdated")
	}
}
