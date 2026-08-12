package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func userIsochroneMux(store handler.ScenarioIsochroneStore, pub routing.Publisher) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/user-scenarios/{slug}/isochrone", handler.UserScenarioIsochrone(store, pub, logger.Discard(), transit.DefaultBoardingWaitPolicy()))
	return mux
}

func isoServeAs(t *testing.T, store handler.ScenarioIsochroneStore, pub routing.Publisher, user transit.User, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if user.ID != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	rec := httptest.NewRecorder()
	userIsochroneMux(store, pub).ServeHTTP(rec, r)
	return rec
}

const isoValidBody = `{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"walk"}`

func freshGraph() *transit.TransitGraph {
	return &transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: "svc-1", WaitSecs: 0, WaitPolicy: string(transit.BoardingWaitNone),
			Edges: []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 300}},
		}},
		Nodes: []transit.GraphNode{
			{Slug: "a", Lat: 37.7, Lng: -122.4, Names: []string{"A"}},
			{Slug: "b", Lat: 37.71, Lng: -122.41, Names: []string{"B"}},
		},
	}
}

func TestUserScenarioIsochrone_401_unauthenticated(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, &routing.FakePublisher{}, transit.User{}, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_unknownSlug(t *testing.T) {
	store := newFakeScenarioStore()

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/nope/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_nonOwner(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnStranger, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_404_noCompiledGraphYet(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_400_invalidMode(t *testing.T) {
	store := newFakeScenarioStore()
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone",
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_409_deletedMember(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	// The job compiled svc-1 and svc-2; svc-2 has since been deleted, so the
	// scenario's current membership (svc-1 only) no longer matches — the exact
	// gap SPA-116 closes.
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1", "svc-2"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code: want %q, got %q", handler.StaleGraphErrorCode, body["code"])
	}
	// The stale response must never leak the outdated graph data.
	if _, ok := body["features"]; ok {
		t.Error("409 response leaks graph features")
	}
}

func TestUserScenarioIsochrone_409_editedMember(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(time.Minute)} // edited after compile
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The origin-range guard lives in the shared enqueue tail, so the authored
// surfaces inherit it from the same code the seeded one is refused by. This is
// the check that the tail is genuinely shared: the substance of the rule is
// covered against the seeded endpoint (SPA-200).
//
// The order matters and is asserted by the 409 tests above, not here: a stale
// graph is still refused as stale, because a range check run against a graph
// the owner has already superseded would be answering about the wrong stations.
func TestUserScenarioIsochrone_422_originOutOfRange(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	graph := freshGraph()
	for i := range graph.Nodes {
		graph.Nodes[i].Lat += 1 // ~111 km north of the origin in isoValidBody
	}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: graph,
	}
	pub := &routing.FakePublisher{}

	rec := isoServeAs(t, store, pub, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.OriginOutOfRangeErrorCode {
		t.Errorf("code = %v, want %q", body["code"], handler.OriginOutOfRangeErrorCode)
	}
	if n := len(pub.Messages()); n != 0 {
		t.Errorf("published %d messages for an out-of-range origin", n)
	}
}

// A fresh graph enqueues rather than computing, and — unlike the public seeded
// isochrone — records the caller as the job's owner, so only they can poll it.
func TestUserScenarioIsochrone_202_enqueuesOwnedByTheCaller(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}
	pub := &routing.FakePublisher{}

	rec := isoServeAs(t, store, pub, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.OwnerID == nil || *job.OwnerID != scnOwner.ID {
		t.Errorf("owner_id = %v, want the caller %q", job.OwnerID, scnOwner.ID)
	}
	if job.CompileJobID != "job-1" {
		t.Errorf("compile_job_id = %q, want the scenario's compile job", job.CompileJobID)
	}
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	if msgs[0].RoutingJobID != job.ID {
		t.Errorf("message names job %q but the caller was handed %q", msgs[0].RoutingJobID, job.ID)
	}
}

// --- single-service isochrone (SPA-140) ---

func userServiceIsochroneMux(store handler.ServiceIsochroneStore, pub routing.Publisher) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/services/{slug}/isochrone", handler.UserServiceIsochrone(store, pub, logger.Discard(), transit.DefaultBoardingWaitPolicy()))
	return mux
}

func svcIsoServeAs(t *testing.T, store handler.ServiceIsochroneStore, pub routing.Publisher, user transit.User, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if user.ID != "" {
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	rec := httptest.NewRecorder()
	userServiceIsochroneMux(store, pub).ServeHTTP(rec, r)
	return rec
}

func TestUserServiceIsochrone_401_unauthenticated(t *testing.T) {
	store := newFakeServiceStore()
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, time.Now())

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, transit.User{}, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserServiceIsochrone_404_unknownSlug(t *testing.T) {
	store := newFakeServiceStore()

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/nope/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 404 rather than 403, so a non-owner cannot probe which service slugs exist.
func TestUserServiceIsochrone_404_nonOwner(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcStranger, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserServiceIsochrone_404_noCompiledGraphYet(t *testing.T) {
	store := newFakeServiceStore()
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, time.Now())

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserServiceIsochrone_400_invalidMode(t *testing.T) {
	store := newFakeServiceStore()
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, time.Now())

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone",
		`{"lat":37.7,"lng":-122.4,"budget_mins":30,"mode":"fly"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserServiceIsochrone_400_budgetNotPositive(t *testing.T) {
	store := newFakeServiceStore()
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, time.Now())

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone",
		`{"lat":37.7,"lng":-122.4,"budget_mins":0,"mode":"walk"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Editing the service after its compile is the only way a single-service graph
// goes stale — there is no membership to change, so this is the whole rule.
func TestUserServiceIsochrone_409_editedService(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(time.Minute)) // edited after compile
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["code"] != handler.StaleGraphErrorCode {
		t.Errorf("code: want %q, got %q", handler.StaleGraphErrorCode, body["code"])
	}
	// The stale response must never leak the outdated graph data.
	if _, ok := body["features"]; ok {
		t.Error("409 response leaks graph features")
	}
}

// A job that compiled a different service cannot satisfy this service's read,
// the membership arm of GraphStale degenerated to a one-vs-one identity check.
func TestUserServiceIsochrone_409_graphCompiledADifferentService(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-2"}, Result: freshGraph(),
	}

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{}, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The single-service twin of the scenario enqueue: 202, owned by the caller,
// naming the service's own compile job.
func TestUserServiceIsochrone_202_enqueuesOwnedByTheCaller(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}
	pub := &routing.FakePublisher{}

	rec := svcIsoServeAs(t, store, pub, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.OwnerID == nil || *job.OwnerID != svcOwner.ID {
		t.Errorf("owner_id = %v, want the caller %q", job.OwnerID, svcOwner.ID)
	}
	if job.CompileJobID != "job-1" {
		t.Errorf("compile_job_id = %q, want the service's compile job", job.CompileJobID)
	}
	if n := len(pub.Messages()); n != 1 {
		t.Errorf("published %d messages, want 1", n)
	}
}

// A service with 0 or 1 stops compiles to a graph with no transit edges. That
// is not an error and must still enqueue: what such a graph yields — a plain
// street-mode isochrone with nothing chained onto it — is the worker's call to
// make, and rejecting it here would mean an as-yet-unstopped service could not
// be previewed at all.
func TestUserServiceIsochrone_202_graphWithoutTransitEdges(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
	edgeless := &transit.TransitGraph{Services: []transit.ServiceGraph{{
		ServiceID: "svc-1", WaitPolicy: string(transit.BoardingWaitNone),
	}}}
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: edgeless,
	}
	pub := &routing.FakePublisher{}

	rec := svcIsoServeAs(t, store, pub, svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	msgs := pub.Messages()
	if len(msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(msgs))
	}
	// The edgeless graph is passed through as-is rather than being second-guessed.
	if msgs[0].Graph == nil || len(msgs[0].Graph.Nodes) != 0 {
		t.Errorf("graph = %+v, want the edgeless compile result unaltered", msgs[0].Graph)
	}
}

// Neither authored isochrone enqueues anything it has just refused. A stale
// target in particular must not leave a routing job behind: the worker would
// compute over a graph the owner has already superseded.
func TestAuthoredIsochrone_refusedRequestsEnqueueNothing(t *testing.T) {
	created := time.Now().Add(-time.Hour)

	t.Run("stale scenario", func(t *testing.T) {
		store := newFakeScenarioStore()
		seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
		store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(time.Minute)}
		store.jobs["trip"] = transit.Job{
			ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
			CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
		}
		pub := &routing.FakePublisher{}

		rec := isoServeAs(t, store, pub, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status: want 409, got %d", rec.Code)
		}
		if n := len(pub.Messages()); n != 0 {
			t.Errorf("published %d messages over a stale graph", n)
		}
		if n := store.count(); n != 0 {
			t.Errorf("recorded %d routing jobs for a stale target", n)
		}
	})

	t.Run("non-owner", func(t *testing.T) {
		store := newFakeServiceStore()
		seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
		store.jobs["line-a"] = transit.Job{
			ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
			CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
		}
		pub := &routing.FakePublisher{}

		rec := svcIsoServeAs(t, store, pub, svcStranger, "/api/services/line-a/isochrone", isoValidBody)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status: want 404, got %d", rec.Code)
		}
		if n := len(pub.Messages()); n != 0 {
			t.Errorf("published %d messages for a non-owner", n)
		}
		if n := store.count(); n != 0 {
			t.Errorf("recorded %d routing jobs for a non-owner", n)
		}
	})
}

// An unconfirmed publish fails the job on the authored surface too, not only on
// the public seeded one.
func TestUserServiceIsochrone_502_unconfirmedPublishFailsTheJob(t *testing.T) {
	store := newFakeServiceStore()
	created := time.Now().Add(-time.Hour)
	seedServiceRow(store, "svc-1", "line-a", svcOwner.ID, created.Add(-time.Minute))
	store.jobs["line-a"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := svcIsoServeAs(t, store, &routing.FakePublisher{Err: routing.ErrNotConfirmed},
		svcOwner, "/api/services/line-a/isochrone", isoValidBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d: %s", rec.Code, rec.Body.String())
	}
	job, ok := store.only()
	if !ok {
		t.Fatalf("want exactly one routing job recorded, got %d", store.count())
	}
	if job.Status != transit.JobStatusFailed {
		t.Errorf("routing job status = %q, want failed", job.Status)
	}
}
