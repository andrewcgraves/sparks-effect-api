package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// A user-authored compile target is either a single service or a scenario
// curating several. Every endpoint over one — compile, graph read, isochrone —
// does the same things in the same order: resolve the {slug}, confirm the
// caller owns it (404, never 403), read the latest succeeded compile, and work
// from the target's member services.
//
// That orchestration lives once, in the three functions the handlers call
// (compileAuthoredTarget, authoredTargetGraph, authoredTargetIsochrone). The
// two targets' differences live once too, in the adapters below. HTTP status
// policy stays in the orchestration where it is visible rather than being
// pushed behind the seam: an adapter decides what a target *is*, never what
// status it answers with.

// ServiceTargetStore is the repository slice the service adapter needs:
// resolving the service by slug and reading its latest compiled graph. A
// service is always its own sole member, so nothing loads a membership.
type ServiceTargetStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (transit.Job, bool, error)
}

// ScenarioTargetStore is the repository slice the scenario adapter needs. It is
// ServiceTargetStore's counterpart plus ListUserServicesByIDs: a scenario's
// members are separate rows, and both the routes a graph read bundles and the
// timestamps a staleness check turns on come from them.
type ScenarioTargetStore interface {
	GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error)
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (transit.Job, bool, error)
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]transit.UserService, error)
}

// authoredTarget is one kind of target as the shared orchestration addresses
// it: the way in, from a request to an owned record.
type authoredTarget interface {
	// load resolves the request's {slug} and confirms the caller owns the
	// result, writing the 401/404/500 itself and reporting false when it
	// cannot. The returned target is bound to the loaded record.
	load(w http.ResponseWriter, r *http.Request) (ownedTarget, bool)
}

// ownedTarget is a loaded target whose caller has been confirmed as its owner —
// everything the orchestration does after authorization goes through here.
type ownedTarget interface {
	// noun names the target in client-facing messages ("service",
	// "scenario"), so every response stays word-for-word consistent with that
	// target's own CRUD. It rides with the loaded record rather than the
	// adapter because nothing before authorization needs it.
	noun() string
	// slug is the target's own slug, as the compiled graph is addressed by.
	slug() string
	// compileJob describes the job that compiles this target for owner. Only
	// the kind and target foreign key differ between the two; the rest of the
	// job is filled in uniformly by createCompileJob.
	compileJob(ownerID string) transit.Job
	// latestGraph is the target's most recent succeeded compile.
	latestGraph(ctx context.Context) (transit.Job, bool, error)
	// members are the services this target compiles: the ids it currently
	// curates, and the records that exist for them. The two are returned
	// separately because they can disagree — a curated service since deleted
	// is an id with no record, which is exactly the drift transit.GraphStale
	// answers 409 for, so the id set rather than the loaded rows is the
	// authority on membership.
	members(ctx context.Context) ([]string, []transit.UserService, error)
}

// serviceTarget adapts a single user-authored service: the degenerate
// one-member scenario, compiled and read alone (see CompileUserService).
type serviceTarget struct{ store ServiceTargetStore }

func (t serviceTarget) load(w http.ResponseWriter, r *http.Request) (ownedTarget, bool) {
	svc, ok := loadService(w, r, t.store)
	if !ok {
		return nil, false
	}
	if !authorizeService(w, r, svc) {
		return nil, false
	}
	return ownedService{svc: svc, store: t.store}, true
}

// ownedService is a loaded service the caller owns, compiled and read as the
// degenerate one-member scenario.
type ownedService struct {
	svc   transit.UserService
	store ServiceTargetStore
}

func (t ownedService) noun() string { return "service" }
func (t ownedService) slug() string { return t.svc.Slug }

func (t ownedService) compileJob(ownerID string) transit.Job {
	return transit.Job{
		Kind:          transit.JobKindCompileUserService,
		UserServiceID: &t.svc.ID,
		OwnerID:       &ownerID,
	}
}

func (t ownedService) latestGraph(ctx context.Context) (transit.Job, bool, error) {
	return t.store.GetLatestSucceededUserServiceJob(ctx, t.svc.Slug)
}

// members is the one-member set: a service is always its own sole member, so
// it can only go stale by being edited, and the record needed to tell is the
// one already loaded to authorize the request.
func (t ownedService) members(context.Context) ([]string, []transit.UserService, error) {
	return []string{t.svc.ID}, []transit.UserService{t.svc}, nil
}

// scenarioTarget adapts a user-authored scenario: a curated set of the owner's
// services, compiled and read as one network.
type scenarioTarget struct{ store ScenarioTargetStore }

func (t scenarioTarget) load(w http.ResponseWriter, r *http.Request) (ownedTarget, bool) {
	sc, ok := loadScenario(w, r, t.store)
	if !ok {
		return nil, false
	}
	if !authorizeScenario(w, r, sc) {
		return nil, false
	}
	return ownedScenario{sc: sc, store: t.store}, true
}

// ownedScenario is a loaded scenario the caller owns, compiled and read as one
// network over the services it curates.
type ownedScenario struct {
	sc    transit.UserScenario
	store ScenarioTargetStore
}

func (t ownedScenario) noun() string { return "scenario" }
func (t ownedScenario) slug() string { return t.sc.Slug }

func (t ownedScenario) compileJob(ownerID string) transit.Job {
	return transit.Job{
		Kind:           transit.JobKindCompileUserScenario,
		UserScenarioID: &t.sc.ID,
		OwnerID:        &ownerID,
	}
}

func (t ownedScenario) latestGraph(ctx context.Context) (transit.Job, bool, error) {
	return t.store.GetLatestSucceededUserScenarioJob(ctx, t.sc.Slug)
}

func (t ownedScenario) members(ctx context.Context) ([]string, []transit.UserService, error) {
	services, err := t.store.ListUserServicesByIDs(ctx, t.sc.ServiceIDs)
	if err != nil {
		return nil, nil, err
	}
	return t.sc.ServiceIDs, services, nil
}

// loadCompiledGraph reads the target's latest succeeded compile, writing the
// 404 a client acts on by firing that target's compile — a target that has
// never compiled and one whose compile stored no result are the same "not
// compiled yet" to a caller.
func loadCompiledGraph(w http.ResponseWriter, r *http.Request, target ownedTarget) (transit.Job, bool) {
	job, found, err := target.latestGraph(r.Context())
	if err != nil {
		writeInternalError(w, "looking up compiled graph", err)
		return transit.Job{}, false
	}
	if !found || job.Result == nil {
		writeError(w, http.StatusNotFound, "no compiled graph for this "+target.noun()+" yet")
		return transit.Job{}, false
	}
	return job, true
}

// updatedAtByID indexes member services by id for transit.GraphStale, which
// compares each compiled member against its current edit time.
func updatedAtByID(services []transit.UserService) map[string]time.Time {
	updatedAt := make(map[string]time.Time, len(services))
	for _, svc := range services {
		updatedAt[svc.ID] = svc.UpdatedAt
	}
	return updatedAt
}
