package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func publishedServiceOverAPI(t *testing.T, h http.Handler, token, routeSlug, name string) (slug string, pinned transit.Job) {
	t.Helper()
	svc := getUserServiceByID(t, h, token, createUserServiceOverAPI(t, h, token, routeSlug, name))
	pinned = compileUserServiceAndWait(t, h, token, svc.Slug)
	if rec := request(t, h, http.MethodPut, "/api/services/"+svc.Slug+"/publication", token); rec.Code != http.StatusOK {
		t.Fatalf("publish: status %d, body %s", rec.Code, rec.Body.String())
	}
	return svc.Slug, pinned
}

func compileUserServiceAndWait(t *testing.T, h http.Handler, token, slug string) transit.Job {
	t.Helper()
	rec := request(t, h, http.MethodPost, "/api/services/"+slug+"/compile", token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST compile: status %d, body %s", rec.Code, rec.Body.String())
	}
	var created transit.Job
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	final := pollJob(t, h, token, created.ID)
	if final.Status != transit.JobStatusSucceeded {
		t.Fatalf("compile: final status = %q, want succeeded (error: %s)", final.Status, final.Error)
	}
	return final
}

func decodeRoutingJobBody(t *testing.T, body []byte) transit.RoutingJob {
	t.Helper()
	var job transit.RoutingJob
	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatalf("decode routing job: %v; body %s", err, body)
	}
	return job
}

func TestIntegration_AnonymousIsochroneOverAPublication(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	admin := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, admin, "pub-iso-owner@example.com", "owner-password")
	stranger := provisionMember(t, h, admin, "pub-iso-stranger@example.com", "stranger-password")

	ingestCompileRoute(t, repo, "pub-iso-route")
	svc := getUserServiceByID(t, h, owner, createUserServiceOverAPI(t, h, owner, "pub-iso-route", "Public Line"))
	draftPath := "/api/services/" + svc.Slug
	plotPath := draftPath + "/publication/isochrone"

	rec := request(t, h, http.MethodPost, "/api/services/no-such-service/publication/isochrone", "", isoRequestBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug: status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	unknown := rec.Body.String()

	readers := []struct{ name, token string }{
		{"anonymous", ""}, {"stranger", stranger}, {"owner", owner}, {"admin", admin},
	}
	assertHidden := func(stage string) {
		t.Helper()
		for _, reader := range readers {
			rec := request(t, h, http.MethodPost, plotPath, reader.token, isoRequestBody)
			if rec.Code != http.StatusNotFound || rec.Body.String() != unknown {
				t.Fatalf("%s, %s: status %d body %s; want 404 %s", stage, reader.name, rec.Code, rec.Body.String(), unknown)
			}
		}
	}

	// A fresh compile the owner could plot over is still not a publication.
	pinned := compileUserServiceAndWait(t, h, owner, svc.Slug)
	assertHidden("never published")
	if n, err := repo.CountInFlightRoutingJobs(ctx, handler.RoutingJobStaleAfter); err != nil || n != 0 {
		t.Fatalf("routing jobs after refusals: %d (err %v), want 0", n, err)
	}

	if rec := request(t, h, http.MethodPut, draftPath+"/publication", owner); rec.Code != http.StatusOK {
		t.Fatalf("publish: status %d, body %s", rec.Code, rec.Body.String())
	}

	// The owner edits the draft without recompiling: their own isochrone is
	// now stale, and the publication's must not notice.
	edited := `{
		"route_slug": "pub-iso-route", "name": "Edited Line",
		"vehicle": {"max_speed_kmh": 200, "acceleration_ms2": 1, "deceleration_ms2": 1, "dwell_s": 30},
		"stops": [{"name": "A", "lat": 37, "lng": -121.8}, {"name": "B", "lat": 37, "lng": -121.4}]
	}`
	if rec := request(t, h, http.MethodPut, draftPath, owner, edited); rec.Code != http.StatusOK {
		t.Fatalf("edit draft: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec = request(t, h, http.MethodPost, draftPath+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("owner's draft isochrone after an edit: status %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	var conflict map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil || conflict["code"] != handler.StaleGraphErrorCode {
		t.Fatalf("owner's draft isochrone: body %s, want code %q", rec.Body.String(), handler.StaleGraphErrorCode)
	}

	compilesBefore, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}

	for _, reader := range readers {
		rec := request(t, h, http.MethodPost, plotPath, reader.token, isoRequestBody)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s plot: status %d, want 202; body %s", reader.name, rec.Code, rec.Body.String())
		}
		job := decodeRoutingJobBody(t, rec.Body.Bytes())
		if job.CompileJobID != pinned.ID {
			t.Errorf("%s plot: compile_job_id = %s, want the pinned %s", reader.name, job.CompileJobID, pinned.ID)
		}

		// NULL in the row, not just absent from the response.
		stored, found, err := repo.GetRoutingJobByID(ctx, job.ID)
		if err != nil || !found {
			t.Fatalf("%s plot: GetRoutingJobByID found=%v err=%v", reader.name, found, err)
		}
		if stored.OwnerID != nil {
			t.Errorf("%s plot: stored owner_id = %q, want NULL", reader.name, *stored.OwnerID)
		}

		// Anyone holding the id polls it back, a session or not.
		for _, poller := range readers {
			if rec := request(t, h, http.MethodGet, "/api/routing-jobs/"+job.ID, poller.token); rec.Code != http.StatusOK {
				t.Errorf("%s polling %s's job: status %d, want 200; body %s", poller.name, reader.name, rec.Code, rec.Body.String())
			}
		}
	}

	// Plotting a publication compiled nothing, however stale the draft behind it.
	compilesAfter, err := repo.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(compilesAfter) != len(compilesBefore) {
		t.Fatalf("compile jobs %d -> %d: a publication isochrone triggered a compile", len(compilesBefore), len(compilesAfter))
	}

	// Once the owner recompiles, their draft isochrone plots the new graph and
	// owns its job, exactly as before this route existed. The public one stays
	// on the pin.
	draftJob := compileUserServiceAndWait(t, h, owner, svc.Slug)
	rec = request(t, h, http.MethodPost, draftPath+"/isochrone", owner, isoRequestBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("owner's draft isochrone after recompile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	owned := decodeRoutingJobBody(t, rec.Body.Bytes())
	if owned.CompileJobID != draftJob.ID || owned.OwnerID == nil {
		t.Fatalf("owner's draft plot: compile %s owner %v; want %s, owned", owned.CompileJobID, owned.OwnerID, draftJob.ID)
	}
	if rec := request(t, h, http.MethodGet, "/api/routing-jobs/"+owned.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anonymous polling the owner's draft job: status %d, want 404", rec.Code)
	}
	rec = request(t, h, http.MethodPost, plotPath, "", isoRequestBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("anonymous plot after recompile: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if job := decodeRoutingJobBody(t, rec.Body.Bytes()); job.CompileJobID != pinned.ID {
		t.Fatalf("anonymous plot after recompile: compile %s, want the pinned %s", job.CompileJobID, pinned.ID)
	}

	if rec := request(t, h, http.MethodDelete, draftPath+"/publication", owner); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status %d, body %s", rec.Code, rec.Body.String())
	}
	assertHidden("unpublished")
}

func TestIntegration_PublicationIsochroneSpendsTheSharedBacklog(t *testing.T) {
	const limit = 1

	h, repo := integrationServerCapped(t, limit)
	ctx := context.Background()
	admin := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, admin, "pub-iso-cap@example.com", "owner-password")
	ingestCompileRoute(t, repo, "pub-iso-cap-route")
	slug, _ := publishedServiceOverAPI(t, h, owner, "pub-iso-cap-route", "Capped Line")
	plotPath := "/api/services/" + slug + "/publication/isochrone"

	if rec := request(t, h, http.MethodPost, plotPath, "", isoRequestBody); rec.Code != http.StatusAccepted {
		t.Fatalf("first plot: status %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	// The backlog is per deployment. An anonymous plot that fills it blocks the
	// owner's draft isochrone too, and is itself refused once full.
	for _, call := range []struct{ name, path, token string }{
		{"anonymous publication plot", plotPath, ""},
		{"owner's draft plot", "/api/services/" + slug + "/isochrone", owner},
	} {
		rec := request(t, h, http.MethodPost, call.path, call.token, isoRequestBody)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s over the cap: status %d, want 429; body %s", call.name, rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != handler.BacklogFullErrorCode {
			t.Fatalf("%s over the cap: body %s, want code %q", call.name, rec.Body.String(), handler.BacklogFullErrorCode)
		}
	}

	if n, err := repo.CountInFlightRoutingJobs(ctx, handler.RoutingJobStaleAfter); err != nil || n != limit {
		t.Fatalf("in flight = %d (err %v), want %d: a refused plot left a row behind", n, err, limit)
	}
}
