package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type ServiceTargetStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	GetLatestSucceededUserServiceJob(ctx context.Context, userServiceSlug string) (transit.Job, bool, error)
}

type ScenarioTargetStore interface {
	GetUserScenarioBySlug(ctx context.Context, slug string) (transit.UserScenario, bool, error)
	GetLatestSucceededUserScenarioJob(ctx context.Context, userScenarioSlug string) (transit.Job, bool, error)
	ListUserServicesByIDs(ctx context.Context, ids []string) ([]transit.UserService, error)
}

type authoredTarget interface {
	load(w http.ResponseWriter, r *http.Request) (ownedTarget, bool)
}

type ownedTarget interface {
	noun() string
	slug() string
	compileJob(ownerID string) transit.Job
	latestGraph(ctx context.Context) (transit.Job, bool, error)
	members(ctx context.Context) ([]string, []transit.UserService, error)
	scenarioBoardingWait() *transit.BoardingWaitOverride
}

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

func (t ownedService) members(context.Context) ([]string, []transit.UserService, error) {
	return []string{t.svc.ID}, []transit.UserService{t.svc}, nil
}

func (t ownedService) scenarioBoardingWait() *transit.BoardingWaitOverride { return nil }

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

func (t ownedScenario) scenarioBoardingWait() *transit.BoardingWaitOverride {
	return t.sc.BoardingWait
}

func loadCompiledGraph(w http.ResponseWriter, r *http.Request, target ownedTarget) (transit.Job, bool) {
	job, found, err := target.latestGraph(r.Context())
	if err != nil {
		writeInternalError(r.Context(), w, "looking up compiled graph", err)
		return transit.Job{}, false
	}
	if !found || job.Result == nil {
		writeError(w, http.StatusNotFound, "no compiled graph for this "+target.noun()+" yet")
		return transit.Job{}, false
	}
	return job, true
}

func updatedAtByID(services []transit.UserService) map[string]time.Time {
	updatedAt := make(map[string]time.Time, len(services))
	for _, svc := range services {
		updatedAt[svc.ID] = svc.UpdatedAt
	}
	return updatedAt
}
