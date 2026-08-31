package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// RouteStore is the slice of the repository route ingestion and reads need.
type RouteStore interface {
	CreateRoute(ctx context.Context, r transit.Route) error
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
	ListCuratedRouteSummaries(ctx context.Context) ([]transit.RouteSummary, error)
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
}

// CreateRoute ingests an admin-authored alignment: a GeoJSON LineString whose
// per-segment track physics live in its properties. It is registered behind
// RequireAdmin, which is the whole of its access control — the handler itself
// makes no authorization decision.
//
// The response is the persisted route, whose slug is how it is addressed from
// then on.
func CreateRoute(store RouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in route.Ingest
		dec := json.NewDecoder(r.Body)
		// Unknown fields are rejected rather than ignored. A misspelled physics
		// key (cant__mm) would otherwise decode to a zero-valued segment and
		// sail through range validation as tangent, level track — silently
		// storing physics the author never wrote.
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
			return
		}

		// Validation is a pure function over the payload, so every geometry and
		// physics rule is exercised in internal/route's own tests rather than
		// through HTTP. Its messages name the offending field and segment, so
		// they are returned to the client as-is.
		if err := route.Validate(in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		slug := in.Properties.Slug
		if slug == "" {
			slug = route.Slugify(in.Properties.Name)
		}
		if slug == "" {
			// Validate accepts any non-blank name, but a name of pure
			// punctuation cannot produce an addressable slug.
			writeError(w, http.StatusBadRequest,
				"could not derive a slug from the name; supply an explicit slug")
			return
		}

		// Checked up front for a clean 409. The UNIQUE constraint on
		// routes.slug is still the authority under a concurrent create; this
		// only spares the common case an opaque database error.
		if _, exists, err := store.GetRouteBySlug(r.Context(), slug); err != nil {
			slog.ErrorContext(r.Context(), "handler: checking existing route failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if exists {
			writeError(w, http.StatusConflict, "a route with slug "+slug+" already exists")
			return
		}

		// A scenario is optional: an ingested route is a standalone alignment
		// unless the caller names one. The slug is resolved to an ID here so a
		// client can never supply an arbitrary scenario_id directly.
		//
		// It must resolve to a *curated* scenario. An admin passes CanAccess on
		// everything, so without the filter this endpoint could drop an unowned
		// route into a user's scenario and break the ownership-uniformity
		// invariant the curated reads depend on.
		scenarioID, ok := resolveCuratedScenarioOrFail(w, r, store, in.Properties.ScenarioSlug)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: generating route id failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// OwnerID is left nil deliberately: an admin-ingested alignment is
		// curated platform data, not the admin's personal property. Setting it
		// from the caller's identity would drop every ingested route out of the
		// public picker, which is the opposite of why they are ingested.
		rt := buildRouteFromIngest(in, id, slug, scenarioID, nil)
		if err := store.CreateRoute(r.Context(), rt); err != nil {
			slog.ErrorContext(r.Context(), "handler: creating route failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		writeJSON(w, http.StatusCreated, rt)
	}
}

// RouteBySlug returns a handler that fetches one route by its globally unique
// slug — geometry, per-segment physics, and metadata — for the public
// /routes/:slug preview. Unlike scenario reads, it is backed by RouteStore
// (Postgres) rather than the embedded scenario store, since ingested routes
// are addressed independently of any scenario.
func RouteBySlug(store RouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		rt, ok, err := store.GetRouteBySlug(r.Context(), slug)
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: looking up route failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok || !mayReadRoute(r.Context(), rt) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		writeJSON(w, http.StatusOK, rt)
	}
}

// Routes returns a handler that lists routes for a picker: enough to show a
// choice and address the one that is chosen, no more. It is the discovery half
// of RouteBySlug — without it a client would have to already know a slug to
// read anything.
//
// Like RouteBySlug it reads Postgres rather than the embedded scenario store,
// so it spans everything addressable by slug: admin-ingested alignments and the
// seeded scenario routes alike. Both are real choices, so the picker is offered
// both.
func Routes(store RouteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		routes, err := store.ListCuratedRouteSummaries(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "handler: listing routes failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if routes == nil {
			routes = []transit.RouteSummary{}
		}
		writeJSON(w, http.StatusOK, routes)
	}
}

// resolveCuratedScenarioOrFail turns an optional scenario slug into a scenario
// ID, writing the error response itself and reporting ok=false when the caller
// should stop. An empty slug is not an error — it yields a nil (standalone)
// scenario.
//
// A scenario someone owns is reported as unknown rather than refused: this is
// the admin ingest path, and an owned scenario is simply not a place curated
// content belongs.
func resolveCuratedScenarioOrFail(w http.ResponseWriter, r *http.Request, store RouteStore, slug string) (*string, bool) {
	if slug == "" {
		return nil, true
	}
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: looking up scenario failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if !found || !isCuratedScenario(sc) {
		writeError(w, http.StatusBadRequest, "unknown scenario_slug "+slug)
		return nil, false
	}
	return &sc.ID, true
}

// buildRouteFromIngest turns a validated payload into the domain route. It is
// the shared middle of the two create paths — admin ingestion and an owner
// authoring their own alignment — which differ only in slug policy and owner,
// both of which the caller has already decided.
func buildRouteFromIngest(in route.Ingest, id, slug string, scenarioID, ownerID *string) transit.Route {
	// Absent bidirectional means true: a physical alignment is traversable
	// both ways unless the author says otherwise.
	bidirectional := true
	if in.Properties.Bidirectional != nil {
		bidirectional = *in.Properties.Bidirectional
	}

	return transit.Route{
		ID:            id,
		ScenarioID:    scenarioID,
		OwnerID:       ownerID,
		Slug:          slug,
		Name:          strings.TrimSpace(in.Properties.Name),
		Description:   strings.TrimSpace(in.Properties.Description),
		Mode:          in.Properties.Mode,
		Geometry:      transit.GeoLineString{Type: in.Type, Coordinates: in.Coordinates},
		Bidirectional: bidirectional,
		Segments:      toRouteSegments(in.Properties.Segments),
	}
}

// toRouteSegments converts the validated ingestion segments to their domain
// form. The two types are deliberately separate: internal/route describes the
// wire payload, transit.RouteSegment is what the compiler and storage use.
func toRouteSegments(segs []route.Segment) []transit.RouteSegment {
	if len(segs) == 0 {
		return nil
	}
	out := make([]transit.RouteSegment, len(segs))
	for i, s := range segs {
		out[i] = transit.RouteSegment{
			CantMM:       s.CantMM,
			CurveRadiusM: s.CurveRadiusM,
			GradePct:     s.GradePct,
		}
	}
	return out
}
