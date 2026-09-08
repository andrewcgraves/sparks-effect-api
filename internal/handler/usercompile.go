package handler

import (
	"context"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func CompileUserService(store CompileStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compileAuthoredTarget(w, r, store, serviceTarget{store}, boardingWait)
	}
}

func CompileUserScenario(store CompileStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compileAuthoredTarget(w, r, store, scenarioTarget{store}, boardingWait)
	}
}

func compileAuthoredTarget(w http.ResponseWriter, r *http.Request, store CompileStore, target authoredTarget, boardingWait transit.BoardingWaitPolicy) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	owned, ok := target.load(w, r)
	if !ok {
		return
	}

	job, ok := createCompileJob(w, r, store, owned.compileJob(user.ID))
	if !ok {
		return
	}
	enqueueCompile(store, job, boardingWait)
	writeJSON(w, http.StatusAccepted, job)
}

func UserScenarioGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetGraph(w, r, store, scenarioTarget{store})
	}
}

func UserServiceGraph(store CompileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authoredTargetGraph(w, r, store, serviceTarget{store})
	}
}

func authoredTargetGraph(w http.ResponseWriter, r *http.Request, store routeStore, target authoredTarget) {
	owned, ok := target.load(w, r)
	if !ok {
		return
	}

	job, ok := loadCompiledGraph(w, r, owned)
	if !ok {
		return
	}

	_, members, err := owned.members(r.Context())
	if err != nil {
		writeInternalError(r.Context(), w, "loading member services", err)
		return
	}
	routes, err := routesByIDs(r.Context(), store, serviceRouteIDs(members))
	if err != nil {
		writeInternalError(r.Context(), w, "loading routes", err)
		return
	}

	writeJSON(w, http.StatusOK, compiledGraphResponse{
		TransitGraph: job.Result,
		Routes:       routes,
	})
}

type compiledGraphResponse struct {
	*transit.TransitGraph
	Routes []transit.Route `json:"routes"`
}

type routeStore interface {
	ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error)
}

func serviceRouteIDs(services []transit.UserService) []string {
	seen := make(map[string]bool, len(services))
	ids := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.RouteID == "" || seen[svc.RouteID] {
			continue
		}
		seen[svc.RouteID] = true
		ids = append(ids, svc.RouteID)
	}
	return ids
}

func routesByIDs(ctx context.Context, store routeStore, ids []string) ([]transit.Route, error) {
	routes, err := store.ListRoutesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if routes == nil {
		routes = []transit.Route{}
	}
	return routes, nil
}

func createCompileJob(w http.ResponseWriter, r *http.Request, store CompileStore, job transit.Job) (transit.Job, bool) {
	id, err := ids.NewUUID()
	if err != nil {
		writeInternalError(r.Context(), w, "generating job id", err)
		return transit.Job{}, false
	}
	job.ID = id
	job.Status = transit.JobStatusQueued

	if err := store.CreateJob(r.Context(), job); err != nil {
		writeInternalError(r.Context(), w, "creating job", err)
		return transit.Job{}, false
	}
	return job, true
}
