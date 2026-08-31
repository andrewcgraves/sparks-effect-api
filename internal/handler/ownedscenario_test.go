package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// fakeScenarioLookup is an in-memory handler.ScenarioBySlugStore: the parent
// lookup, and nothing else. The scenario-scoped child surfaces (stations,
// travel times) embed this rather than the full fake below, so their fakes
// carry only what their handlers can actually call.
type fakeScenarioLookup struct {
	scenarios map[string]transit.Scenario // by slug
	failWith  error
}

func newFakeScenarioLookup() *fakeScenarioLookup {
	return &fakeScenarioLookup{
		scenarios: map[string]transit.Scenario{
			"ca-hsr": {ID: "00000000-0000-4001-8001-000000000001", Slug: "ca-hsr", Name: "CA HSR"},
		},
	}
}

func (f *fakeScenarioLookup) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	if f.failWith != nil {
		return transit.Scenario{}, false, f.failWith
	}
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

// fakeOwnedScenarioStore is an in-memory handler.OwnedScenarioStore: the lookup
// above plus the four methods that change a scenario.
type fakeOwnedScenarioStore struct {
	*fakeScenarioLookup
	curated map[string]int // scenario id -> curated child count
}

func newFakeOwnedScenarioStore() *fakeOwnedScenarioStore {
	return &fakeOwnedScenarioStore{
		fakeScenarioLookup: newFakeScenarioLookup(),
		curated:            map[string]int{},
	}
}

func (f *fakeOwnedScenarioStore) CreateScenario(_ context.Context, sc transit.Scenario) error {
	if f.failWith != nil {
		return f.failWith
	}
	if _, exists := f.scenarios[sc.Slug]; exists {
		return fmt.Errorf("duplicate slug %q", sc.Slug)
	}
	f.scenarios[sc.Slug] = sc
	return nil
}

func (f *fakeOwnedScenarioStore) UpdateScenario(_ context.Context, sc transit.Scenario) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.scenarios[sc.Slug] = sc
	return nil
}

func (f *fakeOwnedScenarioStore) DeleteScenario(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	for slug, sc := range f.scenarios {
		if sc.ID == id {
			delete(f.scenarios, slug)
			return nil
		}
	}
	return fmt.Errorf("no scenario with id %q", id)
}

func (f *fakeOwnedScenarioStore) CountCuratedScenarioChildren(_ context.Context, id string) (int, error) {
	if f.failWith != nil {
		return 0, f.failWith
	}
	return f.curated[id], nil
}

func scenarioBody(name, description, status string) string {
	return `{"name":"` + name + `","description":"` + description + `","status":"` + status + `"}`
}

// asScenarioUser is asUser with the {slug} wildcard bound from the scenario path.
func asScenarioUser(t *testing.T, h http.HandlerFunc, user transit.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	slug := strings.Trim(strings.TrimPrefix(target, "/api/me/scenarios"), "/")
	return runWithSlug(t, h, user, method, target, slug, body)
}

func TestCreateOwnedScenarioStampsTheCallerAsOwner(t *testing.T) {
	store := newFakeOwnedScenarioStore()

	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", scenarioBody("Bay Area Rail", "my network", "draft"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.Scenario
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want %q, got %v", ownerAID, got.OwnerID)
	}
	if got.Slug != "bay-area-rail" {
		t.Errorf("slug: want bay-area-rail, got %q", got.Slug)
	}
	if got.Description != "my network" || got.Status != "draft" {
		t.Errorf("description/status not carried: %+v", got)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/me/scenarios/bay-area-rail" {
		t.Errorf("Location: want /api/me/scenarios/bay-area-rail, got %q", loc)
	}
}

// scenarios.slug is globally unique, so naming a scenario after the curated
// baseline must yield the next free slug rather than a constraint violation.
func TestCreateOwnedScenarioWorksAroundACollidingCuratedSlug(t *testing.T) {
	store := newFakeOwnedScenarioStore()

	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", scenarioBody("CA HSR", "", ""))

	var got transit.Scenario
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Slug != "ca-hsr-2" {
		t.Errorf("slug: want ca-hsr-2, got %q", got.Slug)
	}
}

func TestCreateOwnedScenarioRequiresAName(t *testing.T) {
	store := newFakeOwnedScenarioStore()

	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", scenarioBody("   ", "", ""))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: want 422, got %d (%s)", rec.Code, rec.Body)
	}
}

// A name of pure punctuation validates but cannot produce an address.
func TestCreateOwnedScenarioRejectsAnUnsluggableName(t *testing.T) {
	store := newFakeOwnedScenarioStore()

	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", scenarioBody("!!!", "", ""))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: want 422, got %d (%s)", rec.Code, rec.Body)
	}
}

// The curated ca-hsr baseline must stay read-only for everyone but admins.
func TestCuratedScenarioIsNotEditableByANonAdmin(t *testing.T) {
	for _, tc := range []struct {
		name string
		user transit.User
		want int
	}{
		{"a member", memberA, http.StatusNotFound},
		{"an admin", adminU, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedScenarioStore()
			rec := asScenarioUser(t, handler.UpdateOwnedScenario(store), tc.user,
				http.MethodPut, "/api/me/scenarios/ca-hsr", scenarioBody("Hijacked", "", ""))
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d (%s)", tc.want, rec.Code, rec.Body)
			}
		})
	}
}

func TestOwnedScenarioAnswers404ToStrangers(t *testing.T) {
	seed := func() *fakeOwnedScenarioStore {
		store := newFakeOwnedScenarioStore()
		store.scenarios["a-draft"] = transit.Scenario{
			ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID),
		}
		return store
	}

	for _, tc := range []struct {
		name string
		user transit.User
		want int
	}{
		{"its owner", memberA, http.StatusOK},
		{"a stranger", memberB, http.StatusNotFound},
		{"an admin", adminU, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := asScenarioUser(t, handler.GetOwnedScenario(seed()), tc.user,
				http.MethodGet, "/api/me/scenarios/a-draft", "")
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d", tc.want, rec.Code)
			}
		})
	}
}

// The slug is the address, and it is also what the travel-time set is keyed on,
// so a rename must not move it.
func TestUpdateOwnedScenarioKeepsTheSlugAndTheOwner(t *testing.T) {
	store := newFakeOwnedScenarioStore()
	store.scenarios["a-draft"] = transit.Scenario{
		ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID),
	}

	rec := asScenarioUser(t, handler.UpdateOwnedScenario(store), memberA,
		http.MethodPut, "/api/me/scenarios/a-draft", scenarioBody("Renamed", "new prose", "published"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Scenario
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Slug != "a-draft" {
		t.Errorf("slug: want it unchanged, got %q", got.Slug)
	}
	if got.Name != "Renamed" || got.Description != "new prose" || got.Status != "published" {
		t.Errorf("fields not updated: %+v", got)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want it unchanged, got %v", got.OwnerID)
	}
}

// Deleting cascades over everything under the scenario, which is safe only
// while the uniformity invariant holds. A curated child means it does not.
func TestDeleteOwnedScenarioRefusesWhenItHoldsCuratedContent(t *testing.T) {
	store := newFakeOwnedScenarioStore()
	store.scenarios["a-draft"] = transit.Scenario{
		ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID),
	}
	store.curated[ownedScrID] = 1

	rec := asScenarioUser(t, handler.DeleteOwnedScenario(store), memberA,
		http.MethodDelete, "/api/me/scenarios/a-draft", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d (%s)", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec.Body.Bytes()); code != "scenario_holds_curated_content" {
		t.Errorf("code: want scenario_holds_curated_content, got %q", code)
	}
	if _, still := store.scenarios["a-draft"]; !still {
		t.Error("the scenario was deleted despite the refusal")
	}
}

func TestDeleteOwnedScenarioSucceedsWhenEverythingUnderItIsTheCallersOwn(t *testing.T) {
	store := newFakeOwnedScenarioStore()
	store.scenarios["a-draft"] = transit.Scenario{
		ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID),
	}

	rec := asScenarioUser(t, handler.DeleteOwnedScenario(store), memberA,
		http.MethodDelete, "/api/me/scenarios/a-draft", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d (%s)", rec.Code, rec.Body)
	}
	if _, still := store.scenarios["a-draft"]; still {
		t.Error("the scenario survived its delete")
	}
}

// A client must not be able to claim an id or reassign ownership by putting
// them on the wire — the DTO carries neither, so both are silently ignored.
func TestOwnedScenarioIgnoresClientSuppliedIdentity(t *testing.T) {
	store := newFakeOwnedScenarioStore()

	body := `{"name":"Mine","id":"deadbeef","slug":"claimed","owner_id":"` + ownerBID + `"}`
	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Scenario
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID == "deadbeef" {
		t.Error("the client claimed an id")
	}
	if got.Slug == "claimed" {
		t.Error("the client claimed a slug")
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want the caller, got %v", got.OwnerID)
	}
}

func TestOwnedScenarioStorageFailureIsAnOpaque500(t *testing.T) {
	store := newFakeOwnedScenarioStore()
	store.failWith = fmt.Errorf("connection refused")

	rec := asScenarioUser(t, handler.CreateOwnedScenario(store), memberA,
		http.MethodPost, "/api/me/scenarios", scenarioBody("Mine", "", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Error("the underlying error leaked into the response body")
	}
}
