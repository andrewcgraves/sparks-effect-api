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

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
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
	memberA = account.User{ID: ownerAID, Email: "a@example.com"}
	memberB = account.User{ID: ownerBID, Email: "b@example.com"}
	adminU  = account.User{ID: "00000000-0000-4100-8000-000000000009", Email: "admin@example.com", IsAdmin: true}
)

type fakeOwnedRouteStore struct {
	routes    map[string]transit.Route
	scenarios map[string]transit.Scenario
	deps      map[string]transit.RouteDependents
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

func (f *fakeOwnedRouteStore) ListRoutesByOwner(_ context.Context, ownerID string) ([]transit.Route, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}
	var out []transit.Route
	for _, rt := range f.routes {
		if rt.OwnerID != nil && *rt.OwnerID == ownerID {
			out = append(out, rt)
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

func runWithSlug(t *testing.T, h http.HandlerFunc, user account.User,
	method, target, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("slug", slug)
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func asUser(t *testing.T, h http.HandlerFunc, user account.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	slug := strings.Trim(strings.TrimPrefix(target, "/api/me/routes"), "/")
	return runWithSlug(t, h, user, method, target, slug, body)
}

const (
	bayLinkID     = "00000000-0000-4002-8000-000000000001"
	bayLinkCoords = `[[-122.4, 37.79], [-122.3, 37.70]]`
)

// The same alignment ownedRouteBody sends, so a body built from it with
// only the properties changed is a non-geometry edit of this route.
func bayLink() transit.Route {
	return transit.Route{
		ID: bayLinkID, Slug: "bay-link", Name: "Bay Link", Mode: "rail",
		OwnerID: ptrTo(ownerAID), Bidirectional: true,
		Geometry: transit.GeoLineString{
			Type: "LineString", Coordinates: [][]float64{{-122.4, 37.79}, {-122.3, 37.70}},
		},
	}
}

func routeBody(coordinates, properties string) string {
	return `{"type": "LineString", "coordinates": ` + coordinates + `, "properties": {` + properties + `}}`
}

func ownedRouteBody(name, description, scenarioSlug string) string {
	scenario := ""
	if scenarioSlug != "" {
		scenario = `"scenario_slug": "` + scenarioSlug + `",`
	}
	return routeBody(bayLinkCoords,
		`"name": "`+name+`", "description": "`+description+`", `+scenario+` "mode": "rail"`)
}

// Bay Link's two points are 0.1° of longitude and 0.09° of latitude apart
// at 37.7°N: about 8.8 km east-west and 10.0 km north-south, so roughly
// 13.3 km along the line.
func assertBayLinkLength(t *testing.T, lengthM float64) {
	t.Helper()
	if lengthM < 13_000 || lengthM > 13_700 {
		t.Errorf("length_m: want about 13.3 km, got %v", lengthM)
	}
}

func assertHasKeys(t *testing.T, raw json.RawMessage, keys ...string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decoding object: %v", err)
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			t.Errorf("response is missing %q: %s", k, raw)
		}
	}
}

func TestGetOwnedRouteReportsLengthAndDependents(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["bay-link"] = bayLink()
	store.deps[bayLinkID] = transit.RouteDependents{Services: 1, Segments: 3}

	rec := asUser(t, handler.GetOwnedRoute(store), memberA,
		http.MethodGet, "/api/me/routes/bay-link", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	assertHasKeys(t, rec.Body.Bytes(), "id", "slug", "geometry", "length_m", "dependents")

	var got struct {
		transit.Route
		LengthM    float64                 `json:"length_m"`
		Dependents transit.RouteDependents `json:"dependents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ID != bayLinkID || got.Slug != "bay-link" || len(got.Geometry.Coordinates) != 2 {
		t.Errorf("route: want Bay Link with its geometry, got %+v", got.Route)
	}
	assertBayLinkLength(t, got.LengthM)
	if want := (transit.RouteDependents{Services: 1, Segments: 3}); got.Dependents != want {
		t.Errorf("dependents: want %+v, got %+v", want, got.Dependents)
	}
}

func TestMyRoutesItemsCarryIDLengthAndDependents(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["bay-link"] = bayLink()
	store.deps[bayLinkID] = transit.RouteDependents{UserServices: 2}

	rec := asUser(t, handler.MyRoutes(store), memberA, http.MethodGet, "/api/me/routes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw) != 1 {
		t.Fatalf("want one item, got %s (%v)", rec.Body, err)
	}
	assertHasKeys(t, raw[0], "id", "slug", "name", "mode", "length_m", "dependents")

	var got transit.OwnedRouteSummary
	if err := json.Unmarshal(raw[0], &got); err != nil {
		t.Fatalf("decoding item: %v", err)
	}
	if got.ID != bayLinkID || got.Slug != "bay-link" || got.Name != "Bay Link" || got.Mode != "rail" {
		t.Errorf("item: want Bay Link's identity, got %+v", got)
	}
	assertBayLinkLength(t, got.LengthM)
	if want := (transit.RouteDependents{UserServices: 2}); got.Dependents != want {
		t.Errorf("dependents: want %+v, got %+v", want, got.Dependents)
	}
}

func TestUpdateOwnedRouteRefusesGeometryEditsWhileInUse(t *testing.T) {
	const (
		props       = `"name": "Bay Link", "mode": "rail"`
		movedCoords = `[[-122.4, 37.79], [-122.35, 37.75], [-122.3, 37.70]]`
	)
	for _, tc := range []struct {
		name string
		body string
		free bool
		want int
	}{
		{"moving the alignment", routeBody(movedCoords, props), false, http.StatusConflict},
		{"changing the physics",
			routeBody(bayLinkCoords, props+`, "segments": [{"cant_mm": 50, "curve_radius_m": 2000, "grade_pct": 1}]`),
			false, http.StatusConflict},
		{"renaming", ownedRouteBody("Bay Link Renamed", "", ""), false, http.StatusOK},
		{"describing", ownedRouteBody("Bay Link", "now with prose", ""), false, http.StatusOK},
		{"changing mode", routeBody(bayLinkCoords, `"name": "Bay Link", "mode": "metro"`), false, http.StatusOK},
		{"making it one-way", routeBody(bayLinkCoords, props+`, "bidirectional": false`), false, http.StatusOK},
		{"moving an alignment nothing depends on", routeBody(movedCoords, props), true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeOwnedRouteStore()
			store.routes["bay-link"] = bayLink()
			if !tc.free {
				store.deps[bayLinkID] = transit.RouteDependents{UserServices: 2}
			}

			rec := asUser(t, handler.UpdateOwnedRoute(store), memberA,
				http.MethodPut, "/api/me/routes/bay-link", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status: want %d, got %d (%s)", tc.want, rec.Code, rec.Body)
			}
			if tc.want != http.StatusConflict {
				return
			}

			var refusal struct {
				Code   string                  `json:"code"`
				Detail transit.RouteDependents `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
				t.Fatalf("decoding error body: %v", err)
			}
			if refusal.Code != "route_in_use" {
				t.Errorf("code: want route_in_use, got %q", refusal.Code)
			}
			if refusal.Detail.UserServices != 2 {
				t.Errorf("detail: want the dependents DELETE reports, got %+v", refusal.Detail)
			}
			if len(store.routes["bay-link"].Geometry.Coordinates) != 2 || store.routes["bay-link"].Segments != nil {
				t.Error("the route was changed despite the refusal")
			}
		})
	}
}

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

func TestCreateOwnedRouteValidationRejectionCarriesStructuredDetail(t *testing.T) {
	body := `{
	  "type": "LineString",
	  "coordinates": [[-122.4, 91], [-122.3, 37.70]],
	  "properties": { "name": "Bay Link", "mode": "rail" }
	}`
	rec := asUser(t, handler.CreateOwnedRoute(newFakeOwnedRouteStore()), memberA,
		http.MethodPost, "/api/me/routes", body)
	got := decodeValidationFault(t, rec)
	if got.Code != handler.ValidationErrorCode {
		t.Errorf("code = %q, want %q", got.Code, handler.ValidationErrorCode)
	}
	if len(got.Detail.Faults) != 1 {
		t.Fatalf("got %d faults, want 1: %+v", len(got.Detail.Faults), got.Detail.Faults)
	}
	if got.Detail.Faults[0].Field != "coordinates.lat" || got.Detail.Faults[0].Index == nil || *got.Detail.Faults[0].Index != 0 {
		t.Errorf("fault = %+v, want coordinates.lat at index 0", got.Detail.Faults[0])
	}
}

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
		user account.User
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

	var got []transit.OwnedRouteSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "mine" {
		t.Errorf("want only the caller's own route, got %+v", got)
	}
}

func TestMyRoutesDoesNotWidenForAdmins(t *testing.T) {
	store := newFakeOwnedRouteStore()
	store.routes["theirs"] = transit.Route{
		ID: "2", Slug: "theirs", Name: "Theirs", OwnerID: ptrTo(ownerBID),
	}

	rec := asUser(t, handler.MyRoutes(store), adminU, http.MethodGet, "/api/me/routes", "")

	var got []transit.OwnedRouteSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Errorf("want an admin's own (empty) list, got %+v", got)
	}
}

func TestMyRoutesEmitsAnEmptyListNotNull(t *testing.T) {
	rec := asUser(t, handler.MyRoutes(newFakeOwnedRouteStore()), memberA,
		http.MethodGet, "/api/me/routes", "")
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body: want [], got %q", body)
	}
}

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
