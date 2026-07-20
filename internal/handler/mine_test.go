package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const timeHour = time.Hour

// fakeOwnerStore records which owner ID it was asked about, so tests can prove
// the handler scopes by the *authenticated* identity rather than by anything
// the client supplied.
type fakeOwnerStore struct {
	scenarios map[string][]transit.Scenario
	services  map[string][]transit.Service
	askedFor  []string
}

func (f *fakeOwnerStore) ListScenariosByOwner(_ context.Context, ownerID string) ([]transit.Scenario, error) {
	f.askedFor = append(f.askedFor, ownerID)
	return f.scenarios[ownerID], nil
}

func (f *fakeOwnerStore) ListServicesByOwner(_ context.Context, ownerID string) ([]transit.Service, error) {
	f.askedFor = append(f.askedFor, ownerID)
	return f.services[ownerID], nil
}

func newFakeOwnerStore() *fakeOwnerStore {
	ownerA, ownerB := "user-1", "user-2"
	return &fakeOwnerStore{
		scenarios: map[string][]transit.Scenario{
			"user-1": {{ID: "sc-a", Slug: "a-net", Name: "A Net", OwnerID: &ownerA}},
			"user-2": {{ID: "sc-b", Slug: "b-net", Name: "B Net", OwnerID: &ownerB}},
		},
		services: map[string][]transit.Service{
			"user-1": {{ID: "svc-a", Name: "A Service", OwnerID: &ownerA}},
			"user-2": {{ID: "svc-b", Name: "B Service", OwnerID: &ownerB}},
		},
	}
}

func getAs(t *testing.T, h http.Handler, path string, user transit.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMyScenariosReturnsOnlyTheCallersRows(t *testing.T) {
	store := newFakeOwnerStore()
	rec := getAs(t, handler.MyScenarios(store), "/api/me/scenarios", transit.User{ID: "user-1"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []transit.Scenario
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "sc-a" {
		t.Errorf("got %+v, want only sc-a", got)
	}
	if len(store.askedFor) != 1 || store.askedFor[0] != "user-1" {
		t.Errorf("store queried for %v, want [user-1]", store.askedFor)
	}
}

func TestMyServicesReturnsOnlyTheCallersRows(t *testing.T) {
	store := newFakeOwnerStore()
	rec := getAs(t, handler.MyServices(store), "/api/me/services", transit.User{ID: "user-2"})

	var got []transit.Service
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "svc-b" {
		t.Errorf("got %+v, want only svc-b", got)
	}
}

// Ownership scoping must come from the authenticated identity, never from a
// client-supplied owner_id — otherwise any user could read another's rows by
// passing their ID.
func TestOwnerScopingIgnoresClientSuppliedOwnerID(t *testing.T) {
	store := newFakeOwnerStore()
	rec := getAs(t, handler.MyScenarios(store),
		"/api/me/scenarios?owner_id=user-2", transit.User{ID: "user-1"})

	var got []transit.Scenario
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "sc-a" {
		t.Errorf("owner_id query param overrode the identity: got %+v", got)
	}
	if store.askedFor[0] != "user-1" {
		t.Errorf("store queried for %q, want user-1", store.askedFor[0])
	}
}

// A user owning nothing gets an empty JSON array, not null — clients iterate
// the response directly.
func TestMyScenariosReturnsEmptyArrayNotNull(t *testing.T) {
	store := newFakeOwnerStore()
	rec := getAs(t, handler.MyScenarios(store), "/api/me/scenarios", transit.User{ID: "user-nothing"})

	if body := rec.Body.String(); body != "[]\n" {
		t.Errorf("body = %q, want an empty array", body)
	}
}

// Admins are not exempt from scoping here: "my scenarios" means theirs, not
// everyone's. Admin power applies to gated endpoints, not to this read.
func TestMyScenariosScopesAdminsToo(t *testing.T) {
	store := newFakeOwnerStore()
	getAs(t, handler.MyScenarios(store), "/api/me/scenarios",
		transit.User{ID: "admin-1", IsAdmin: true})

	if store.askedFor[0] != "admin-1" {
		t.Errorf("store queried for %q, want admin-1", store.askedFor[0])
	}
}
