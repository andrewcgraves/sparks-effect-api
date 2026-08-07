package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// rewindAnalyticsEventsMigration unwinds 00015 and is the current tail of the
// rewind chain that starts in snapmigration_test.go — see
// rewindRoutingJobsMigration, which now calls this first since 00015 sits
// after it.
func rewindAnalyticsEventsMigration(t *testing.T, url string) {
	t.Helper()
	exec(t, url,
		`DROP TABLE IF EXISTS analytics_events`,
		`DELETE FROM goose_db_version WHERE version_id = 15`)
}

func newAnalyticsEvent(t *testing.T, eventType, path string, props string) transit.AnalyticsEventRecord {
	t.Helper()
	id, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	return transit.AnalyticsEventRecord{
		ID: id, Type: eventType, Path: path, Properties: json.RawMessage(props),
	}
}

func TestInsertAnalyticsEvents_emptyBatchIsANoOp(t *testing.T) {
	repo, _ := freshRepo(t)
	if err := repo.InsertAnalyticsEvents(context.Background(), nil); err != nil {
		t.Fatalf("InsertAnalyticsEvents(nil): %v", err)
	}
}

func TestInsertAnalyticsEvents_propertiesRoundTripThroughSummary(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	events := []transit.AnalyticsEventRecord{
		newAnalyticsEvent(t, "page_view", "/", `{"type":"page_view","path":"/"}`),
		newAnalyticsEvent(t, "page_view", "/scenario/ca-hsr", `{"type":"page_view","path":"/scenario/ca-hsr"}`),
		newAnalyticsEvent(t, "mode_toggle", "", `{"type":"mode_toggle","mode":"walk"}`),
	}
	if err := repo.InsertAnalyticsEvents(ctx, events); err != nil {
		t.Fatalf("InsertAnalyticsEvents: %v", err)
	}

	since := time.Now().Add(-time.Hour)
	until := time.Now().Add(time.Hour)
	summary, err := repo.AnalyticsSummary(ctx, since, until)
	if err != nil {
		t.Fatalf("AnalyticsSummary: %v", err)
	}

	counts := map[string]int64{}
	for _, c := range summary.EventCounts {
		counts[c.EventType] = c.Count
	}
	if counts["page_view"] != 2 {
		t.Errorf("page_view count = %d, want 2", counts["page_view"])
	}
	if counts["mode_toggle"] != 1 {
		t.Errorf("mode_toggle count = %d, want 1", counts["mode_toggle"])
	}

	pages := map[string]int64{}
	for _, p := range summary.TopPages {
		pages[p.Path] = p.Count
	}
	if pages["/"] != 1 || pages["/scenario/ca-hsr"] != 1 {
		t.Errorf("top_pages = %+v, want one each for / and /scenario/ca-hsr", summary.TopPages)
	}

	if len(summary.DailyCounts) == 0 {
		t.Error("daily_counts: want at least one bucket for events inserted just now")
	}
}

func TestAnalyticsSummary_excludesEventsOutsideTheWindow(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	if err := repo.InsertAnalyticsEvents(ctx, []transit.AnalyticsEventRecord{
		newAnalyticsEvent(t, "page_view", "/", `{"type":"page_view","path":"/"}`),
	}); err != nil {
		t.Fatalf("InsertAnalyticsEvents: %v", err)
	}

	// A window that closed before the insert (and one that opens after it)
	// must both come back empty — created_at is server-assigned "now", so
	// this exercises the boundary rather than a fabricated timestamp.
	past := time.Now().Add(-48 * time.Hour)
	summary, err := repo.AnalyticsSummary(ctx, past.Add(-time.Hour), past)
	if err != nil {
		t.Fatalf("AnalyticsSummary: %v", err)
	}
	if len(summary.EventCounts) != 0 {
		t.Errorf("event_counts for a window before the insert = %+v, want none", summary.EventCounts)
	}

	future := time.Now().Add(48 * time.Hour)
	summary, err = repo.AnalyticsSummary(ctx, future, future.Add(time.Hour))
	if err != nil {
		t.Fatalf("AnalyticsSummary: %v", err)
	}
	if len(summary.EventCounts) != 0 {
		t.Errorf("event_counts for a window after the insert = %+v, want none", summary.EventCounts)
	}
}

// analytics_events stores no identifier of any kind (see the migration's
// privacy rationale) — this test pins that guarantee at the schema level so
// a future column addition trips a test rather than silently reintroducing
// one.
func TestAnalyticsEventsSchemaHasNoIdentifierColumns(t *testing.T) {
	ctx := context.Background()
	_, url := freshRepo(t)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_name = 'analytics_events'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	want := map[string]bool{
		"id": true, "event_type": true, "path": true, "properties": true, "created_at": true,
	}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !want[col] {
			t.Errorf("unexpected column %q on analytics_events; if this is deliberate, it must not be anything that could identify a visitor", col)
		}
		delete(want, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(want) != 0 {
		t.Errorf("missing expected columns: %v", want)
	}
}
