package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	ownerAID   = "00000000-0000-4100-8000-000000000001"
	ownerBID   = "00000000-0000-4100-8000-000000000002"
	ownedScrID = "00000000-0000-4101-8000-000000000001"
)

var (
	memberA = transit.User{ID: ownerAID, Email: "a@example.com"}
	memberB = transit.User{ID: ownerBID, Email: "b@example.com"}
	adminU  = transit.User{ID: "00000000-0000-4100-8000-000000000009", Email: "admin@example.com", IsAdmin: true}
)

// fakeOwnedRouteStore is an in-memory handler.OwnedRouteStore. failWith is the
// package's standard lever for exercising the 500 path.
type fakeOwnedRouteStore struct {
	routes    map[string]transit.Route // by slug
	scenarios map[string]transit.Scenario
	deps      map[string]transit.RouteDependents // by route id
	failWith  error
}

func newFakeOwnedRouteStore() *fakeOwnedRouteStore {
	return &fakeOwnedRouteStore{
		routes: map[string]transit.Route{},
		scenarios: map[string]transit.Scenario{
			// Curated platform data: nobody owns it.
			"ca-hsr": {ID: "00000000-0000-4001-8001-000000000001", Slug: "ca-hsr", Name: "CA HSR"},
			// A scenario member A owns.
			"a-draft": {ID: ownedScrID, Slug: "a-draft", Name: "A Draft", OwnerID: ptrTo(ownerAID)},
		},
		deps: map[string]transit.RouteDependents{},
	}
}

func ptrTo[T any](v T) *T { return &v }

func (f *fakeOwnedRouteStore) CreateRoute(_ context.Context, rt transit.Route) error {
	if f.failWith != nil {
		return f.failWith
	}
	if _, exists := f.routes[rt.Slug]; exists {
		return fmt.Errorf("duplicate slug %q", rt.Slug)
	}
	f.routes[rt.Slug] = rt
	return nil
}

func (f *fakeOwnedRouteStore) GetRouteBySlug(_ context.Context, slug string) (transit.Route, bool, error) {
	if f.failWith != nil {
		return transit.Route{}, false, f.failWith
	}
	rt, ok := f.routes[slug]
	return rt, ok, nil
}

func (f *fakeOwnedRouteStore) UpdateRoute(_ context.Context, rt transit.Route) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.routes[rt.Slug] = rt
	return nil
}

func (f *fakeOwnedRouteStore) DeleteRoute(_ context.Context, id string) error {
	if f.failWith != nil {
		return f.failWith
	}
	for slug, rt := range f.routes {
		if rt.ID == id {
			delete(f.routes, slug)
			return nil
		}
	}
	return fmt.Errorf("no route with id %q", id)
}

func (f *fakeOwnedRouteStore) CountRouteDependents(_ context.Context, routeID string) (transit.RouteDependents, error) {
	if f.failWith != nil {
		return transit.RouteDependents{}, f.failWith
	}
	return f.deps[routeID], nil
}

func (f *fakeOwnedRouteStore) ListRouteSummariesByOwner(_ context.Context, ownerID string) ([]transit.RouteSummary, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []transit.RouteSummary
	for _, rt := range f.routes {
		if rt.OwnerID != nil && *rt.OwnerID == ownerID {
			out = append(out, transit.RouteSummary{
				Slug: rt.Slug, Name: rt.Name, Description: rt.Description, Mode: rt.Mode,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (f *fakeOwnedRouteStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	if f.failWith != nil {
		return transit.Scenario{}, false, f.failWith
	}
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

// runWithSlug drives a handler with an identity already on the context, the way
// RequireAuth would have left it, and with the {slug} wildcard bound the way
// the mux would have. Both are supplied directly rather than by standing up the
// server, which is what keeps these unit tests about the handler.
func runWithSlug(t *testing.T, h http.HandlerFunc, user transit.User,
	method, target, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("slug", slug)
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// asUser is runWithSlug with the slug taken from a /api/me/routes target.
func asUser(t *testing.T, h http.HandlerFunc, user transit.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	slug := strings.Trim(strings.TrimPrefix(target, "/api/me/routes"), "/")
	return runWithSlug(t, h, user, method, target, slug, body)
}

func ownedRouteBody(name, description, scenarioSlug string) string {
	scenario := ""
	if scenarioSlug != "" {
		scenario = `"scenario_slug": "` + scenarioSlug + `",`
	}
	return `{
	  "type": "LineString",
	  "coordinates": [[-122.4, 37.79], [-122.3, 37.70]],
	  "properties": {
	    "name": "` + name + `",
	    "description": "` + description + `",
	    ` + scenario + `
	    "mode": "rail"
	  }
	}`
}

// The headline acceptance criterion: a member authors a route, and it is theirs.
func TestCreateOwnedRouteStampsTheCallerAsOwner(t *testing.T) {
	store := newFakeOwnedRouteStore()

	rec := asUser(t, handler.CreateOwnedRoute(store), memberA,
		http.MethodPost, "/api/me/routes", ownedRouteBody("Bay Link", "my draft", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.Route
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want %q, got %v", ownerAID, got.OwnerID)
	}
	if got.Slug != "bay-link" {
		t.Errorf("slug: want bay-link, got %q", got.Slug)
	}
	if got.Description != "my draft" {
		t.Errorf("description: want %q, got %q", "my draft", got.Description)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/me/routes/bay-link" {
		t.Errorf("Location: want /api/me/routes/bay-link, got %q", loc)
	}
}

// routes.slug is globally unique across curated and owned rows, so a user
// naming their draft after a curated alignment must get the next free slug
// rather than a constraint violation.
func TestCreateOwnedRouteWorksAroundACollidingCuratedSlug(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["bay-link"] = transit.Route{
		ID: "00000000-0000-4002-8000-000000000001", Slug: "bay-link", Name: "Bay Link",
	}

	rec := asUser(t, handler.CreateOwnedRoute(store), memberA,
		http.MethodPost, "/api/me/routes", ownedRouteBody("Bay Link", "", ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (%s)", rec.Code, rec.Body)
	}
	var got transit.Route
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Slug != "bay-link-2" {
		t.Errorf("slug: want bay-link-2, got %q", got.Slug)
	}
}

// The ownership-uniformity invariant: a member may author into their own
// scenario, and nowhere else.
func TestCreateOwnedRouteRefusesAScenarioTheCallerDoesNotOwn(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scenarioSlug string
		wantStatus   int
	}{
		{"their own scenario", "a-draft", http.StatusCreated},
		{"the curated baseline", "ca-hsr", http.StatusUnprocessableEntity},
		{"a scenario that does not exist", "nope", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedRouteStore()
			rec := asUser(t, handler.CreateOwnedRoute(store), memberA,
				http.MethodPost, "/api/me/routes", ownedRouteBody("Spur", "", tc.scenarioSlug))
			if rec.Code != tc.wantStatus {
				t.Errorf("status: want %d, got %d (%s)", tc.wantStatus, rec.Code, rec.Body)
			}
		})
	}
}

// The hole auth.CanAccess leaves on its own: it short-circuits on IsAdmin, so
// an admin passes it on a curated scenario, and the create path then stamps the
// *caller* as the child's owner. That is an owned route inside the curated
// ca-hsr baseline — which ListRoutesByScenario does not filter, so LoadStore
// would publish it into the compiled store.
//
// Two rules close it, and both are pinned here: /api/me refuses a curated
// parent outright, and a route that names a parent takes that parent's owner
// rather than the caller's.
func TestCreateOwnedRouteNeverLeavesAChildOwnedDifferentlyFromItsScenario(t *testing.T) {
	for _, tc := range []struct {
		name         string
		user         transit.User
		scenarioSlug string
		wantStatus   int
		wantOwner    *string // only checked on a 201
	}{
		{"an admin into the curated baseline", adminU, "ca-hsr", http.StatusUnprocessableEntity, nil},
		{"a member into the curated baseline", memberA, "ca-hsr", http.StatusUnprocessableEntity, nil},
		// The invariant's sole exception: no parent to agree with, so the route
		// carries the caller's own id.
		{"an admin standalone", adminU, "", http.StatusCreated, ptrTo(adminU.ID)},
		{"a member into their own scenario", memberA, "a-draft", http.StatusCreated, ptrTo(ownerAID)},
		// An admin may reach a member's scenario, but the row it leaves behind
		// belongs to the member, not the admin.
		{"an admin into a member's scenario", adminU, "a-draft", http.StatusCreated, ptrTo(ownerAID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedRouteStore()
			rec := asUser(t, handler.CreateOwnedRoute(store), tc.user,
				http.MethodPost, "/api/me/routes", ownedRouteBody("Spur", "", tc.scenarioSlug))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status: want %d, got %d (%s)", tc.wantStatus, rec.Code, rec.Body)
			}
			if tc.wantStatus != http.StatusCreated {
				if len(store.routes) != 0 {
					t.Errorf("a route was written despite the refusal: %+v", store.routes)
				}
				return
			}
			var got transit.Route
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding response: %v", err)
			}
			if got.OwnerID == nil || *got.OwnerID != *tc.wantOwner {
				t.Errorf("owner: want %q, got %v", *tc.wantOwner, got.OwnerID)
			}
		})
	}
}

// The same rule on the update path, which resolves the parent scenario a second
// time: a route cannot be moved into the curated baseline, and moving it into
// somebody's scenario hands it that scenario's owner.
func TestUpdateOwnedRouteCannotMoveARouteIntoTheCuratedBaseline(t *testing.T) {
	seed := func() *fakeOwnedRouteStore {
		store := newFakeOwnedRouteStore()
		store.routes["bay-link"] = transit.Route{
			ID: "00000000-0000-4002-8000-000000000001", Slug: "bay-link",
			Name: "Bay Link", OwnerID: ptrTo(ownerAID),
		}
		return store
	}

	t.Run("an admin moving it into ca-hsr", func(t *testing.T) {
		store := seed()
		rec := asUser(t, handler.UpdateOwnedRoute(store), adminU, http.MethodPut,
			"/api/me/routes/bay-link", ownedRouteBody("Bay Link", "", "ca-hsr"))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status: want 422, got %d (%s)", rec.Code, rec.Body)
		}
		if got := store.routes["bay-link"]; got.ScenarioID != nil {
			t.Errorf("scenario: want it unmoved, got %v", got.ScenarioID)
		}
	})

	t.Run("an admin moving it into the member's own scenario", func(t *testing.T) {
		store := seed()
		rec := asUser(t, handler.UpdateOwnedRoute(store), adminU, http.MethodPut,
			"/api/me/routes/bay-link", ownedRouteBody("Bay Link", "", "a-draft"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
		}
		got := store.routes["bay-link"]
		if got.OwnerID == nil || *got.OwnerID != ownerAID {
			t.Errorf("owner: want the scenario's owner %q, got %v", ownerAID, got.OwnerID)
		}
		if got.ScenarioID == nil || *got.ScenarioID != ownedScrID {
			t.Errorf("scenario: want %q, got %v", ownedScrID, got.ScenarioID)
		}
	})
}

// A non-owner gets 404, not 403: the set of authored slugs is not public
// knowledge, and a name-derived slug is guessable enough that 403 would confirm
// it exists.
func TestOwnedRouteAnswers404ToEveryoneButItsOwner(t *testing.T) {
	seed := func() *fakeOwnedRouteStore {
		store := newFakeOwnedRouteStore()
		store.routes["bay-link"] = transit.Route{
			ID: "00000000-0000-4002-8000-000000000001", Slug: "bay-link",
			Name: "Bay Link", OwnerID: ptrTo(ownerAID),
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
		t.Run(tc.name+" reading", func(t *testing.T) {
			rec := asUser(t, handler.GetOwnedRoute(seed()), tc.user,
				http.MethodGet, "/api/me/routes/bay-link", "")
			if rec.Code != tc.want {
				t.Errorf("status: want %d, got %d", tc.want, rec.Code)
			}
		})
		t.Run(tc.name+" deleting", func(t *testing.T) {
			want := tc.want
			if want == http.StatusOK {
				want = http.StatusNoContent
			}
			rec := asUser(t, handler.DeleteOwnedRoute(seed()), tc.user,
				http.MethodDelete, "/api/me/routes/bay-link", "")
			if rec.Code != want {
				t.Errorf("status: want %d, got %d", want, rec.Code)
			}
		})
	}
}

// A curated route has no owner, so CanAccess admits only admins. This is what
// keeps the seeded ca-hsr alignments read-only for everyone else.
func TestCuratedRouteIsNotEditableByANonAdmin(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["curated"] = transit.Route{
		ID: "00000000-0000-4002-8000-000000000002", Slug: "curated", Name: "Curated",
	}

	rec := asUser(t, handler.UpdateOwnedRoute(store), memberA,
		http.MethodPut, "/api/me/routes/curated", ownedRouteBody("Renamed", "", ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("member editing a curated route: want 404, got %d", rec.Code)
	}

	rec = asUser(t, handler.UpdateOwnedRoute(store), adminU,
		http.MethodPut, "/api/me/routes/curated", ownedRouteBody("Renamed", "", ""))
	if rec.Code != http.StatusOK {
		t.Errorf("admin editing a curated route: want 200, got %d (%s)", rec.Code, rec.Body)
	}
}

// The slug is the route's address: renaming must not move it, or every link to
// it breaks and the travel-time segments naming it are orphaned.
func TestUpdateOwnedRouteKeepsTheSlugAndTheOwner(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["bay-link"] = transit.Route{
		ID: "00000000-0000-4002-8000-000000000001", Slug: "bay-link",
		Name: "Bay Link", OwnerID: ptrTo(ownerAID),
	}

	rec := asUser(t, handler.UpdateOwnedRoute(store), memberA,
		http.MethodPut, "/api/me/routes/bay-link", ownedRouteBody("Completely Renamed", "now with prose", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var got transit.Route
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Slug != "bay-link" {
		t.Errorf("slug: want it unchanged at bay-link, got %q", got.Slug)
	}
	if got.Name != "Completely Renamed" {
		t.Errorf("name: want it updated, got %q", got.Name)
	}
	if got.Description != "now with prose" {
		t.Errorf("description: want it updated, got %q", got.Description)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerAID {
		t.Errorf("owner: want it unchanged, got %v", got.OwnerID)
	}
}

// user_services.route_id is ON DELETE CASCADE, so an unchecked delete would
// silently destroy someone's saved services. The 409 is what makes that
// cascade unreachable through the API.
func TestDeleteOwnedRouteRefusesWhileAnythingDependsOnIt(t *testing.T) {
	store := newFakeOwnedRouteStore()
	const id = "00000000-0000-4002-8000-000000000001"
	store.routes["bay-link"] = transit.Route{
		ID: id, Slug: "bay-link", Name: "Bay Link", OwnerID: ptrTo(ownerAID),
	}
	store.deps[id] = transit.RouteDependents{UserServices: 2}

	rec := asUser(t, handler.DeleteOwnedRoute(store), memberA,
		http.MethodDelete, "/api/me/routes/bay-link", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d (%s)", rec.Code, rec.Body)
	}
	if code := errorCodeOf(t, rec.Body.Bytes()); code != "route_in_use" {
		t.Errorf("code: want route_in_use, got %q", code)
	}
	if _, still := store.routes["bay-link"]; !still {
		t.Error("the route was deleted despite the refusal")
	}
}

func TestMyRoutesReturnsOnlyTheCallersOwnRoutes(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["mine"] = transit.Route{
		ID: "1", Slug: "mine", Name: "Mine", OwnerID: ptrTo(ownerAID),
	}
	store.routes["theirs"] = transit.Route{
		ID: "2", Slug: "theirs", Name: "Theirs", OwnerID: ptrTo(ownerBID),
	}
	store.routes["curated"] = transit.Route{ID: "3", Slug: "curated", Name: "Curated"}

	rec := asUser(t, handler.MyRoutes(store), memberA, http.MethodGet, "/api/me/routes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}

	var got []transit.RouteSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "mine" {
		t.Errorf("want only the caller's own route, got %+v", got)
	}
}

// Admins are scoped to their own rows here, matching MyServices: admin rights
// gate privileged endpoints, they do not redefine what "mine" means.
func TestMyRoutesDoesNotWidenForAdmins(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["theirs"] = transit.Route{
		ID: "2", Slug: "theirs", Name: "Theirs", OwnerID: ptrTo(ownerBID),
	}

	rec := asUser(t, handler.MyRoutes(store), adminU, http.MethodGet, "/api/me/routes", "")

	var got []transit.RouteSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Errorf("want an admin's own (empty) list, got %+v", got)
	}
}

// An empty owned list is [] on the wire, never null.
func TestMyRoutesEmitsAnEmptyListNotNull(t *testing.T) {
	rec := asUser(t, handler.MyRoutes(newFakeOwnedRouteStore()), memberA,
		http.MethodGet, "/api/me/routes", "")
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body: want [], got %q", body)
	}
}

// A storage failure is an opaque 500 — the underlying error is logged, never
// returned.
func TestOwnedRouteStorageFailureIsAnOpaque500(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.failWith = fmt.Errorf("connection refused")

	rec := asUser(t, handler.MyRoutes(store), memberA, http.MethodGet, "/api/me/routes", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Error("the underlying error leaked into the response body")
	}
}

// An unauthenticated request never reaches these handlers in production —
// RequireAuth is in front of them — but the guard is what makes that a
// belt-and-braces property rather than an assumption.
func TestOwnedRouteHandlersRequireAnIdentity(t *testing.T) {
	store := newFakeOwnedRouteStore()
	for name, h := range map[string]http.HandlerFunc{
		"list":   handler.MyRoutes(store),
		"create": handler.CreateOwnedRoute(store),
		"get":    handler.GetOwnedRoute(store),
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/me/routes", strings.NewReader(""))
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status: want 401, got %d", rec.Code)
			}
		})
	}
}

func errorCodeOf(t *testing.T, body []byte) string {
	t.Helper()
	var parsed struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	return parsed.Code
}
