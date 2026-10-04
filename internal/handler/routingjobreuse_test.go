package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func previousJob() *transit.RoutingJob {
	return &transit.RoutingJob{
		ID: "rj-earlier", Status: transit.JobStatusSucceeded, CompileJobID: "compile-job-1",
		Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk,
		Result:    json.RawMessage(`{"type":"FeatureCollection","features":[]}`),
		CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}
}

func TestIsochrone_200_servesARepeatFromThePreviousJob(t *testing.T) {
	store := compiledStore()
	store.reusable = previousJob()
	pub := &routing.FakePublisher{}

	rec := postIsochrone(store, pub, validIsochroneBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	job := decodeRoutingJob(t, rec)
	if job.ID != "rj-earlier" || job.Status != transit.JobStatusSucceeded {
		t.Errorf("answered %s/%s, want the earlier succeeded job", job.ID, job.Status)
	}
	if string(job.Result) != `{"type":"FeatureCollection","features":[]}` {
		t.Errorf("result = %s, want the earlier job's", job.Result)
	}
	if n := len(pub.Messages()); n != 0 {
		t.Errorf("published %d messages for a reused result, want none", n)
	}
	// No row means nothing for CountInFlightRoutingJobs to count.
	if n := store.count(); n != 0 {
		t.Errorf("recorded %d routing jobs for a reused result, want none", n)
	}
}

func TestIsochrone_asksForReuseWithTheWholeRequest(t *testing.T) {
	store := compiledStore()

	rec := postIsochrone(store, &routing.FakePublisher{}, validIsochroneBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}

	if len(store.reuseAsked) != 1 {
		t.Fatalf("asked for reuse %d times, want once", len(store.reuseAsked))
	}
	asked := store.reuseAsked[0]
	want := transit.RoutingJob{
		CompileJobID: "compile-job-1", Lat: 37.7, Lng: -122.4, BudgetMins: 30, Mode: transit.TravelModeWalk,
	}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %+v, want %+v", asked, want)
	}
}

func TestIsochrone_202_aFailedReuseLookupStillEnqueues(t *testing.T) {
	store := compiledStore()
	store.reuseErr = errors.New("db hiccup")
	pub := &routing.FakePublisher{}

	rec := postIsochrone(store, pub, validIsochroneBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := len(pub.Messages()); n != 1 {
		t.Errorf("published %d messages, want 1: reuse is an optimisation, not a dependency", n)
	}
}

func TestIsochrone_422_anOutOfRangeOriginIsRefusedBeforeReuse(t *testing.T) {
	store := compiledStore()
	store.reusable = previousJob()

	rec := postIsochrone(store, &routing.FakePublisher{},
		`{"lat":0,"lng":0,"budget_mins":30,"mode":"walk","scenario_slug":"ca-hsr"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserScenarioIsochrone_asksForReuseAsTheCaller(t *testing.T) {
	store := newFakeScenarioStore()
	created := time.Now().Add(-time.Hour)
	seedScenarioRow(store, "scn-1", "trip", scnOwner.ID, []string{"svc-1"})
	store.members["svc-1"] = transit.UserService{ID: "svc-1", UpdatedAt: created.Add(-time.Minute)}
	store.jobs["trip"] = transit.Job{
		ID: "job-1", Status: transit.JobStatusSucceeded, CreatedAt: created,
		CompiledServiceIDs: []string{"svc-1"}, Result: freshGraph(),
	}

	rec := isoServeAs(t, store, &routing.FakePublisher{}, scnOwner, "/api/user-scenarios/trip/isochrone", isoValidBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d: %s", rec.Code, rec.Body.String())
	}

	// The owner is part of the reuse key: the store can only hand back a job
	// this caller could already poll.
	if len(store.reuseAsked) != 1 {
		t.Fatalf("asked for reuse %d times, want once", len(store.reuseAsked))
	}
	if owner := store.reuseAsked[0].OwnerID; owner == nil || *owner != scnOwner.ID {
		t.Errorf("asked for reuse as owner %v, want the caller %q", owner, scnOwner.ID)
	}
}
