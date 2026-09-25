package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pubJobID  = "00000000-0000-400a-8003-000000000001"
	pubJobID2 = "00000000-0000-400a-8003-000000000002"
	pubJobID3 = "00000000-0000-400a-8003-000000000003"
)

func TestPublishFreezesProseAndEdgeRouteGeometry(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	// The pinned graph names the other alignment, not the draft's route_id.
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, []transit.Edge{{
		FromSlug: "a", ToSlug: "b", Seconds: 30, RouteID: usRouteID2,
	}})

	live, found, err := repo.GetRouteBySlug(ctx, "us-route-1")
	if err != nil || !found {
		t.Fatalf("GetRouteBySlug: found=%v err=%v", found, err)
	}
	frozenGeom := append([][]float64(nil), live.Geometry.Coordinates...)

	pub, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("PublishUserService: %v", err)
	}
	if pub.CompileJobID != pubJobID || pub.Name != svc.Name || pub.Subtext != svc.Subtext || pub.Description != svc.Description {
		t.Fatalf("snapshot prose/pin = %+v", pub)
	}
	if len(pub.Routes) != 1 || pub.Routes[0].ID != usRouteID2 {
		t.Fatalf("routes = %+v, want the edge route %s", pub.Routes, usRouteID2)
	}
	if !coordsEqual(pub.Routes[0].Geometry.Coordinates, frozenGeom) {
		t.Fatalf("frozen geometry = %v, want %v", pub.Routes[0].Geometry.Coordinates, frozenGeom)
	}
	if pub.PublishedAt.IsZero() {
		t.Fatal("published_at is zero")
	}

	stored, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("reload draft: found=%v err=%v", found, err)
	}
	stored.Name = "Renamed after publish"
	stored.Subtext = "A later subtext"
	stored.Description = "A later description"
	stored.RouteID = usRouteID2
	if err := repo.UpdateUserService(ctx, stored); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	live.Geometry.Coordinates = [][]float64{{-10, 1}, {-9, 1}}
	if err := repo.UpdateRoute(ctx, live); err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}

	got, found, err := repo.GetServicePublication(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("GetServicePublication: found=%v err=%v", found, err)
	}
	if got.Name != svc.Name || got.Subtext != svc.Subtext || got.Description != svc.Description {
		t.Fatalf("publication prose changed with the draft: %+v", got)
	}
	if len(got.Routes) != 1 || got.Routes[0].ID != usRouteID2 {
		t.Fatalf("publication routes = %+v", got.Routes)
	}
	if !coordsEqual(got.Routes[0].Geometry.Coordinates, frozenGeom) {
		t.Fatalf("publication geometry = %v, want the frozen copy %v", got.Routes[0].Geometry.Coordinates, frozenGeom)
	}
	mutated, _, err := repo.GetRouteBySlug(ctx, "us-route-1")
	if err != nil {
		t.Fatalf("reload route: %v", err)
	}
	if coordsEqual(mutated.Geometry.Coordinates, frozenGeom) {
		t.Fatal("live route geometry did not change; the freeze assertion is vacuous")
	}
}

func TestPublishStoresEmptyProseAndEmptyRouteArray(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	svc.Subtext = ""
	svc.Description = ""
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, []transit.Edge{})

	pub, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("PublishUserService: %v", err)
	}
	if pub.Subtext != "" || pub.Description != "" {
		t.Fatalf("prose = %q / %q, want empty strings", pub.Subtext, pub.Description)
	}
	if pub.Routes == nil || len(pub.Routes) != 0 {
		t.Fatalf("routes = %#v, want an empty slice", pub.Routes)
	}
	raw := scalarText(t, url, `SELECT routes::text FROM service_publications WHERE user_service_id = '`+svc.ID+`'`)
	if raw != "[]" {
		t.Fatalf("routes jsonb = %s, want []", raw)
	}
}

func TestPublishRejectsMissingCompileAndKeepsLatestSuccess(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	if _, err := publishUserService(ctx, repo, svc.ID); !errors.Is(err, handler.ErrStaleGraph) {
		t.Fatalf("no compile: %v, want ErrStaleGraph", err)
	}
	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("publication after a refused publish: found=%v err=%v", found, err)
	}

	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)
	owner := usOwnerID
	svcID := svc.ID
	if err := repo.CreateJob(ctx, transit.Job{
		ID: pubJobID2, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
		UserServiceID: &svcID, OwnerID: &owner,
	}); err != nil {
		t.Fatalf("CreateJob failed compile: %v", err)
	}
	if err := repo.UpdateJobStatus(ctx, pubJobID2, transit.JobStatusFailed, "compile failed"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	pub, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("publish with a later failure: %v", err)
	}
	if pub.CompileJobID != pubJobID {
		t.Fatalf("pin = %s, want the latest success %s", pub.CompileJobID, pubJobID)
	}
}

func TestUnpublishAndRepublish(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)

	first, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := repo.UnpublishUserService(ctx, svc.ID); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("publication after unpublish: found=%v err=%v", found, err)
	}
	if err := repo.UnpublishUserService(ctx, svc.ID); err != nil {
		t.Fatalf("second unpublish: %v", err)
	}

	stored, _, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stored.Name = "Republished name"
	stored.Subtext = "Republished subtext"
	stored.Description = "Republished description"
	if err := repo.UpdateUserService(ctx, stored); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}
	if _, err := publishUserService(ctx, repo, svc.ID); !errors.Is(err, handler.ErrStaleGraph) {
		t.Fatalf("publish after an edit with no new compile: %v, want ErrStaleGraph", err)
	}
	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("stale republish wrote a row: found=%v err=%v", found, err)
	}

	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID2, nil)
	second, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if second.CompileJobID != pubJobID2 || second.Name != "Republished name" ||
		second.Subtext != "Republished subtext" || second.Description != "Republished description" {
		t.Fatalf("fresh snapshot = %+v", second)
	}
	if !second.PublishedAt.After(first.PublishedAt) {
		t.Fatalf("published_at = %s, want after %s", second.PublishedAt, first.PublishedAt)
	}
}

func TestDeleteUserServiceRemovesPublicationAndJobs(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)
	if _, err := publishUserService(ctx, repo, svc.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// The statement DeleteUserService issues. NO ACTION on the publication's
	// job pin is checked at the end of the statement, so this one delete can
	// cascade the job and the publication together.
	exec(t, url, `DELETE FROM user_services WHERE id = '`+svc.ID+`'`)

	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("publication after deleting the service: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetJobByID(ctx, pubJobID); err != nil || found {
		t.Fatalf("compile job after deleting the service: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetUserServiceByID(ctx, svc.ID); err != nil || found {
		t.Fatalf("service row survived the delete: found=%v err=%v", found, err)
	}
}

func TestDeleteUserServiceMethodRemovesPublicationAndJobs(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)
	if _, err := publishUserService(ctx, repo, svc.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := repo.DeleteUserService(ctx, svc.ID); err != nil {
		t.Fatalf("DeleteUserService: %v", err)
	}
	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("publication: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetJobByID(ctx, pubJobID); err != nil || found {
		t.Fatalf("job: found=%v err=%v", found, err)
	}
}

func TestDeletePinnedJobIsRejected(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)
	if _, err := publishUserService(ctx, repo, svc.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}

	conn := connectDB(t, url)
	_, err := conn.Exec(ctx, `DELETE FROM jobs WHERE id = $1`, pubJobID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("DELETE pinned job: %v, want foreign_key_violation", err)
	}
	if pgErr.ConstraintName != "service_publications_compile_job_id_fkey" {
		t.Fatalf("constraint = %q, want service_publications_compile_job_id_fkey", pgErr.ConstraintName)
	}

	if _, found, err := repo.GetJobByID(ctx, pubJobID); err != nil || !found {
		t.Fatalf("pinned job after the rejected delete: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || !found {
		t.Fatalf("publication after the rejected delete: found=%v err=%v", found, err)
	}
}

// The editor commits a draft change before publish decides. Publish must
// observe that edit, refuse with a stale graph, and write no row.
func TestPublishLockCoversStalenessAndWrite(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)

	conn := connectDB(t, url)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM user_services WHERE id = $1 FOR UPDATE`, svc.ID).Scan(&locked); err != nil {
		t.Fatalf("lock draft: %v", err)
	}

	bctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, pubErr := publishUserService(bctx, repo, svc.ID)
		errCh <- pubErr
	}()

	if !waitForUngrantedUserServiceLock(t, url, 5*time.Second) {
		t.Fatal("publish did not block on the draft row lock")
	}

	if _, err := tx.Exec(ctx,
		`UPDATE user_services SET name = $2, updated_at = now() WHERE id = $1`,
		svc.ID, "Edited during publish"); err != nil {
		t.Fatalf("update while holding the lock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, handler.ErrStaleGraph) {
			t.Fatalf("publish after a concurrent edit: %v, want stale graph", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publish did not return after the draft lock was released")
	}

	if _, found, err := repo.GetServicePublication(ctx, svc.ID); err != nil || found {
		t.Fatalf("publication after the raced edit: found=%v err=%v", found, err)
	}
	got, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("reload draft: found=%v err=%v", found, err)
	}
	if got.Name != "Edited during publish" {
		t.Fatalf("draft name = %q, want the edit that raced the publish", got.Name)
	}
}

// Publish locks the draft, decides, and inserts in one transaction. The editor
// blocks on that row lock from the moment publish has decided until publish
// commits, so the stored snapshot is the pre-edit prose and the pinned job.
func TestPublishLockSpansTheWrite(t *testing.T) {
	repo, ctx, url := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)

	preName, preSubtext, preDescription := svc.Name, svc.Subtext, svc.Description
	const (
		editedName        = "Edited during publish"
		editedSubtext     = "Edited subtext"
		editedDescription = "Edited description"
	)

	ready := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		postgres.SetPublishAfterDecide(nil)
		release()
	})
	postgres.SetPublishAfterDecide(func() {
		close(ready)
		<-releaseCh
	})

	pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type pubResult struct {
		pub transit.ServicePublication
		err error
	}
	resCh := make(chan pubResult, 1)
	go func() {
		pub, err := publishUserService(pctx, repo, svc.ID)
		resCh <- pubResult{pub, err}
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("publish did not reach the post-decide seam while holding the draft lock")
	}

	editor := connectDB(t, url)
	type lockedEdit struct {
		err            error
		sawPub         bool
		pubName        string
		pubSubtext     string
		pubDescription string
		compileJob     string
	}
	editCh := make(chan lockedEdit, 1)
	go func() {
		tx, err := editor.Begin(context.Background())
		if err != nil {
			editCh <- lockedEdit{err: err}
			return
		}
		defer tx.Rollback(context.Background()) //nolint:errcheck // rollback after commit is a no-op

		if _, err := tx.Exec(context.Background(),
			`UPDATE user_services SET name = $2, subtext = $3, description = $4, updated_at = now() WHERE id = $1`,
			svc.ID, editedName, editedSubtext, editedDescription); err != nil {
			editCh <- lockedEdit{err: err}
			return
		}
		var got lockedEdit
		err = tx.QueryRow(context.Background(),
			`SELECT name, subtext, description, compile_job_id::text
			   FROM service_publications WHERE user_service_id = $1`, svc.ID).
			Scan(&got.pubName, &got.pubSubtext, &got.pubDescription, &got.compileJob)
		if errors.Is(err, pgx.ErrNoRows) {
			editCh <- lockedEdit{}
			return
		}
		if err != nil {
			editCh <- lockedEdit{err: err}
			return
		}
		got.sawPub = true
		if err := tx.Commit(context.Background()); err != nil {
			got.err = err
		}
		editCh <- got
	}()

	if !waitForLockedUserServiceUpdate(t, url, 5*time.Second) {
		t.Fatal("editor UPDATE did not block on the draft row lock")
	}
	select {
	case got := <-editCh:
		t.Fatalf("editor UPDATE finished while publish still held the draft lock: %+v", got)
	default:
	}

	release()

	var published pubResult
	select {
	case published = <-resCh:
	case <-time.After(5 * time.Second):
		t.Fatal("publish did not commit after the seam returned")
	}
	if published.err != nil {
		t.Fatalf("publish: %v, want the pre-edit snapshot and no stale_graph error", published.err)
	}
	if published.pub.Name != preName || published.pub.Subtext != preSubtext ||
		published.pub.Description != preDescription || published.pub.CompileJobID != pubJobID {
		t.Fatalf("committed snapshot = %+v, want name %q subtext %q description %q job %s",
			published.pub, preName, preSubtext, preDescription, pubJobID)
	}

	var edited lockedEdit
	select {
	case edited = <-editCh:
	case <-time.After(5 * time.Second):
		t.Fatal("editor UPDATE did not proceed after publish committed")
	}
	if edited.err != nil {
		t.Fatalf("editor UPDATE: %v", edited.err)
	}
	if !edited.sawPub {
		t.Fatal("publication was not committed before the editor acquired the draft lock")
	}
	if edited.pubName != preName || edited.pubSubtext != preSubtext || edited.pubDescription != preDescription ||
		edited.compileJob != pubJobID {
		t.Fatalf("publication visible as the editor got the lock = name %q subtext %q description %q job %s, want the pre-edit snapshot",
			edited.pubName, edited.pubSubtext, edited.pubDescription, edited.compileJob)
	}

	stored, found, err := repo.GetServicePublication(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("GetServicePublication: found=%v err=%v", found, err)
	}
	if stored.Name != preName || stored.Subtext != preSubtext || stored.Description != preDescription ||
		stored.CompileJobID != pubJobID {
		t.Fatalf("stored publication = %+v, want the pre-edit prose and job %s", stored, pubJobID)
	}
	draft, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("reload draft: found=%v err=%v", found, err)
	}
	if draft.Name != editedName || draft.Subtext != editedSubtext || draft.Description != editedDescription {
		t.Fatalf("draft after the editor unblocked = %q / %q / %q", draft.Name, draft.Subtext, draft.Description)
	}
}

func TestStaleRepublishLeavesSnapshotUnchanged(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, []transit.Edge{{
		FromSlug: "a", ToSlug: "b", Seconds: 30, RouteID: usRouteID2,
	}})
	first, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	stored, found, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("reload draft: found=%v err=%v", found, err)
	}
	stored.Name = "Edited name"
	stored.Subtext = "Edited subtext"
	stored.Description = "Edited description"
	if err := repo.UpdateUserService(ctx, stored); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}

	if _, err := publishUserService(ctx, repo, svc.ID); !errors.Is(err, handler.ErrStaleGraph) {
		t.Fatalf("stale republish: %v, want ErrStaleGraph", err)
	}
	got, found, err := repo.GetServicePublication(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("publication after stale republish: found=%v err=%v", found, err)
	}
	samePublication(t, got, first)
}

func TestPublishMissingEdgeRouteLeavesSnapshot(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, []transit.Edge{{
		FromSlug: "a", ToSlug: "b", Seconds: 30, RouteID: usRouteID2,
	}})
	first, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID2, []transit.Edge{{
		FromSlug: "a", ToSlug: "b", Seconds: 30, RouteID: "00000000-0000-4002-8003-0000000000ff",
	}})
	if _, err := publishUserService(ctx, repo, svc.ID); !errors.Is(err, handler.ErrStaleGraph) {
		t.Fatalf("publish with a missing edge route: %v, want ErrStaleGraph", err)
	}
	got, found, err := repo.GetServicePublication(ctx, svc.ID)
	if err != nil || !found {
		t.Fatalf("publication after the missing route: found=%v err=%v", found, err)
	}
	samePublication(t, got, first)
}

func TestUnpublishMissingServiceIsIdempotent(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	if err := repo.UnpublishUserService(ctx, usServiceID); err != nil {
		t.Fatalf("unpublish with no service row: %v, want nil", err)
	}
}

func TestGetServicePublicationBySlug(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, nil)

	// Compiled is not published.
	if _, found, err := repo.GetServicePublicationBySlug(ctx, svc.Slug); err != nil || found {
		t.Fatalf("unpublished slug: found=%v err=%v, want not found", found, err)
	}
	if _, found, err := repo.GetServicePublicationBySlug(ctx, "no-such-service"); err != nil || found {
		t.Fatalf("unknown slug: found=%v err=%v, want not found", found, err)
	}

	want, err := publishUserService(ctx, repo, svc.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	// An edit after publishing must not reach the read: it selects nothing
	// from the draft row it joins through.
	stored, _, err := repo.GetUserServiceByID(ctx, svc.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	stored.Name = "Edited after publish"
	stored.Subtext = "Edited subtext"
	stored.RouteID = usRouteID2
	if err := repo.UpdateUserService(ctx, stored); err != nil {
		t.Fatalf("UpdateUserService: %v", err)
	}

	got, found, err := repo.GetServicePublicationBySlug(ctx, svc.Slug)
	if err != nil || !found {
		t.Fatalf("published slug: found=%v err=%v", found, err)
	}
	samePublication(t, got, want)

	if err := repo.UnpublishUserService(ctx, svc.ID); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, found, err := repo.GetServicePublicationBySlug(ctx, svc.Slug); err != nil || found {
		t.Fatalf("unpublished again: found=%v err=%v, want not found", found, err)
	}
}

func TestGetSucceededCompileJob(t *testing.T) {
	repo, ctx, _ := userServiceFixture(t)
	svc := sampleUserService()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}
	succeedUserServiceCompile(t, repo, ctx, svc.ID, pubJobID, []transit.Edge{{
		FromSlug: "a", ToSlug: "b", Seconds: 45, RouteID: usRouteID,
	}})

	got, found, err := repo.GetSucceededCompileJob(ctx, pubJobID)
	if err != nil || !found {
		t.Fatalf("GetSucceededCompileJob: found=%v err=%v", found, err)
	}
	if got.Result == nil || len(got.Result.Services) != 1 || len(got.Result.Services[0].Edges) != 1 {
		t.Fatalf("result = %+v, want the stored graph", got.Result)
	}
	edge := got.Result.Services[0].Edges[0]
	if edge.RouteID != usRouteID || edge.Seconds != 45 || edge.FromSlug != "a" {
		t.Fatalf("edge = %+v", edge)
	}
	if got.Kind != transit.JobKindCompileUserService || got.Status != transit.JobStatusSucceeded {
		t.Fatalf("job = %s/%s", got.Kind, got.Status)
	}

	if _, found, err := repo.GetSucceededCompileJob(ctx, "00000000-0000-400a-8003-0000000000ff"); err != nil || found {
		t.Fatalf("unknown id: found=%v err=%v", found, err)
	}

	owner := usOwnerID
	svcID := svc.ID
	if err := repo.CreateJob(ctx, transit.Job{
		ID: pubJobID3, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
		UserServiceID: &svcID, OwnerID: &owner,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := repo.UpdateJobStatus(ctx, pubJobID3, transit.JobStatusFailed, "nope"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if _, found, err := repo.GetSucceededCompileJob(ctx, pubJobID3); err != nil || found {
		t.Fatalf("failed job: found=%v err=%v, want not found", found, err)
	}
	if _, found, err := repo.GetJobByID(ctx, pubJobID3); err != nil || !found {
		t.Fatalf("GetJobByID failed job: found=%v err=%v", found, err)
	}
}

func publishUserService(ctx context.Context, repo *postgres.Repo, serviceID string) (transit.ServicePublication, error) {
	return repo.PublishUserService(ctx, serviceID, func(ctx context.Context, locked transit.UserService, read handler.PublicationRead) (transit.ServicePublication, error) {
		return handler.PublicationFromLockedDraft(ctx, locked, read, transit.DefaultBoardingWaitPolicy())
	})
}

func succeedUserServiceCompile(t *testing.T, repo *postgres.Repo, ctx context.Context, serviceID, jobID string, edges []transit.Edge) {
	t.Helper()
	if edges == nil {
		edges = []transit.Edge{{FromSlug: "a", ToSlug: "b", Seconds: 30, RouteID: usRouteID}}
	}
	owner := usOwnerID
	if err := repo.CreateJob(ctx, transit.Job{
		ID: jobID, Kind: transit.JobKindCompileUserService, Status: transit.JobStatusQueued,
		UserServiceID: &serviceID, OwnerID: &owner,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := repo.CompleteJob(ctx, jobID, transit.TransitGraph{
		Services: []transit.ServiceGraph{{
			ServiceID: serviceID, WaitPolicy: string(transit.BoardingWaitNone), Edges: edges,
		}},
	}, []string{serviceID}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
}

func connectDB(t *testing.T, url string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func waitForLockedUserServiceUpdate(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	conn := connectDB(t, url)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// The editor's UPDATE waits on the publish transaction's row lock.
		// That wait is on the session, as wait_event_type Lock.
		var n int
		err := conn.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND pid <> pg_backend_pid()
			    AND wait_event_type = 'Lock'
			    AND query LIKE 'UPDATE user_services SET name%'`).Scan(&n)
		if err != nil {
			t.Fatalf("lock wait query: %v", err)
		}
		if n >= 1 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func waitForUngrantedUserServiceLock(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	conn := connectDB(t, url)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// A blocked FOR UPDATE waits on the holder's transaction, not on an
		// ungranted tuple row in pg_locks, so the wait shows up on the session.
		var n int
		err := conn.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query LIKE '%user_services%FOR UPDATE%'`).Scan(&n)
		if err != nil {
			t.Fatalf("lock wait query: %v", err)
		}
		if n >= 1 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func scalarText(t *testing.T, url, query string) string {
	t.Helper()
	var s string
	if err := connectDB(t, url).QueryRow(context.Background(), query).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func samePublication(t *testing.T, got, want transit.ServicePublication) {
	t.Helper()
	if got.UserServiceID != want.UserServiceID || got.CompileJobID != want.CompileJobID ||
		got.Name != want.Name || got.Subtext != want.Subtext || got.Description != want.Description ||
		!got.PublishedAt.Equal(want.PublishedAt) {
		t.Fatalf("publication = %+v, want %+v", got, want)
	}
	if len(got.Routes) != len(want.Routes) {
		t.Fatalf("routes = %+v, want %+v", got.Routes, want.Routes)
	}
	for i := range want.Routes {
		if got.Routes[i].ID != want.Routes[i].ID ||
			!coordsEqual(got.Routes[i].Geometry.Coordinates, want.Routes[i].Geometry.Coordinates) {
			t.Fatalf("route %d = %+v, want %+v", i, got.Routes[i], want.Routes[i])
		}
	}
}

func coordsEqual(a, b [][]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
