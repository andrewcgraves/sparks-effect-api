package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

var ErrStaleGraph = errors.New("compiled graph is stale")

type PublicationRead interface {
	LatestSucceededCompileJob(ctx context.Context, serviceID string) (transit.Job, bool, error)
	ListRoutesByIDs(ctx context.Context, ids []string) ([]transit.Route, error)
}

type PublicationDecide func(context.Context, transit.UserService, PublicationRead) (transit.ServicePublication, error)

type PublicationStore interface {
	GetUserServiceBySlug(ctx context.Context, slug string) (transit.UserService, bool, error)
	PublishUserService(ctx context.Context, serviceID string, decide PublicationDecide) (transit.ServicePublication, error)
	UnpublishUserService(ctx context.Context, serviceID string) error
}

type PublishedServiceStore interface {
	GetServicePublicationBySlug(ctx context.Context, slug string) (transit.ServicePublication, bool, error)
	GetSucceededCompileJob(ctx context.Context, id string) (transit.Job, bool, error)
}

// The graph is embedded flat, as compiledGraphResponse embeds it, so a client
// that draws GET /api/services/{slug}/graph can draw a publication unchanged.
type publicationResponse struct {
	transit.ServicePublication
	*transit.TransitGraph
}

func GetServicePublication(store PublishedServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// No identity is read, so the response depends on the slug alone: the
		// owner sees what everyone sees, and a shared cache cannot mix a draft
		// into it. The draft stays at GET /api/services/{slug} (ADR-0005).
		pub, found, err := store.GetServicePublicationBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			writeInternalError(r.Context(), w, "loading publication", err)
			return
		}
		// Unpublished answers exactly as unknown does, before a first publish
		// and after an unpublish alike. Anything else confirms to a stranger
		// that a draft exists behind a guessed slug.
		if !found {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}

		job, found, err := store.GetSucceededCompileJob(r.Context(), pub.CompileJobID)
		if err != nil {
			writeInternalError(r.Context(), w, "loading published graph", err)
			return
		}
		// The pin's foreign key holds the job for as long as it is pinned, so a
		// miss means the publication just read was removed in between — most
		// plausibly the service being deleted.
		if !found {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}
		if job.Result == nil {
			writeInternalError(r.Context(), w, "loading published graph",
				fmt.Errorf("pinned compile job %s has no graph", job.ID))
			return
		}
		writeJSON(w, http.StatusOK, publicationResponse{ServicePublication: pub, TransitGraph: job.Result})
	}
}

func PublishService(store PublicationStore, boardingWait transit.BoardingWaitPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}

		// decide runs inside the draft row lock. The staleness check and the
		// snapshot write share that lock, so an edit cannot land between them.
		pub, err := store.PublishUserService(r.Context(), svc.ID, func(ctx context.Context, locked transit.UserService, read PublicationRead) (transit.ServicePublication, error) {
			return PublicationFromLockedDraft(ctx, locked, read, boardingWait)
		})
		if errors.Is(err, ErrStaleGraph) {
			writeErrorCode(w, http.StatusConflict, StaleGraphErrorCode,
				"compiled graph is stale; recompile the service and retry")
			return
		}
		if err != nil {
			writeInternalError(r.Context(), w, "publishing service", err)
			return
		}
		writeJSON(w, http.StatusOK, pub)
	}
}

func UnpublishService(store PublicationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, ok := loadService(w, r, store)
		if !ok {
			return
		}
		if !authorizeService(w, r, svc) {
			return
		}
		if err := store.UnpublishUserService(r.Context(), svc.ID); err != nil {
			writeInternalError(r.Context(), w, "unpublishing service", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func PublicationFromLockedDraft(ctx context.Context, svc transit.UserService, read PublicationRead, global transit.BoardingWaitPolicy) (transit.ServicePublication, error) {
	job, found, err := read.LatestSucceededCompileJob(ctx, svc.ID)
	if err != nil {
		return transit.ServicePublication{}, err
	}
	if !currentCompile(svc, job, found, global) {
		return transit.ServicePublication{}, ErrStaleGraph
	}

	ids := edgeRouteIDs(job)
	var loaded []transit.Route
	if len(ids) > 0 {
		loaded, err = read.ListRoutesByIDs(ctx, ids)
		if err != nil {
			return transit.ServicePublication{}, err
		}
	}
	routes, err := routesInEdgeOrder(ids, loaded)
	if err != nil {
		return transit.ServicePublication{}, err
	}
	return transit.ServicePublication{
		UserServiceID: svc.ID,
		CompileJobID:  job.ID,
		Name:          svc.Name,
		Subtext:       svc.Subtext,
		Description:   svc.Description,
		Routes:        routes,
	}, nil
}

func currentCompile(svc transit.UserService, job transit.Job, found bool, global transit.BoardingWaitPolicy) bool {
	if !found || job.Result == nil || job.Kind != transit.JobKindCompileUserService || job.Status != transit.JobStatusSucceeded {
		return false
	}
	// A user service has no scenario boarding-wait override. The resolved
	// policy is the service's own, else the global one the route was
	// registered with — the same inputs the authored isochrone uses.
	policies := resolvedBoardingWaitByService([]transit.UserService{svc}, nil, global)
	return !transit.GraphStale(job, []string{svc.ID}, map[string]time.Time{svc.ID: svc.UpdatedAt}, policies)
}

func edgeRouteIDs(job transit.Job) []string {
	if job.Result == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, sg := range job.Result.Services {
		for _, e := range sg.Edges {
			if e.RouteID == "" {
				continue
			}
			if _, ok := seen[e.RouteID]; ok {
				continue
			}
			seen[e.RouteID] = struct{}{}
			ids = append(ids, e.RouteID)
		}
	}
	return ids
}

func routesInEdgeOrder(ids []string, loaded []transit.Route) ([]transit.Route, error) {
	byID := make(map[string]transit.Route, len(loaded))
	for _, rt := range loaded {
		byID[rt.ID] = rt
	}
	out := make([]transit.Route, 0, len(ids))
	for _, id := range ids {
		rt, ok := byID[id]
		if !ok {
			// The graph names a route the table does not have. That is the
			// same refusal as a stale compile: do not publish a partial snapshot.
			return nil, ErrStaleGraph
		}
		out = append(out, rt)
	}
	return out, nil
}
