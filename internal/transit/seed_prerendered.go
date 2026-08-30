package transit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"strings"
)

// PrerenderedSeedStore is what seeding curated isochrones needs: the seeded
// scenarios to look for files under, each one's current service membership to
// snapshot, what is already stored so a re-run writes nothing, and the insert.
//
// It is a narrow interface rather than the whole Repository for the reason
// SeededCompileStore is: it makes the seeder drivable by a fake in a test,
// which is the only way the idempotency and skip behaviour below get exercised
// at all. *postgres.Repo satisfies it.
type PrerenderedSeedStore interface {
	ListScenarios(ctx context.Context) ([]Scenario, error)
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]ServiceMembership, error)
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]PrerenderedIsochrone, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *PrerenderedIsochrone) error
}

// prerenderedSeedFile is one file under a scenario's prerendered/ directory.
//
// The file is self-describing: it carries its own id, so nothing about which
// entry it is depends on the filename or on insertion order. That id is the
// identity this seeder is idempotent against.
//
// Result is json.RawMessage so the payload reaches the database exactly as
// authored. Decoding it into anything else would mean this repository had an
// opinion about a shape it does not produce, cannot check, and must not
// silently rewrite.
type prerenderedSeedFile struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Lat        float64         `json:"lat"`
	Lng        float64         `json:"lng"`
	BudgetMins int             `json:"budget_mins"`
	Mode       TravelMode      `json:"mode"`
	Result     json.RawMessage `json:"result"`
}

// SeedPrerenderedIsochrones writes each seeded scenario's curated isochrones
// from fsys, skipping any whose id is already stored.
//
// # Why this is not part of SeedIfEmpty
//
// SeedIfEmpty returns the moment it finds a single scenario row, which is the
// right rule for "populate an empty database" and the wrong one entirely for
// shipping new seed content. Every deployed environment already has scenarios,
// so anything hung off that path would land on a fresh developer database and
// nowhere else — the same split migrations 00011, 00012, 00015, 00016 and
// 00018 each had to work around. This runs unconditionally instead and earns
// its idempotency from the data rather than from an early return.
//
// # Idempotency
//
// The id in each file is the entry's stable identity. A file whose id is
// already stored for that scenario is skipped, so the first boot inserts and
// every boot after it does nothing. Editing a payload in place therefore does
// not republish it — deliberately: these are curated illustrations, and
// silently rewriting one under a client that has the old bytes cached is worse
// than requiring a new id. Nothing here ever updates or deletes.
//
// # Ordering
//
// It must run after CompileSeededIfNeeded, and does in cmd/api. The membership
// snapshot it takes is the scenario's services as they stand, so a boot that
// is still mid-seed would snapshot a partial set and every entry would report
// itself outdated from birth.
//
// A scenario with no prerendered/ directory is simply skipped: most scenarios
// ship no curated isochrones, and an absent directory is that statement, not a
// fault. A directory whose files are malformed is a fault, and aborts the boot
// — this is repo-authored data, so a bad file is a mistake to surface loudly
// rather than a condition to tolerate.
func SeedPrerenderedIsochrones(ctx context.Context, fsys fs.FS, store PrerenderedSeedStore) error {
	scenarios, err := store.ListScenarios(ctx)
	if err != nil {
		return fmt.Errorf("transit: listing scenarios to seed prerendered isochrones: %w", err)
	}

	for _, sc := range scenarios {
		if err := seedScenarioPrerendered(ctx, fsys, store, sc); err != nil {
			return fmt.Errorf("transit: seeding prerendered isochrones for %q: %w", sc.Slug, err)
		}
	}
	return nil
}

func seedScenarioPrerendered(ctx context.Context, fsys fs.FS, store PrerenderedSeedStore, sc Scenario) error {
	dir := path.Join("data/scenarios", sc.Slug, "prerendered")
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, path.Join(dir, e.Name()))
	}
	if len(files) == 0 {
		return nil
	}

	// The existing set comes from the list read, which does not select the
	// payloads: this runs on every boot, and reading a few hundred kilobytes
	// per entry to answer "is it there?" would be the one expensive thing in
	// an otherwise free no-op.
	existing, err := store.ListPrerenderedIsochronesByScenario(ctx, sc.Slug)
	if err != nil {
		return fmt.Errorf("listing existing prerendered isochrones: %w", err)
	}
	stored := make(map[string]bool, len(existing))
	for _, p := range existing {
		stored[p.ID] = true
	}

	// Read lazily: a scenario whose entries are all present should not pay for
	// a membership query it will not use.
	var members []ServiceMembership
	loadedMembers := false

	for _, file := range files {
		seed, err := readPrerenderedSeedFile(fsys, file)
		if err != nil {
			return err
		}
		if stored[seed.ID] {
			continue
		}
		if !loadedMembers {
			members, err = store.ListServiceMembershipByScenario(ctx, sc.ID)
			if err != nil {
				return fmt.Errorf("loading service membership: %w", err)
			}
			loadedMembers = true
		}

		entry := PrerenderedIsochrone{
			ID:           seed.ID,
			ScenarioSlug: sc.Slug,
			Label:        seed.Label,
			Lat:          seed.Lat,
			Lng:          seed.Lng,
			BudgetMins:   seed.BudgetMins,
			Mode:         seed.Mode,
			Result:       seed.Result,
			// Snapshotted now, not left empty: an empty snapshot against a
			// scenario that has services differs from that scenario's live
			// membership, so the entry would report outdated from its first
			// read (see MembershipStale).
			CompiledServiceIDs: MembershipIDs(members),
		}
		if err := store.CreatePrerenderedIsochrone(ctx, &entry); err != nil {
			return fmt.Errorf("creating prerendered isochrone %q: %w", seed.ID, err)
		}
		stored[seed.ID] = true
		slog.InfoContext(ctx, "transit: seeded prerendered isochrone",
			"scenario_slug", sc.Slug, "prerendered_isochrone_id", seed.ID, "label", seed.Label)
	}
	return nil
}

// readPrerenderedSeedFile parses and validates one seed file.
//
// Validation covers the fields this repository has an opinion about — an id to
// be idempotent on, a label to show, a mode and budget the isochrone surface
// recognises — and stops at result, which is opaque here exactly as it is in
// the create handler and in storage.
func readPrerenderedSeedFile(fsys fs.FS, file string) (prerenderedSeedFile, error) {
	data, err := fs.ReadFile(fsys, file)
	if err != nil {
		return prerenderedSeedFile{}, fmt.Errorf("reading %s: %w", file, err)
	}
	var seed prerenderedSeedFile
	if err := json.Unmarshal(data, &seed); err != nil {
		return prerenderedSeedFile{}, fmt.Errorf("parsing %s: %w", file, err)
	}
	if seed.ID == "" {
		return prerenderedSeedFile{}, fmt.Errorf("%s: id is required; it is the identity this seeder is idempotent on", file)
	}
	if strings.TrimSpace(seed.Label) == "" {
		return prerenderedSeedFile{}, fmt.Errorf("%s: label is required", file)
	}
	if !seed.Mode.Valid() {
		return prerenderedSeedFile{}, fmt.Errorf("%s: mode %q is not one of %s", file, seed.Mode, TravelModeList())
	}
	if seed.BudgetMins <= 0 {
		return prerenderedSeedFile{}, fmt.Errorf("%s: budget_mins must be greater than 0", file)
	}
	if len(seed.Result) == 0 {
		return prerenderedSeedFile{}, fmt.Errorf("%s: result is required", file)
	}
	return seed, nil
}

// SeedPrerenderedIsochronesFromEmbedded is SeedPrerenderedIsochrones over this
// package's own embedded seed data — what cmd/api calls, so the embed stays
// private to the package that owns it while the fs.FS seam above stays open
// for tests.
func SeedPrerenderedIsochronesFromEmbedded(ctx context.Context, store PrerenderedSeedStore) error {
	return SeedPrerenderedIsochrones(ctx, dataFS, store)
}
