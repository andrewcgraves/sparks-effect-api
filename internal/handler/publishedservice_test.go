package handler_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type fakePublishedServiceStore struct {
	items []transit.PublishedServiceSummary
	next  *transit.PublishedIndexKey
	err   error

	calls    int
	gotAfter *transit.PublishedIndexKey
	gotLimit int
}

func (f *fakePublishedServiceStore) ListPublishedServiceSummaries(_ context.Context, after *transit.PublishedIndexKey, limit int) (transit.PublishedIndexPage, error) {
	f.calls++
	f.gotAfter, f.gotLimit = after, limit
	return transit.PublishedIndexPage{Items: f.items, Next: f.next}, f.err
}

func listPublishedServices(t *testing.T, store handler.PublishedServiceStore) *httptest.ResponseRecorder {
	t.Helper()
	return getPublishedServices(t, store, "")
}

func getPublishedServices(t *testing.T, store handler.PublishedServiceStore, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.PublishedServices(store, testTags).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/published-services"+query, nil))
	return rec
}

type publishedPage struct {
	Items      []transit.PublishedServiceSummary `json:"items"`
	NextCursor *string                           `json:"next_cursor"`
}

func decodePublishedPage(t *testing.T, rec *httptest.ResponseRecorder) publishedPage {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if _, ok := raw["next_cursor"]; !ok || len(raw) != 2 {
		t.Fatalf("body = %s, want exactly items and next_cursor", rec.Body.String())
	}
	var page publishedPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	return page
}

func TestPublishedServicesSendsCardFieldsInStoreOrder(t *testing.T) {
	store := &fakePublishedServiceStore{items: []transit.PublishedServiceSummary{
		{Slug: "newer", Name: "Newer Line", Subtext: "Electrified · Express", Description: "Published second.",
			AuthorName: "Ada Lovelace", PublishedAt: time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)},
		{Slug: "older", Name: "Older Line"},
	}}

	rec := listPublishedServices(t, store)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	// Neither limit nor cursor: the bare array the website read before
	// SPA-434, and everything in it.
	if store.gotAfter != nil || store.gotLimit != 0 {
		t.Fatalf("store asked for after=%+v limit=%d, want everything", store.gotAfter, store.gotLimit)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 || got[0]["slug"] != "newer" || got[1]["slug"] != "older" {
		t.Fatalf("items = %+v, want newer then older, as the store ordered them", got)
	}
	want := map[string]any{
		"slug": "newer", "name": "Newer Line",
		"subtext": "Electrified · Express", "description": "Published second.",
		"author_name": "Ada Lovelace", "published_at": "2026-10-12T09:30:00Z",
	}
	if len(got[0]) != len(want) {
		t.Fatalf("first item keys = %+v, want exactly %+v", got[0], want)
	}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("%s = %v, want %v", k, got[0][k], v)
		}
	}
	// Empty prose is omitted, matching UserService and ServicePublication.
	if _, ok := got[1]["subtext"]; ok {
		t.Errorf("empty subtext sent: %+v", got[1])
	}
	if _, ok := got[1]["description"]; ok {
		t.Errorf("empty description sent: %+v", got[1])
	}
}

func TestPublishedServicesSendsAnEmptyArrayForNothingPublished(t *testing.T) {
	rec := listPublishedServices(t, &fakePublishedServiceStore{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

func TestPublishedServicesReportsStorageFailure(t *testing.T) {
	rec := listPublishedServices(t, &fakePublishedServiceStore{err: errors.New("database is down")})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}

func TestPublishedServicesPagesFiftyAtATimeByDefault(t *testing.T) {
	store := &fakePublishedServiceStore{items: []transit.PublishedServiceSummary{{Slug: "only", Name: "Only Line"}}}

	page := decodePublishedPage(t, getPublishedServices(t, store, "?cursor="))
	if store.gotAfter != nil || store.gotLimit != 50 {
		t.Fatalf("store asked for after=%+v limit=%d, want the first 50", store.gotAfter, store.gotLimit)
	}
	if len(page.Items) != 1 || page.Items[0].Slug != "only" {
		t.Fatalf("items = %+v, want the store's page", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("next_cursor = %q on the last page, want null", *page.NextCursor)
	}
}

func TestPublishedServicesSendsAnEmptyPageAsAnEmptyArray(t *testing.T) {
	rec := getPublishedServices(t, &fakePublishedServiceStore{}, "?limit=10")
	if body := strings.TrimSpace(rec.Body.String()); body != `{"items":[],"next_cursor":null}` {
		t.Fatalf("body = %s, want an empty items array and a null cursor", body)
	}
}

func TestPublishedServicesCapsTheLimitAtOneHundred(t *testing.T) {
	for query, want := range map[string]int{"?limit=1": 1, "?limit=100": 100, "?limit=101": 100, "?limit=5000": 100} {
		store := &fakePublishedServiceStore{}
		decodePublishedPage(t, getPublishedServices(t, store, query))
		if store.gotLimit != want {
			t.Errorf("%s: store limit = %d, want %d", query, store.gotLimit, want)
		}
	}
}

func TestPublishedServicesRejectsALimitThatIsNotAPositiveInteger(t *testing.T) {
	for _, query := range []string{"?limit=", "?limit=0", "?limit=-3", "?limit=ten", "?limit=2.5"} {
		store := &fakePublishedServiceStore{}
		rec := getPublishedServices(t, store, query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body %s", query, rec.Code, rec.Body.String())
		}
		if store.calls != 0 {
			t.Errorf("%s: store was read for a rejected request", query)
		}
	}
}

func TestPublishedServicesNextCursorResumesAfterTheStoresKey(t *testing.T) {
	key := transit.PublishedIndexKey{
		FirstPublishedAt: time.Date(2026, 9, 30, 3, 12, 11, 518123000, time.UTC),
		Slug:             "coast|line",
	}
	first := &fakePublishedServiceStore{items: []transit.PublishedServiceSummary{{Slug: "coast|line"}}, next: &key}
	page := decodePublishedPage(t, getPublishedServices(t, first, "?limit=1"))
	if page.NextCursor == nil || *page.NextCursor == "" {
		t.Fatalf("next_cursor = %v, want a cursor while the store has more", page.NextCursor)
	}

	second := &fakePublishedServiceStore{}
	decodePublishedPage(t, getPublishedServices(t, second, "?limit=1&cursor="+url.QueryEscape(*page.NextCursor)))
	if second.gotAfter == nil || !second.gotAfter.FirstPublishedAt.Equal(key.FirstPublishedAt) || second.gotAfter.Slug != key.Slug {
		t.Fatalf("store resumed after %+v, want %+v", second.gotAfter, key)
	}
	if second.gotLimit != 1 {
		t.Fatalf("store limit = %d, want 1", second.gotLimit)
	}
}

func TestPublishedServicesRejectsAMalformedCursor(t *testing.T) {
	for _, cursor := range []string{
		"not base64!",
		base64.RawURLEncoding.EncodeToString([]byte("no separator")),
		base64.RawURLEncoding.EncodeToString([]byte("yesterday|coast-line")),
	} {
		store := &fakePublishedServiceStore{}
		rec := getPublishedServices(t, store, "?cursor="+url.QueryEscape(cursor))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("cursor %q: status = %d, want 400; body %s", cursor, rec.Code, rec.Body.String())
		}
		if store.calls != 0 {
			t.Errorf("cursor %q: store was read for a rejected request", cursor)
		}
	}
}
