package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// preNow anchors every timestamp in this file, so "the member changed after
// the entry was curated" is a fact about the fixtures rather than about how
// long the test took to run.
var preNow = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

const preScenarioSlug = "ca-hsr"

var (
	preAdmin     = transit.User{ID: "admin-9", Email: "admin@example.com", IsAdmin: true}
	preNonAdmin  = transit.User{ID: "user-9", Email: "user@example.com"}
	preAdminTok  = "prerendered-admin-token"
	preUserToken = "prerendered-user-token"
)

// samplePayload stands in for a real isochrone payload. Its shape is
// deliberately arbitrary: the API stores and serves these opaquely, so a test
// that used a "correct" GeoJSON shape would be asserting a contract this
// repository does not have.
const samplePayload = `{"contours":[{"minutes":30,"polygon":"opaque"}],"note":"not parsed by the API"}`

// fakePrerenderedStore is an in-memory handler.PrerenderedStore.
//
// It mirrors the one thing about the real store that the endpoints' contract
// depends on: the list read does not carry payloads. Returning them here would
// let a handler that forgot to drop Result pass a test the Postgres store
// would fail, which is precisely the regression these tests exist to catch.
type fakePrerenderedStore struct {
	scenarios map[string]transit.Scenario            // by slug
	members   map[string][]transit.ServiceMembership // by scenario id
	entries   map[string]transit.PrerenderedIsochrone
	order     []string // insertion order, standing in for ORDER BY created_at

	scenarioErr error
	membersErr  error
	listErr     error
	getErr      error
	createErr   error
}

func newFakePrerenderedStore() *fakePrerenderedStore {
	return &fakePrerenderedStore{
		scenarios: map[string]transit.Scenario{
			preScenarioSlug: {ID: "scenario-1", Slug: preScenarioSlug, Name: "California HSR"},
		},
		members: map[string][]transit.ServiceMembership{
			"scenario-1": {
				{ServiceID: "svc-1", UpdatedAt: preNow.Add(-time.Hour)},
				{ServiceID: "svc-2", UpdatedAt: preNow.Add(-time.Minute)},
			},
		},
		entries: map[string]transit.PrerenderedIsochrone{},
	}
}

func (f *fakePrerenderedStore) GetScenarioBySlug(_ context.Context, slug string) (transit.Scenario, bool, error) {
	if f.scenarioErr != nil {
		return transit.Scenario{}, false, f.scenarioErr
	}
	sc, ok := f.scenarios[slug]
	return sc, ok, nil
}

func (f *fakePrerenderedStore) ListServiceMembershipByScenario(_ context.Context, scenarioID string) ([]transit.ServiceMembership, error) {
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	return f.members[scenarioID], nil
}

func (f *fakePrerenderedStore) ListPrerenderedIsochronesByScenario(_ context.Context, slug string) ([]transit.PrerenderedIsochrone, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Deliberately nil, not [], when there is nothing: the [] on the wire must
	// be the handler's guarantee and not an accident of what the store returned.
	var out []transit.PrerenderedIsochrone
	for _, id := range f.order {
		p := f.entries[id]
		if p.ScenarioSlug != slug {
			continue
		}
		p.Result = nil // the real query does not select this column
		out = append(out, p)
	}
	return out, nil
}

func (f *fakePrerenderedStore) GetPrerenderedIsochrone(_ context.Context, id string) (transit.PrerenderedIsochrone, bool, error) {
	if f.getErr != nil {
		return transit.PrerenderedIsochrone{}, false, f.getErr
	}
	p, ok := f.entries[id]
	return p, ok, nil
}

func (f *fakePrerenderedStore) CreatePrerenderedIsochrone(_ context.Context, p *transit.PrerenderedIsochrone) error {
	if f.createErr != nil {
		return f.createErr
	}
	// The real store fills these in from the database and the 201 carries
	// them, so a fake that left them zero would hide a handler that never
	// persisted the row.
	p.CreatedAt = preNow
	p.UpdatedAt = preNow
	f.entries[p.ID] = *p
	f.order = append(f.order, p.ID)
	return nil
}

// put seeds an entry directly, for the read tests that need rows without
// curating them through the admin endpoint first.
func (f *fakePrerenderedStore) put(p transit.PrerenderedIsochrone) transit.PrerenderedIsochrone {
	if p.ScenarioSlug == "" {
		p.ScenarioSlug = preScenarioSlug
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = preNow
	}
	f.entries[p.ID] = p
	f.order = append(f.order, p.ID)
	return p
}

// seededEntry is a curated entry that is current: its snapshot names exactly
// the scenario's members, all of which last changed before it was curated.
func seededEntry(id, label string) transit.PrerenderedIsochrone {
	return transit.PrerenderedIsochrone{
		ID:                 id,
		ScenarioSlug:       preScenarioSlug,
		Label:              label,
		Lat:                37.3297,
		Lng:                -121.9020,
		BudgetMins:         240,
		Mode:               transit.TravelModeBike,
		Result:             json.RawMessage(samplePayload),
		CompiledServiceIDs: []string{"svc-1", "svc-2"},
		CreatedAt:          preNow,
	}
}

// prerenderedMux registers the three endpoints the way server.New does: the
// two reads open, the write behind auth.RequireAdmin. The gate is composed
// here rather than assumed away because "who may curate one" is half the
// contract of the POST, and the handler itself deliberately does not check —
// it trusts its registration, so the registration is what a test must include.
func prerenderedMux(store handler.PrerenderedStore) *http.ServeMux {
	lookup := func(_ context.Context, tokenHash string) (transit.User, bool, error) {
		switch tokenHash {
		case auth.HashToken(preAdminTok):
			return preAdmin, true, nil
		case auth.HashToken(preUserToken):
			return preNonAdmin, true, nil
		}
		return transit.User{}, false, nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/scenarios/{slug}/prerendered-isochrones", handler.PrerenderedIsochrones(store))
	mux.HandleFunc("GET /api/prerendered-isochrones/{id}", handler.PrerenderedIsochrone(store))
	mux.Handle("POST /api/scenarios/{slug}/prerendered-isochrones",
		auth.RequireAdmin(lookup)(handler.CreatePrerenderedIsochrone(store)))
	return mux
}

// preRequest serves one request through that mux, with an optional bearer
// token. An empty token is an anonymous caller.
func preRequest(t *testing.T, store handler.PrerenderedStore, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, http.NoBody)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	prerenderedMux(store).ServeHTTP(rec, r)
	return rec
}

// decodePrerenderedList decodes a response body as raw JSON objects, so a test can ask
// whether a key is present at all rather than whether a struct field ended up
// zero — the difference the list contract turns on.
func decodePrerenderedList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return out
}

func decodePrerendered(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return out
}

func listPrerendered(t *testing.T, store handler.PrerenderedStore) *httptest.ResponseRecorder {
	t.Helper()
	return preRequest(t, store, http.MethodGet,
		"/api/scenarios/"+preScenarioSlug+"/prerendered-isochrones", "", "")
}

// --- list ---

// The list is metadata only. Payloads run 300-500KB apiece, so a list that
// carried them would be megabytes of data the caller discards to render a row
// of labels — and the store's query does not even select the column.
func TestPrerenderedIsochrones_200_listsMetadataWithoutResult(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "San Jose — 240 min by bike"))
	store.put(seededEntry("entry-2", "Burbank walk reach"))

	rec := listPrerendered(t, store)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	items := decodePrerenderedList(t, rec)
	if len(items) != 2 {
		t.Fatalf("got %d entries, want 2; body %s", len(items), rec.Body.String())
	}
	for i, item := range items {
		if _, present := item["result"]; present {
			t.Errorf("entry %d carries a result payload; the list must never include one", i)
		}
		for _, key := range []string{"id", "label", "lat", "lng", "budget_mins", "mode", "outdated", "created_at"} {
			if _, present := item[key]; !present {
				t.Errorf("entry %d is missing %q; body %s", i, key, rec.Body.String())
			}
		}
	}

	first := items[0]
	if first["id"] != "entry-1" || first["label"] != "San Jose — 240 min by bike" {
		t.Errorf("first entry = %v", first)
	}
	if first["mode"] != string(transit.TravelModeBike) {
		t.Errorf("mode = %v, want bike", first["mode"])
	}
	if first["budget_mins"] != float64(240) {
		t.Errorf("budget_mins = %v, want 240", first["budget_mins"])
	}
}

// A scenario with nothing curated answers [], never null. A client mapping
// over the response should not have to special-case an absent list.
func TestPrerenderedIsochrones_200_emptyScenarioReturnsEmptyArray(t *testing.T) {
	rec := listPrerendered(t, newFakePrerenderedStore())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

// An unknown slug is a mistake the caller made; an empty list would say the
// scenario exists and has nothing, which is a different fact.
func TestPrerenderedIsochrones_404_unknownScenario(t *testing.T) {
	rec := preRequest(t, newFakePrerenderedStore(), http.MethodGet,
		"/api/scenarios/no-such-scenario/prerendered-isochrones", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

// --- detail ---

// The detail read is the one that exists to move a payload, and it serves it
// back exactly as stored — the API neither produced nor parsed it.
func TestPrerenderedIsochrone_200_returnsFullResult(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "San Jose — 240 min by bike"))

	rec := preRequest(t, store, http.MethodGet, "/api/prerendered-isochrones/entry-1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}

	var got struct {
		ID     string          `json:"id"`
		Label  string          `json:"label"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "entry-1" {
		t.Errorf("id = %q", got.ID)
	}
	if len(got.Result) == 0 {
		t.Fatal("detail response carries no result payload")
	}

	// Compared as decoded JSON rather than as bytes: the payload must survive
	// the round trip semantically, and only the encoder decides its spacing.
	var want, have any
	if err := json.Unmarshal([]byte(samplePayload), &want); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if err := json.Unmarshal(got.Result, &have); err != nil {
		t.Fatalf("unmarshal served payload: %v", err)
	}
	if !jsonEqual(want, have) {
		t.Errorf("payload changed in transit:\n got %s\nwant %s", got.Result, samplePayload)
	}

	// Metadata still travels with it — a client fetching one entry should not
	// have to hold the list to know what it is looking at.
	obj := decodePrerendered(t, rec)
	for _, key := range []string{"label", "lat", "lng", "budget_mins", "mode", "outdated", "created_at"} {
		if _, present := obj[key]; !present {
			t.Errorf("detail response is missing %q", key)
		}
	}
}

func TestPrerenderedIsochrone_404_unknownID(t *testing.T) {
	rec := preRequest(t, newFakePrerenderedStore(), http.MethodGet,
		"/api/prerendered-isochrones/no-such-id", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

// --- outdated, computed on read ---

// The membership arm: the scenario gained a service after the entry was
// curated, so the snapshot no longer describes it.
func TestPrerenderedIsochrones_200_outdatedWhenMembershipChanged(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "curated before svc-2 existed"))
	// The stored snapshot names svc-1 and svc-2; the scenario now has three.
	store.members["scenario-1"] = append(store.members["scenario-1"],
		transit.ServiceMembership{ServiceID: "svc-3", UpdatedAt: preNow.Add(-time.Minute)})

	if got := outdatedOf(t, listPrerendered(t, store), 0); !got {
		t.Error("outdated = false, want true: the scenario's membership grew after this entry was curated")
	}
}

// A member that was deleted cascades out of membership without touching any
// timestamp, which is the case timestamps alone cannot see (SPA-116).
func TestPrerenderedIsochrones_200_outdatedWhenMemberRemoved(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "curated while svc-2 was a member"))
	store.members["scenario-1"] = store.members["scenario-1"][:1]

	if got := outdatedOf(t, listPrerendered(t, store), 0); !got {
		t.Error("outdated = false, want true: a snapshotted member is no longer in the scenario")
	}
}

// The fallback arm: membership is unchanged, but a still-present member was
// edited after the entry was curated, so the payload predates that edit.
func TestPrerenderedIsochrones_200_outdatedWhenMemberEditedAfterCreation(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "curated before svc-2 was edited"))
	store.members["scenario-1"] = []transit.ServiceMembership{
		{ServiceID: "svc-1", UpdatedAt: preNow.Add(-time.Hour)},
		{ServiceID: "svc-2", UpdatedAt: preNow.Add(time.Minute)},
	}

	if got := outdatedOf(t, listPrerendered(t, store), 0); !got {
		t.Error("outdated = false, want true: a still-present member changed after this entry was curated")
	}
}

func TestPrerenderedIsochrones_200_notOutdatedWhenFresh(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "still current"))

	if got := outdatedOf(t, listPrerendered(t, store), 0); got {
		t.Error("outdated = true, want false: membership and timestamps are unchanged")
	}
}

// An outdated entry is reported, never withheld: the payload is still the
// thing the caller asked for, and "this was computed before the scenario
// changed" is a caption rather than a refusal.
func TestPrerenderedIsochrone_200_outdatedEntryIsStillServedWithItsPayload(t *testing.T) {
	store := newFakePrerenderedStore()
	store.put(seededEntry("entry-1", "curated before the scenario changed"))
	store.members["scenario-1"] = store.members["scenario-1"][:1]

	rec := preRequest(t, store, http.MethodGet, "/api/prerendered-isochrones/entry-1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; an outdated entry must not be refused", rec.Code)
	}
	obj := decodePrerendered(t, rec)
	if obj["outdated"] != true {
		t.Errorf("outdated = %v, want true", obj["outdated"])
	}
	if _, present := obj["result"]; !present {
		t.Error("an outdated entry was served without its payload")
	}
}

// --- create ---

const createPrerenderedBody = `{
	"label": "San Jose — 240 min by bike",
	"lat": 37.3297,
	"lng": -121.9020,
	"budget_mins": 240,
	"mode": "bike",
	"result": {"contours":[{"minutes":240,"polygon":"opaque"}]}
}`

func createPrerendered(t *testing.T, store handler.PrerenderedStore, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return preRequest(t, store, http.MethodPost,
		"/api/scenarios/"+preScenarioSlug+"/prerendered-isochrones", token, body)
}

func TestCreatePrerenderedIsochrone_401_anonymous(t *testing.T) {
	store := newFakePrerenderedStore()
	rec := createPrerendered(t, store, "", createPrerenderedBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
	}
	if len(store.entries) != 0 {
		t.Error("an anonymous caller curated an entry")
	}
}

func TestCreatePrerenderedIsochrone_403_nonAdmin(t *testing.T) {
	store := newFakePrerenderedStore()
	rec := createPrerendered(t, store, preUserToken, createPrerenderedBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	if len(store.entries) != 0 {
		t.Error("a non-admin curated an entry")
	}
}

func TestCreatePrerenderedIsochrone_201_admin(t *testing.T) {
	store := newFakePrerenderedStore()
	rec := createPrerendered(t, store, preAdminTok, createPrerenderedBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	obj := decodePrerendered(t, rec)
	if _, present := obj["result"]; present {
		t.Error("the 201 echoed the payload back; the caller just sent it")
	}
	if obj["outdated"] != false {
		t.Errorf("outdated = %v, want false: it was snapshotted a moment ago", obj["outdated"])
	}
	id, _ := obj["id"].(string)
	if id == "" {
		t.Fatal("the created entry has no id")
	}
	if obj["created_at"] != preNow.Format(time.RFC3339Nano) {
		t.Errorf("created_at = %v, want the store-assigned %v", obj["created_at"], preNow)
	}

	stored, ok := store.entries[id]
	if !ok {
		t.Fatalf("entry %q was not persisted", id)
	}
	if stored.ScenarioSlug != preScenarioSlug {
		t.Errorf("scenario_slug = %q, want %q", stored.ScenarioSlug, preScenarioSlug)
	}
	if len(stored.Result) == 0 {
		t.Error("the payload was not persisted")
	}
	// The snapshot is the whole reason a later read can say "outdated".
	if got := stored.CompiledServiceIDs; len(got) != 2 || got[0] != "svc-1" || got[1] != "svc-2" {
		t.Errorf("compiled_service_ids = %v, want the scenario's current members", got)
	}
}

// The payload is opaque: anything that is JSON at all is accepted, because
// this API does not produce isochrones and has no shape to check one against.
func TestCreatePrerenderedIsochrone_201_acceptsAnyResultShape(t *testing.T) {
	for _, payload := range []string{
		`{"anything":"at all"}`,
		`[1,2,3]`,
		`"a bare string"`,
		`{"type":"FeatureCollection","features":[]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			store := newFakePrerenderedStore()
			body := `{"label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":` + payload + `}`
			rec := createPrerendered(t, store, preAdminTok, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCreatePrerenderedIsochrone_400_invalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{"label":`},
		{"missing label", `{"lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":{}}`},
		{"blank label", `{"label":"   ","lat":1,"lng":2,"budget_mins":30,"mode":"walk","result":{}}`},
		{"unknown mode", `{"label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"teleport","result":{}}`},
		{"missing mode", `{"label":"L","lat":1,"lng":2,"budget_mins":30,"result":{}}`},
		{"zero budget", `{"label":"L","lat":1,"lng":2,"budget_mins":0,"mode":"walk","result":{}}`},
		{"missing result", `{"label":"L","lat":1,"lng":2,"budget_mins":30,"mode":"walk"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakePrerenderedStore()
			rec := createPrerendered(t, store, preAdminTok, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			if len(store.entries) != 0 {
				t.Error("an entry was curated despite invalid input")
			}
		})
	}
}

func TestCreatePrerenderedIsochrone_404_unknownScenario(t *testing.T) {
	store := newFakePrerenderedStore()
	rec := preRequest(t, store, http.MethodPost,
		"/api/scenarios/no-such-scenario/prerendered-isochrones", preAdminTok, createPrerenderedBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if len(store.entries) != 0 {
		t.Error("an entry was curated against a scenario that does not exist")
	}
}

// An entry curated through the endpoint is immediately listable and fetchable,
// which is the only proof that the write and the two reads agree on identity.
func TestCreatePrerenderedIsochrone_201_isImmediatelyReadable(t *testing.T) {
	store := newFakePrerenderedStore()
	created := createPrerendered(t, store, preAdminTok, createPrerenderedBody)
	if created.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", created.Code, created.Body.String())
	}
	id, _ := decodePrerendered(t, created)["id"].(string)

	items := decodePrerenderedList(t, listPrerendered(t, store))
	if len(items) != 1 || items[0]["id"] != id {
		t.Fatalf("list = %v, want the entry just curated (%s)", items, id)
	}
	if items[0]["outdated"] != false {
		t.Errorf("outdated = %v, want false right after curation", items[0]["outdated"])
	}

	detail := preRequest(t, store, http.MethodGet, "/api/prerendered-isochrones/"+id, "", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.Code)
	}
	if _, present := decodePrerendered(t, detail)["result"]; !present {
		t.Error("the curated payload is not served back by the detail read")
	}
}

// --- helpers ---

// outdatedOf reads the outdated flag of the nth listed entry.
func outdatedOf(t *testing.T, rec *httptest.ResponseRecorder, n int) bool {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	items := decodePrerenderedList(t, rec)
	if n >= len(items) {
		t.Fatalf("entry %d not in a list of %d", n, len(items))
	}
	flag, ok := items[n]["outdated"].(bool)
	if !ok {
		t.Fatalf("outdated is %T, want bool", items[n]["outdated"])
	}
	return flag
}

// jsonEqual compares two decoded JSON values structurally.
func jsonEqual(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
}
