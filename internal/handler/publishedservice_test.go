package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type fakePublishedServiceStore struct {
	items []transit.PublishedServiceSummary
	err   error
}

func (f fakePublishedServiceStore) ListPublishedServiceSummaries(context.Context) ([]transit.PublishedServiceSummary, error) {
	return f.items, f.err
}

func listPublishedServices(t *testing.T, store handler.PublishedServiceStore) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.PublishedServices(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/published-services", nil))
	return rec
}

func TestPublishedServicesSendsCardFieldsInStoreOrder(t *testing.T) {
	store := fakePublishedServiceStore{items: []transit.PublishedServiceSummary{
		{Slug: "newer", Name: "Newer Line", Subtext: "Electrified · Express", Description: "Published second."},
		{Slug: "older", Name: "Older Line"},
	}}

	rec := listPublishedServices(t, store)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
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
	rec := listPublishedServices(t, fakePublishedServiceStore{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

func TestPublishedServicesReportsStorageFailure(t *testing.T) {
	rec := listPublishedServices(t, fakePublishedServiceStore{err: errors.New("database is down")})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("internal error leaked to client: %s", rec.Body.String())
	}
}
