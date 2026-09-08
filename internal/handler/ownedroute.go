package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type OwnedRouteStore interface {
	CreateRoute(ctx context.Context, rt transit.Route) error
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
	UpdateRoute(ctx context.Context, rt transit.Route) error
	DeleteRoute(ctx context.Context, id string) error
	CountRouteDependents(ctx context.Context, routeID string) (transit.RouteDependents, error)
	ListRouteSummariesByOwner(ctx context.Context, ownerID string) ([]transit.RouteSummary, error)
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
}

const maxRouteBodyBytes = 8 << 20

func CreateOwnedRoute(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		in, ok := decodeRouteIngest(w, r)
		if !ok {
			return
		}

		// A scenario is optional: a route is a standalone alignment unless the
		// caller names one. When they do it must be a scenario they own — the
		// ownership-uniformity invariant, without which an owned route could be
		// smuggled into the curated ca-hsr scenario and compiled into the
		// public graph.
		scenarioID, ok := resolveOwnScenarioOrFail(w, r, store, user, in.Properties.ScenarioSlug)
		if !ok {
			return
		}

		slug, err := mintRouteSlug(r.Context(), store, in.Properties.Name)
		if err != nil {
			writeInternalError(r.Context(), w, "minting route slug", err)
			return
		}
		if slug == "" {
			// Validate accepts any non-blank name, but a name of pure
			// punctuation cannot produce an addressable slug.
			writeError(w, http.StatusUnprocessableEntity,
				"could not derive a slug from the name; give the route a name with letters or digits in it")
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "minting route id", err)
			return
		}

		rt := buildRouteFromIngest(in, id, slug, scenarioID, &user.ID)
		if err := store.CreateRoute(r.Context(), rt); err != nil {
			writeInternalError(r.Context(), w, "creating route", err)
			return
		}

		w.Header().Set("Location", "/api/me/routes/"+rt.Slug)
		writeJSON(w, http.StatusCreated, rt)
	}
}

func MyRoutes(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		routes, err := store.ListRouteSummariesByOwner(r.Context(), user.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "listing owned routes", err)
			return
		}
		if routes == nil {
			routes = []transit.RouteSummary{}
		}
		writeJSON(w, http.StatusOK, routes)
	}
}

func GetOwnedRoute(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, ok := loadOwnedRoute(w, r, store)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, rt)
	}
}

func UpdateOwnedRoute(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, ok := loadOwnedRoute(w, r, store)
		if !ok {
			return
		}
		user, _ := auth.UserFrom(r.Context())

		in, ok := decodeRouteIngest(w, r)
		if !ok {
			return
		}

		scenarioID, ok := resolveOwnScenarioOrFail(w, r, store, user, in.Properties.ScenarioSlug)
		if !ok {
			return
		}

		// Identity carries over from the stored row: buildRouteFromIngest is
		// handed the existing id, slug, and owner rather than anything the
		// client sent, so neither can be reassigned through an update.
		updated := buildRouteFromIngest(in, rt.ID, rt.Slug, scenarioID, rt.OwnerID)
		if err := store.UpdateRoute(r.Context(), updated); err != nil {
			writeInternalError(r.Context(), w, "updating route", err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func DeleteOwnedRoute(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, ok := loadOwnedRoute(w, r, store)
		if !ok {
			return
		}

		deps, err := store.CountRouteDependents(r.Context(), rt.ID)
		if err != nil {
			writeInternalError(r.Context(), w, "counting route dependents", err)
			return
		}
		if deps.Any() {
			writeErrorDetail(w, http.StatusConflict, "route_in_use",
				"this route still has services or segments built on it", deps)
			return
		}

		if err := store.DeleteRoute(r.Context(), rt.ID); err != nil {
			writeInternalError(r.Context(), w, "deleting route", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func loadOwnedRoute(w http.ResponseWriter, r *http.Request, store OwnedRouteStore) (transit.Route, bool) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return transit.Route{}, false
	}

	rt, found, err := store.GetRouteBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeInternalError(r.Context(), w, "looking up route", err)
		return transit.Route{}, false
	}
	if !found || !auth.CanAccess(user, rt.OwnerID) {
		writeError(w, http.StatusNotFound, "route not found")
		return transit.Route{}, false
	}
	return rt, true
}

func resolveOwnScenarioOrFail(
	w http.ResponseWriter, r *http.Request, store OwnedRouteStore,
	user transit.User, slug string,
) (*string, bool) {
	if slug == "" {
		return nil, true
	}
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return nil, false
	}
	if !found || !auth.CanAccess(user, sc.OwnerID) {
		// Reported as unknown rather than forbidden, for loadOwnedRoute's
		// reason: a scenario the caller cannot reach should not be
		// distinguishable from one that does not exist.
		writeError(w, http.StatusUnprocessableEntity, "unknown scenario_slug "+slug)
		return nil, false
	}
	return &sc.ID, true
}

func decodeRouteIngest(w http.ResponseWriter, r *http.Request) (route.Ingest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRouteBodyBytes)

	var in route.Ingest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return route.Ingest{}, false
		}
		writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
		return route.Ingest{}, false
	}

	// Validation is a pure function over the payload, so every geometry and
	// physics rule is exercised in internal/route's own tests rather than
	// through HTTP. Its messages name the offending field and segment, so they
	// are returned to the client as-is.
	if err := route.Validate(in); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return route.Ingest{}, false
	}
	return in, true
}

func mintRouteSlug(ctx context.Context, store OwnedRouteStore, name string) (string, error) {
	base := route.Slugify(name)
	if base == "" {
		return "", nil
	}
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		_, taken, err := store.GetRouteBySlug(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}
