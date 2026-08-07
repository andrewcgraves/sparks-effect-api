package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type fakeAnalyticsStore struct {
	inserted []transit.AnalyticsEventRecord
	failWith error

	summary       transit.AnalyticsSummary
	summaryErr    error
	gotSince      time.Time
	gotUntil      time.Time
	summaryCalled bool
}

func (f *fakeAnalyticsStore) InsertAnalyticsEvents(_ context.Context, events []transit.AnalyticsEventRecord) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.inserted = append(f.inserted, events...)
	return nil
}

func (f *fakeAnalyticsStore) AnalyticsSummary(_ context.Context, since, until time.Time) (transit.AnalyticsSummary, error) {
	f.summaryCalled = true
	f.gotSince, f.gotUntil = since, until
	if f.summaryErr != nil {
		return transit.AnalyticsSummary{}, f.summaryErr
	}
	return f.summary, nil
}

func postAnalyticsEvents(t *testing.T, store handler.AnalyticsEventStore, body, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	rec := httptest.NewRecorder()
	handler.IngestAnalyticsEvents(store).ServeHTTP(rec, req)
	return rec
}

const realBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

func TestIngestAnalyticsEvents_storesRecognisedEvents(t *testing.T) {
	store := &fakeAnalyticsStore{}
	body := `{"events":[
		{"type":"page_view","path":"/scenario/ca-hsr"},
		{"type":"mode_toggle","mode":"walk"}
	]}`

	rec := postAnalyticsEvents(t, store, body, realBrowserUA)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d; body %s", rec.Code, rec.Body.String())
	}
	if len(store.inserted) != 2 {
		t.Fatalf("inserted: want 2 events, got %d", len(store.inserted))
	}
	if store.inserted[0].Type != "page_view" || store.inserted[0].Path != "/scenario/ca-hsr" {
		t.Errorf("event 0: got type=%q path=%q", store.inserted[0].Type, store.inserted[0].Path)
	}
	if store.inserted[0].ID == "" {
		t.Error("event 0: expected a minted id")
	}
	if store.inserted[1].Type != "mode_toggle" {
		t.Errorf("event 1: got type=%q", store.inserted[1].Type)
	}
	var props map[string]any
	if err := json.Unmarshal(store.inserted[1].Properties, &props); err != nil {
		t.Fatalf("unmarshal properties: %v", err)
	}
	if props["mode"] != "walk" {
		t.Errorf("properties: want mode=walk, got %v", props)
	}
}

func TestIngestAnalyticsEvents_dropsUnrecognisedTypesWithoutError(t *testing.T) {
	store := &fakeAnalyticsStore{}
	body := `{"events":[
		{"type":"page_view","path":"/"},
		{"type":"totally_made_up","foo":"bar"}
	]}`

	rec := postAnalyticsEvents(t, store, body, realBrowserUA)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d", rec.Code)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted: want 1 recognised event, got %d", len(store.inserted))
	}
}

func TestIngestAnalyticsEvents_botUserAgentStoresNothing(t *testing.T) {
	for _, ua := range []string{
		"",
		"curl/8.4.0",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"python-requests/2.31.0",
	} {
		t.Run(ua, func(t *testing.T) {
			store := &fakeAnalyticsStore{}
			rec := postAnalyticsEvents(t, store, `{"events":[{"type":"page_view","path":"/"}]}`, ua)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status: want 204, got %d", rec.Code)
			}
			if len(store.inserted) != 0 {
				t.Fatalf("inserted: want 0 events for bot UA %q, got %d", ua, len(store.inserted))
			}
		})
	}
}

func TestIngestAnalyticsEvents_emptyBatchRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	rec := postAnalyticsEvents(t, store, `{"events":[]}`, realBrowserUA)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestIngestAnalyticsEvents_oversizedBatchRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	var events []string
	for i := 0; i < 201; i++ {
		events = append(events, `{"type":"page_view","path":"/"}`)
	}
	body := `{"events":[` + strings.Join(events, ",") + `]}`

	rec := postAnalyticsEvents(t, store, body, realBrowserUA)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if len(store.inserted) != 0 {
		t.Fatalf("inserted: want 0 events, got %d", len(store.inserted))
	}
}

func TestIngestAnalyticsEvents_malformedJSONRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	rec := postAnalyticsEvents(t, store, `not json`, realBrowserUA)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestIngestAnalyticsEvents_bodyTooLargeRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	// One legitimate event plus a padding property well past the 64 KiB cap.
	pad := strings.Repeat("a", 1<<17)
	body := fmt.Sprintf(`{"events":[{"type":"origin_search","query":%q,"resultCount":1}]}`, pad)

	rec := postAnalyticsEvents(t, store, body, realBrowserUA)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413, got %d", rec.Code)
	}
}

func TestIngestAnalyticsEvents_storeErrorAnswers500(t *testing.T) {
	store := &fakeAnalyticsStore{failWith: errors.New("boom")}
	rec := postAnalyticsEvents(t, store, `{"events":[{"type":"page_view","path":"/"}]}`, realBrowserUA)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
}

func TestAnalyticsSummary_defaultsToTrailing30Days(t *testing.T) {
	store := &fakeAnalyticsStore{summary: transit.AnalyticsSummary{
		EventCounts: []transit.EventTypeCount{{EventType: "page_view", Count: 5}},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/analytics/summary", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d; body %s", rec.Code, rec.Body.String())
	}
	if !store.summaryCalled {
		t.Fatal("expected the store to be queried")
	}
	gotSpan := store.gotUntil.Sub(store.gotSince)
	wantSpan := 30 * 24 * time.Hour
	if gotSpan != wantSpan {
		t.Errorf("window: want %s, got %s", wantSpan, gotSpan)
	}

	var got transit.AnalyticsSummary
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.EventCounts) != 1 || got.EventCounts[0].Count != 5 {
		t.Errorf("event_counts: got %+v", got.EventCounts)
	}
}

func TestAnalyticsSummary_explicitWindow(t *testing.T) {
	store := &fakeAnalyticsStore{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/analytics/summary?since=2026-01-01&until=2026-02-01", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d; body %s", rec.Code, rec.Body.String())
	}
	wantSince := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !store.gotSince.Equal(wantSince) || !store.gotUntil.Equal(wantUntil) {
		t.Errorf("window: want [%s, %s), got [%s, %s)", wantSince, wantUntil, store.gotSince, store.gotUntil)
	}
}

func TestAnalyticsSummary_sinceAfterUntilRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/analytics/summary?since=2026-02-01&until=2026-01-01", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
	if store.summaryCalled {
		t.Error("store should not be queried for an invalid window")
	}
}

func TestAnalyticsSummary_windowTooWideRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/analytics/summary?since=2020-01-01&until=2026-01-01", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestAnalyticsSummary_malformedDateRejected(t *testing.T) {
	store := &fakeAnalyticsStore{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/admin/analytics/summary?since=not-a-date", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rec.Code)
	}
}

func TestAnalyticsSummary_storeErrorAnswers500(t *testing.T) {
	store := &fakeAnalyticsStore{summaryErr: errors.New("boom")}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/analytics/summary", nil)
	rec := httptest.NewRecorder()
	handler.AnalyticsSummary(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
}
