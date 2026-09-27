package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/jackc/pgx/v5"
)

// SPA-328: ON CONFLICT behaviour a fake WorkerStore cannot observe. Every case
// is its own station so the whole table goes through one Put, which is also
// how the worker sends it — a conflict that must not update still has to let
// the rest of the batch land.
func TestIsochroneCachePutReplacesOnlyABadOrOlderRow(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	const (
		stored   = `{"type":"FeatureCollection","features":[{"id":"stored"}]}`
		incoming = `{"type":"FeatureCollection","features":[{"id":"incoming"}]}`
	)
	older := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		storedGeom   string
		storedAt     *time.Time
		incomingGeom string
		incomingAt   time.Time
		replaced     bool
	}{
		{"featureless row", `{"type":"FeatureCollection","features":[]}`, &newer, incoming, older, true},
		{"row without features", `{"type":"FeatureCollection"}`, &newer, incoming, time.Time{}, true},
		{"null features", `{"type":"FeatureCollection","features":null}`, &newer, incoming, older, true},
		{"features not an array", `{"type":"FeatureCollection","features":{"a":1}}`, &newer, incoming, older, true},
		{"type not a string", `{"type":7,"features":[{}]}`, &newer, incoming, older, true},
		{"not an object", `[{"type":"Feature"}]`, &newer, incoming, older, true},
		{"older tileset", stored, &older, incoming, newer, true},
		{"null tileset loses to a stamp", stored, nil, incoming, older, true},

		{"newer tileset kept", stored, &newer, incoming, older, false},
		{"same tileset kept", stored, &newer, incoming, newer, false},
		{"stamp kept over null", stored, &older, incoming, time.Time{}, false},
		{"null kept over null", stored, nil, incoming, time.Time{}, false},
		{"unusable incoming never replaces a bad row", `{"type":"FeatureCollection","features":[]}`, nil,
			`{"type":"Incoming","features":[]}`, newer, false},
		{"unusable incoming never replaces an older row", stored, &older,
			`{"type":"FeatureCollection","features":[]}`, newer, false},
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	key := func(i int) handler.IsochroneKey {
		return handler.IsochroneKey{
			CompileJobID: routingCompileJobID, StationSlug: "station-" + string(rune('a'+i)),
			Mode: "transit", ContourMins: 30, DepartsOn: "2026-09-01",
		}
	}

	entries := make([]handler.CachedIsochrone, len(cases))
	for i, c := range cases {
		k := key(i)
		if _, err := conn.Exec(ctx, `INSERT INTO isochrone_cache
			(compile_job_id, station_slug, mode, contour_mins, geometry, tileset_at, departs_on)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			k.CompileJobID, k.StationSlug, k.Mode, k.ContourMins, []byte(c.storedGeom), c.storedAt, k.DepartsOn); err != nil {
			t.Fatalf("%s: seed stored row: %v", c.name, err)
		}
		entries[i] = handler.CachedIsochrone{Key: k, Geometry: json.RawMessage(c.incomingGeom), TilesetAt: c.incomingAt}
	}

	if err := repo.PutIsochroneCache(ctx, entries); err != nil {
		t.Fatalf("PutIsochroneCache: a conflict that does not update must not fail the batch: %v", err)
	}

	for i, c := range cases {
		k := key(i)
		var (
			geom []byte
			at   *time.Time
		)
		if err := conn.QueryRow(ctx,
			`SELECT geometry, tileset_at FROM isochrone_cache
			  WHERE compile_job_id = $1 AND station_slug = $2 AND mode = $3
			    AND contour_mins = $4 AND departs_on = $5`,
			k.CompileJobID, k.StationSlug, k.Mode, k.ContourMins, k.DepartsOn).Scan(&geom, &at); err != nil {
			t.Fatalf("%s: read back: %v", c.name, err)
		}

		wantGeom, wantAt := c.storedGeom, c.storedAt
		if c.replaced {
			wantGeom = c.incomingGeom
			wantAt = nil
			if !c.incomingAt.IsZero() {
				wantAt = &c.incomingAt
			}
		}
		if !jsonEqual(t, geom, []byte(wantGeom)) {
			t.Errorf("%s: geometry = %s, want %s", c.name, geom, wantGeom)
		}
		if !sameStamp(at, wantAt) {
			t.Errorf("%s: tileset_at = %v, want %v", c.name, at, wantAt)
		}
	}
}

// The acceptance case end to end through Get: a row the worker would discard
// on read is served as the recomputed polygon after one Put, so the next
// lookup is a hit and the discard warning does not recur.
func TestIsochroneCacheGetServesTheReplacementForAFeaturelessRow(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)
	seedCompileJob(t, repo, routingCompileJobID)

	k := handler.IsochroneKey{
		CompileJobID: routingCompileJobID, StationSlug: "station-a", Mode: "walk", ContourMins: 30,
	}
	stamp := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	put := func(geom string) {
		t.Helper()
		if err := repo.PutIsochroneCache(ctx, []handler.CachedIsochrone{
			{Key: k, Geometry: json.RawMessage(geom), TilesetAt: stamp},
		}); err != nil {
			t.Fatalf("PutIsochroneCache: %v", err)
		}
	}

	put(`{"type":"FeatureCollection","features":[]}`)
	put(`{"type":"FeatureCollection","features":[{"id":"recomputed"}]}`)

	got, err := repo.GetIsochroneCache(ctx, []handler.IsochroneKey{k})
	if err != nil {
		t.Fatalf("GetIsochroneCache: %v", err)
	}
	var parsed struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(got[k], &parsed); err != nil {
		t.Fatalf("geometry: %v", err)
	}
	if len(parsed.Features) != 1 {
		t.Errorf("served %s; the featureless row was not replaced by the recomputed polygon", got[k])
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("unmarshal %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	return string(xs) == string(ys)
}

func sameStamp(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
