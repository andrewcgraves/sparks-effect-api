package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// --- Analytics events (SPA-218) ---

// InsertAnalyticsEvents writes events in one transaction, so a batch either
// lands whole or not at all rather than a partial write a client has no way
// to detect and retry safely. A loop of individual inserts, not a bulk copy:
// batches are capped small by the handler (see handler.maxAnalyticsEvents),
// so there is no volume here CopyFrom's setup cost would pay for itself on.
func (r *Repo) InsertAnalyticsEvents(ctx context.Context, events []transit.AnalyticsEventRecord) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrap("InsertAnalyticsEvents begin", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	for _, e := range events {
		props := e.Properties
		if props == nil {
			props = []byte(`{}`)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO analytics_events (id, event_type, path, properties)
			 VALUES ($1, $2, $3, $4)`,
			e.ID, e.Type, e.Path, props); err != nil {
			return wrap("InsertAnalyticsEvents", err)
		}
	}
	return wrap("InsertAnalyticsEvents commit", tx.Commit(ctx))
}

// AnalyticsSummary aggregates analytics_events in [since, until) three ways:
// totals by event type, a daily series by event type for charting, and the
// most-viewed pages. Three queries rather than one: each groups differently,
// and a single query wide enough to answer all three would be harder to read
// than three narrow ones are to run.
func (r *Repo) AnalyticsSummary(ctx context.Context, since, until time.Time) (transit.AnalyticsSummary, error) {
	summary := transit.AnalyticsSummary{Since: since, Until: until}

	eventRows, err := r.pool.Query(ctx,
		`SELECT event_type, count(*) FROM analytics_events
		 WHERE created_at >= $1 AND created_at < $2
		 GROUP BY event_type ORDER BY event_type`,
		since, until)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary event counts", err)
	}
	summary.EventCounts, err = scanEventTypeCounts(eventRows)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary event counts", err)
	}

	dailyRows, err := r.pool.Query(ctx,
		`SELECT date_trunc('day', created_at) AS day, event_type, count(*)
		 FROM analytics_events
		 WHERE created_at >= $1 AND created_at < $2
		 GROUP BY day, event_type ORDER BY day, event_type`,
		since, until)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary daily counts", err)
	}
	summary.DailyCounts, err = scanDailyEventCounts(dailyRows)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary daily counts", err)
	}

	// page_view is the only event type path is populated for; the others
	// would only add a meaningless empty-string row to "top pages".
	pageRows, err := r.pool.Query(ctx,
		`SELECT path, count(*) FROM analytics_events
		 WHERE event_type = 'page_view' AND created_at >= $1 AND created_at < $2
		 GROUP BY path ORDER BY count(*) DESC, path LIMIT 20`,
		since, until)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary top pages", err)
	}
	summary.TopPages, err = scanPathCounts(pageRows)
	if err != nil {
		return transit.AnalyticsSummary{}, wrap("AnalyticsSummary top pages", err)
	}

	return summary, nil
}

func scanEventTypeCounts(rows pgx.Rows) ([]transit.EventTypeCount, error) {
	defer rows.Close()
	counts := []transit.EventTypeCount{}
	for rows.Next() {
		var c transit.EventTypeCount
		if err := rows.Scan(&c.EventType, &c.Count); err != nil {
			return nil, err
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

func scanDailyEventCounts(rows pgx.Rows) ([]transit.DailyEventCount, error) {
	defer rows.Close()
	counts := []transit.DailyEventCount{}
	for rows.Next() {
		var c transit.DailyEventCount
		if err := rows.Scan(&c.Date, &c.EventType, &c.Count); err != nil {
			return nil, err
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

func scanPathCounts(rows pgx.Rows) ([]transit.PathCount, error) {
	defer rows.Close()
	counts := []transit.PathCount{}
	for rows.Next() {
		var c transit.PathCount
		if err := rows.Scan(&c.Path, &c.Count); err != nil {
			return nil, err
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}
