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

// OwnedRouteStore is the slice of the repository the owner-scoped route CRUD
// needs. It is narrower than transit.Repository so these handlers can be tested
// against a small fake.
//
// GetRouteBySlug is unfiltered on purpose: routes.slug is globally unique
// across curated and owned rows, so minting a slug has to be able to see the
// whole namespace. Ownership is decided here, against the row it returns.
type OwnedRouteStore interface {
	CreateRoute(ctx context.Context, rt transit.Route) error
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
	UpdateRoute(ctx context.Context, rt transit.Route) error
	DeleteRoute(ctx context.Context, id string) error
	CountRouteDependents(ctx context.Context, routeID string) (transit.RouteDependents, error)
	ListRouteSummariesByOwner(ctx context.Context, ownerID string) ([]transit.RouteSummary, error)
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
}

// maxRouteBodyBytes caps a request body. Route geometry is the largest payload
// this API accepts by a wide margin — a real alignment runs to thousands of
// coordinates — so the ceiling is higher than the 1 MiB the other authoring
// endpoints use, while still bounding what a single request can cost.
const maxRouteBodyBytes = 8 << 20 // 8 MiB

// CreateOwnedRoute persists an alignment the caller owns.
//
// It shares route.Validate and buildRouteFromIngest with the admin ingestion
// endpoint, and differs in exactly two ways. The slug is always minted from the
// name rather than honoured from the payload: an admin curating platform data
// is choosing a public address, while a user naming their own draft is not, and
// letting them claim an arbitrary slug would let them squat one. And the route
// is stamped with the caller's id, which is what keeps it out of the public
// picker.
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

// MyRoutes returns the alignments owned by the authenticated caller, standalone
// and scenario-bound alike.
//
// The owner ID comes from the request context — the identity the middleware
// resolved from the bearer token — and never from the request itself, so there
// is no parameter a caller could set to read someone else's rows.
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

// GetOwnedRoute returns one of the caller's own alignments, whole — geometry
// and per-segment physics included, unlike the summary the list returns.
func GetOwnedRoute(store OwnedRouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt, ok := loadOwnedRoute(w, r, store)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, rt)
	}
}

// UpdateOwnedRoute rewrites an alignment the caller owns. The payload is the
// same shape a create takes, so a client edits by reading, changing, and
// sending the whole thing back.
//
// The slug is not re-derived from a changed name: it is the route's address,
// and re-slugging would break every link to it and orphan the travel-time
// segments that name it.
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

// DeleteOwnedRoute removes an alignment the caller owns, once nothing depends
// on it.
//
// The dependency check is not politeness. user_services.route_id is ON DELETE
// CASCADE, so an unchecked delete here would silently destroy saved services —
// including other people's, since a curated route is referenceable by anyone.
// Refusing with a 409 makes that cascade unreachable through the API.
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

// loadOwnedRoute resolves the {slug} path value and applies the ownership rule,
// writing the response itself and reporting ok=false when the caller should
// stop.
//
// A route the caller does not own answers 404 rather than 403, the convention
// the authored-service and authored-scenario handlers already follow: unlike
// the curated data, the set of authored slugs is not public knowledge, and a
// slug derived from a user-chosen name is guessable enough that 403 would
// confirm it exists. A curated route lands here too — its owner is nil, so
// CanAccess admits only admins — which is what keeps the seeded alignments
// read-only for everyone else.
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

// resolveOwnScenarioOrFail turns an optional scenario slug into a scenario id
// the caller may author inside. An empty slug is not an error — it yields a nil
// (standalone) scenario.
//
// This is the enforcement point for the ownership-uniformity invariant on the
// create and update paths. Note it is CanAccess, not CanReference: a curated
// scenario is a public building block to *reference*, but authoring a route
// into one is mutating it, and that stays admin-only.
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

// decodeRouteIngest reads and validates the GeoJSON ingestion payload shared by
// both create paths.
//
// Unknown fields are rejected rather than ignored, as they are for admin
// ingestion: a misspelled physics key (cant__mm) would otherwise decode to a
// zero-valued segment and sail through range validation as tangent, level
// track — silently storing physics the author never wrote.
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

// mintRouteSlug derives a URL-safe slug from name, appending -2, -3, ... until
// it finds one no route is using. It returns "" when the name slugifies to
// nothing, which the caller reports as a 422.
//
// The namespace it probes spans curated and owned routes alike, because
// routes.slug is globally unique: a user naming their draft "Phase 1" gets
// phase-1-2 rather than a constraint violation.
//
// This is check-then-insert, so two concurrent creates of the same name can
// both see a slug free and the loser will fail the UNIQUE constraint with a
// 500. Acceptable at present scale, and the same trade mintSlug already makes;
// the constraint is what keeps it correct.
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
