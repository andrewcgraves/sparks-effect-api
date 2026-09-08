package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func ingestCompileRoute(t *testing.T, repo interface {
	CreateRoute(ctx context.Context, r transit.Route) error
}, slug string) {
	t.Helper()
	if err := repo.CreateRoute(context.Background(), transit.Route{
		ID: mustUUID(t), Slug: slug, Name: "Route", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
}

func TestIntegration_UserServiceCompileLifecycle(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	token := provisionMember(t, h, adminToken, "svc-compiler@example.com", "member-password")

	ingestCompileRoute(t, repo, "uc-svc-route")
	svcID := createUserServiceOverAPI(t, h, token, "uc-svc-route", "Solo Line")

	// The service is addressed by slug; the create helper does not return it, so
	// read it back to learn the slug the server minted.
	svc := getUserServiceByID(t, h, token, svcID)

	rec := request(t, h, http.MethodPost, "/api/services/"+svc.Slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Kind != transit.JobKindCompileUserService {
		t.Errorf("kind = %q, want %q", created.Kind, transit.JobKindCompileUserService)
	}

	final := pollJob(t, h, token, created.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("final status = %q, want succeeded (error: %s)", final.Status, final.Error)
	}
	if final.Result == nil || len(final.Result.Services) != 1 {
		t.Fatalf("result = %+v, want one compiled service", final.Result)
	}
	if len(final.Result.Nodes) != 2 {
		t.Errorf("result nodes = %d, want 2 (one per stop, SPA-111)", len(final.Result.Nodes))
	}
	if len(final.CompiledServiceIDs) != 1 || final.CompiledServiceIDs[0] != svcID {
		t.Errorf("compiled_service_ids = %v, want [%s]", final.CompiledServiceIDs, svcID)
	}
}

func TestIntegration_UserScenarioCompileAndGraphBySlug(t *testing.T) {
	h, repo := integrationServer(t)
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "scn-owner@example.com", "owner-password")
	stranger := provisionMember(t, h, adminToken, "scn-stranger@example.com", "stranger-password")

	ingestCompileRoute(t, repo, "uc-scn-route")
	svc1 := createUserServiceOverAPI(t, h, owner, "uc-scn-route", "Express")
	svc2 := createUserServiceOverAPI(t, h, owner, "uc-scn-route", "Local")

	rec := request(t, h, http.MethodPost, "/api/user-scenarios", owner,
		`{"name":"Trip","service_ids":["`+svc1+`","`+svc2+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create scenario: status %d, body %s", rec.Code, rec.Body.String())
	}
	var scenario transit.UserScenario
	if err := json.NewDecoder(rec.Body).Decode(&scenario); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A stranger cannot trigger a compile on someone else's scenario.
	if r := request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/compile", stranger); r.Code != http.StatusNotFound {
		t.Errorf("stranger compile: status %d, want 404", r.Code)
	}

	rec = request(t, h, http.MethodPost, "/api/user-scenarios/"+scenario.Slug+"/compile", owner)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	final := pollJob(t, h, owner, created.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("final status = %q, want succeeded (error: %s)", final.Status, final.Error)
	}
	if len(final.Result.Services) != 2 {
		t.Fatalf("result services = %d, want 2", len(final.Result.Services))
	}
	// The two members share both stop coordinates, so their stops merge across
	// services: two realised interchange clusters.
	if len(final.Result.Merge.Clusters) != 2 {
		t.Errorf("merge clusters = %d, want 2 co-located clusters; merge = %+v", len(final.Result.Merge.Clusters), final.Result.Merge)
	}
	if len(final.CompiledServiceIDs) != 2 {
		t.Errorf("compiled_service_ids = %v, want both members", final.CompiledServiceIDs)
	}

	// Retrievable by the scenario's slug — the owner sees the graph.
	graphRec := request(t, h, http.MethodGet, "/api/user-scenarios/"+scenario.Slug+"/graph", owner)
	if graphRec.Code != http.StatusOK {
		t.Fatalf("GET graph: status %d, want 200; body %s", graphRec.Code, graphRec.Body.String())
	}
	var graph transit.TransitGraph
	if err := json.NewDecoder(graphRec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(graph.Services) != 2 {
		t.Errorf("graph services = %d, want 2", len(graph.Services))
	}

	// A stranger cannot read it — owner-scoped, unlike the seeded public graph.
	if r := request(t, h, http.MethodGet, "/api/user-scenarios/"+scenario.Slug+"/graph", stranger); r.Code != http.StatusNotFound {
		t.Errorf("stranger graph read: status %d, want 404", r.Code)
	}
}

func getUserServiceByID(t *testing.T, h http.Handler, token, id string) transit.UserService {
	t.Helper()
	rec := request(t, h, http.MethodGet, "/api/services", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list services: status %d", rec.Code)
	}
	var services []transit.UserService
	if err := json.NewDecoder(rec.Body).Decode(&services); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, svc := range services {
		if svc.ID == id {
			return svc
		}
	}
	t.Fatalf("service %s not found in owner's list", id)
	return transit.UserService{}
}
