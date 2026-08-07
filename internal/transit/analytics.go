package transit

import (
	"encoding/json"
	"time"
)

// AnalyticsEventTypes is the known event taxonomy, matching the website's
// src/analytics/types.ts AnalyticsEvent union. The ingestion endpoint drops
// anything outside this set rather than storing it, so a client typo or a
// probe against the endpoint cannot seed the aggregate reads with junk
// categories that never go away.
var AnalyticsEventTypes = map[string]bool{
	"page_view":         true,
	"origin_search":     true,
	"mode_toggle":       true,
	"isochrone_request": true,
	"isochrone_error":   true,
}

// AnalyticsEventRecord is one event as stored: its recognised type, the page
// it happened on (empty outside page_view), and its type-specific fields held
// verbatim. See the analytics_events migration for why there is no visitor
// identifier of any kind here and no client-supplied timestamp.
type AnalyticsEventRecord struct {
	ID         string
	Type       string
	Path       string
	Properties json.RawMessage
}

// EventTypeCount is one row of AnalyticsSummary.EventCounts: how many events
// of a type fell inside the requested window.
type EventTypeCount struct {
	EventType string `json:"event_type"`
	Count     int64  `json:"count"`
}

// DailyEventCount is one row of AnalyticsSummary.DailyCounts: a day, a type,
// and how many of that type were recorded on it. Days and types with no
// events are simply absent rather than reported as zero, so a client renders
// a sparse series rather than padding one out itself.
type DailyEventCount struct {
	Date      time.Time `json:"date"`
	EventType string    `json:"event_type"`
	Count     int64     `json:"count"`
}

// PathCount is one row of AnalyticsSummary.TopPages.
type PathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// AnalyticsSummary is the aggregate an admin reads: totals by event type, a
// daily time series for charting, and the most-viewed pages, all scoped to
// [Since, Until).
type AnalyticsSummary struct {
	Since       time.Time         `json:"since"`
	Until       time.Time         `json:"until"`
	EventCounts []EventTypeCount  `json:"event_counts"`
	DailyCounts []DailyEventCount `json:"daily_counts"`
	TopPages    []PathCount       `json:"top_pages"`
}
