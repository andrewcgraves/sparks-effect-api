package transit_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakePrerenderedSeedStore is an in-memory transit.PrerenderedSeedStore.
//
// It counts creates rather than only recording them, because the property that
// matters most here is not what ends up stored but how many times the seeder
// tried to store it: idempotency is a statement about writes, and a store that
// merely overwrote by id would look identical from the outside.
type fakePrerenderedSeedStore struct {
	scenarios []transit.Scenario
	members   map[string][]transit.ServiceMembership // by scenario id
	entries   map[string]transit.PrerenderedIsochrone

	creates int
	// listedFor records which scenario slugs were listed, so a test can assert
	// a scenario with no prerendered/ directory was skipped before any query.
	listedFor []string
}

func newFakePrerenderedSeedStore() *fakePrerenderedSeedStore {
	return &fakePrerenderedSeedStore{
		scenarios: []transit.Scenario{{ID: "scenario-1", Slug: "ca-hsr"}},
		members: map[string][]transit.ServiceMembership{
			"scenario-1": {
				{ServiceID: "svc-1", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				{ServiceID: "svc-2", UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			},
		},
		entries: map[string]transit.PrerenderedIsochrone{},
	}
}

func (f *fakePrerenderedSeedStore) ListScenarios(context.Context) ([]transit.Scenario, error) {
	return f.scenarios, nil
}

func (f *fakePrerenderedSeedStore) ListServiceMembershipByScenario(_ context.Context, scenarioID string) ([]transit.ServiceMembership, error) {
	return f.members[scenarioID], nil
}

func (f *fakePrerenderedSeedStore) ListPrerenderedIsochronesByScenario(_ context.Context, slug string) ([]transit.PrerenderedIsochrone, error) {
	f.listedFor = append(f.listedFor, slug)
	var out []transit.PrerenderedIsochrone
	for _, p := range f.entries {
		if p.ScenarioSlug == slug {
			// The real list query does not select result; mirroring that here
			// keeps the seeder honest about only needing ids from it.
			p.Result = nil
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakePrerenderedSeedStore) CreatePrerenderedIsochrone(_ context.Context, p *transit.PrerenderedIsochrone) error {
	f.creates++
	p.CreatedAt = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	p.UpdatedAt = p.CreatedAt
	f.entries[p.ID] = *p
	return nil
}

// seedFile writes one prerendered seed file into a MapFS.
func seedFile(id, label, mode string, budget int, payload string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{
		"id": "` + id + `",
		"label": "` + label + `",
		"lat": 37.3297,
		"lng": -121.9020,
		"budget_mins": ` + strconv.Itoa(budget) + `,
		"mode": "` + mode + `",
		"result": ` + payload + `
	}`)}
}

func twoEntryFS() fstest.MapFS {
	return fstest.MapFS{
		"data/scenarios/ca-hsr/prerendered/isochrone-sj-240-bike.json": seedFile(
			"11111111-1111-4111-8111-111111111111", "San Jose — 240 min by bike", "bike", 240,
			`{"contours":[{"minutes":240}]}`),
		"data/scenarios/ca-hsr/prerendered/isochrone-burbank-walk.json": seedFile(
			"22222222-2222-4222-8222-222222222222", "Burbank walk reach", "walk", 45,
			`{"contours":[{"minutes":45}]}`),
		// A non-JSON file in the same directory must be ignored, not parsed —
		// the README documenting the format lives here in the real tree.
		"data/scenarios/ca-hsr/prerendered/README.md": &fstest.MapFile{Data: []byte("# not a payload")},
	}
}

func TestSeedPrerenderedIsochrones_seedsFromFiles(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	if err := transit.SeedPrerenderedIsochrones(context.Background(), twoEntryFS(), store); err != nil {
		t.Fatalf("SeedPrerenderedIsochrones: %v", err)
	}

	if store.creates != 2 {
		t.Fatalf("creates = %d, want 2 (the README must not be read as a payload)", store.creates)
	}

	got, ok := store.entries["11111111-1111-4111-8111-111111111111"]
	if !ok {
		t.Fatal("the San Jose entry was not seeded under the id its file names")
	}
	if got.ScenarioSlug != "ca-hsr" {
		t.Errorf("scenario_slug = %q, want ca-hsr", got.ScenarioSlug)
	}
	if got.Label != "San Jose — 240 min by bike" {
		t.Errorf("label = %q", got.Label)
	}
	if got.Mode != transit.TravelModeBike || got.BudgetMins != 240 {
		t.Errorf("mode/budget = %v/%d, want bike/240", got.Mode, got.BudgetMins)
	}
	if got.Lat != 37.3297 || got.Lng != -121.9020 {
		t.Errorf("origin = %v,%v", got.Lat, got.Lng)
	}

	// The payload reaches storage as authored — the seeder parses the envelope
	// around it and nothing inside it.
	var payload map[string]any
	if err := json.Unmarshal(got.Result, &payload); err != nil {
		t.Fatalf("stored payload is not the JSON from the file: %v", err)
	}
	if _, present := payload["contours"]; !present {
		t.Errorf("stored payload = %s, want the file's own object", got.Result)
	}
}

// The snapshot must be the scenario's real membership. Leaving it empty would
// make every seeded entry report itself outdated on its very first read, since
// an empty set does not match a scenario that has services.
func TestSeedPrerenderedIsochrones_snapshotsServiceIDs(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	if err := transit.SeedPrerenderedIsochrones(context.Background(), twoEntryFS(), store); err != nil {
		t.Fatalf("SeedPrerenderedIsochrones: %v", err)
	}

	for id, entry := range store.entries {
		got := entry.CompiledServiceIDs
		if len(got) != 2 || got[0] != "svc-1" || got[1] != "svc-2" {
			t.Fatalf("entry %s: compiled_service_ids = %v, want the scenario's members", id, got)
		}
		if transit.PrerenderedOutdated(entry, store.members["scenario-1"]) {
			t.Errorf("entry %s reports outdated the moment it was seeded", id)
		}
	}
}

// This runs on every boot, so the second run — and every run after it — must
// write nothing at all.
func TestSeedPrerenderedIsochrones_secondRunIsANoOp(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	fsys := twoEntryFS()
	ctx := context.Background()

	if err := transit.SeedPrerenderedIsochrones(ctx, fsys, store); err != nil {
		t.Fatalf("first run: %v", err)
	}
	after := store.creates

	for i := range 3 {
		if err := transit.SeedPrerenderedIsochrones(ctx, fsys, store); err != nil {
			t.Fatalf("run %d: %v", i+2, err)
		}
	}
	if store.creates != after {
		t.Errorf("creates = %d after re-running, want %d: the id in each file is the identity to skip on",
			store.creates, after)
	}
	if len(store.entries) != 2 {
		t.Errorf("entries = %d, want 2", len(store.entries))
	}
}

// A file added later is picked up on the next boot without disturbing what is
// already stored — the point of being idempotent per id rather than per run.
func TestSeedPrerenderedIsochrones_seedsOnlyWhatIsNew(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	ctx := context.Background()
	if err := transit.SeedPrerenderedIsochrones(ctx, twoEntryFS(), store); err != nil {
		t.Fatalf("first run: %v", err)
	}

	fsys := twoEntryFS()
	fsys["data/scenarios/ca-hsr/prerendered/isochrone-fresno-drive.json"] = seedFile(
		"33333333-3333-4333-8333-333333333333", "Fresno drive reach", "drive", 60, `{"contours":[]}`)

	if err := transit.SeedPrerenderedIsochrones(ctx, fsys, store); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if store.creates != 3 {
		t.Errorf("creates = %d, want 3: only the new file should have been written", store.creates)
	}
}

// Most scenarios ship no curated isochrones. An absent prerendered/ directory
// is that statement, not a fault — and it must not even reach the store.
func TestSeedPrerenderedIsochrones_skipsScenarioWithNoPrerenderedDir(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	store.scenarios = append(store.scenarios,
		transit.Scenario{ID: "scenario-2", Slug: "brightline-west"})

	if err := transit.SeedPrerenderedIsochrones(context.Background(), twoEntryFS(), store); err != nil {
		t.Fatalf("SeedPrerenderedIsochrones: %v", err)
	}
	if store.creates != 2 {
		t.Errorf("creates = %d, want 2", store.creates)
	}
	for _, slug := range store.listedFor {
		if slug == "brightline-west" {
			t.Error("a scenario with no prerendered/ directory was queried anyway")
		}
	}
}

// An empty prerendered/ directory is the same no-op, and must not query either.
func TestSeedPrerenderedIsochrones_emptyDirIsANoOp(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	fsys := fstest.MapFS{
		"data/scenarios/ca-hsr/prerendered/README.md": &fstest.MapFile{Data: []byte("# nothing yet")},
	}

	if err := transit.SeedPrerenderedIsochrones(context.Background(), fsys, store); err != nil {
		t.Fatalf("SeedPrerenderedIsochrones: %v", err)
	}
	if store.creates != 0 {
		t.Errorf("creates = %d, want 0", store.creates)
	}
	if len(store.listedFor) != 0 {
		t.Errorf("listed %v, want nothing: there was nothing to compare against", store.listedFor)
	}
}

// This is repo-authored data, so a malformed file is a mistake to surface
// loudly at boot rather than a row to skip quietly.
func TestSeedPrerenderedIsochrones_rejectsMalformedFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantIn  string
	}{
		{"not json", `{`, "parsing"},
		{"no id", `{"label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":{}}`, "id is required"},
		{"blank label", `{"id":"x","label":"  ","lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":{}}`, "label is required"},
		{"bad mode", `{"id":"x","label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"teleport","result":{}}`, "mode"},
		{"zero budget", `{"id":"x","label":"L","lat":1,"lng":2,"budget_mins":0,"mode":"walk","result":{}}`, "budget_mins"},
		{"no result", `{"id":"x","label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"walk"}`, "result is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakePrerenderedSeedStore()
			fsys := fstest.MapFS{
				"data/scenarios/ca-hsr/prerendered/bad.json": &fstest.MapFile{Data: []byte(tc.content)},
			}
			err := transit.SeedPrerenderedIsochrones(context.Background(), fsys, store)
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q should mention %q", err, tc.wantIn)
			}
			if store.creates != 0 {
				t.Error("a malformed file was partially written")
			}
		})
	}
}

// The embedded seed tree must stay parseable by the seeder that reads it, so a
// payload committed with a typo fails here rather than at a deployment's boot.
func TestSeedPrerenderedIsochrones_embeddedSeedDataIsValid(t *testing.T) {
	store := newFakePrerenderedSeedStore()
	if err := transit.SeedPrerenderedIsochronesFromEmbedded(context.Background(), store); err != nil {
		t.Fatalf("the embedded prerendered seed data does not load: %v", err)
	}
}
