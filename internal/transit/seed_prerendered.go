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

type PrerenderedSeedStore interface {
	ListCuratedScenarios(ctx context.Context) ([]Scenario, error)
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]ServiceMembership, error)
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]PrerenderedIsochrone, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *PrerenderedIsochrone) error
}

type prerenderedSeedFile struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Lat        float64         `json:"lat"`
	Lng        float64         `json:"lng"`
	BudgetMins int             `json:"budget_mins"`
	Mode       TravelMode      `json:"mode"`
	Result     json.RawMessage `json:"result"`
}

func SeedPrerenderedIsochrones(ctx context.Context, fsys fs.FS, store PrerenderedSeedStore) error {
	scenarios, err := store.ListCuratedScenarios(ctx)
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

func SeedPrerenderedIsochronesFromEmbedded(ctx context.Context, store PrerenderedSeedStore) error {
	return SeedPrerenderedIsochrones(ctx, dataFS, store)
}
