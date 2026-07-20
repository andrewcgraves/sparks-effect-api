package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/route"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// RouteStore is the slice of the repository route ingestion needs.
type RouteStore interface {
	CreateRoute(ctx context.Context, r transit.Route) error
	GetRouteBySlug(ctx context.Context, slug string) (transit.Route, bool, error)
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
			log.Printf("handler: checking existing route failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		} else if exists {
			writeError(w, http.StatusConflict, "a route with slug "+slug+" already exists")
			return
		}

		// A scenario is optional: an ingested route is a standalone alignment
		// unless the caller names one. The slug is resolved to an ID here so a
		// client can never supply an arbitrary scenario_id directly.
		scenarioID, ok := resolveScenarioOrFail(w, r, store, in.Properties.ScenarioSlug)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			log.Printf("handler: generating route id failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		// Absent bidirectional means true: a physical alignment is traversable
		// both ways unless the author says otherwise.
		bidirectional := true
		if in.Properties.Bidirectional != nil {
			bidirectional = *in.Properties.Bidirectional
		}

		rt := transit.Route{
			ID:            id,
			ScenarioID:    scenarioID,
			Slug:          slug,
			Name:          strings.TrimSpace(in.Properties.Name),
			Mode:          in.Properties.Mode,
			Geometry:      transit.GeoLineString{Type: in.Type, Coordinates: in.Coordinates},
			Bidirectional: bidirectional,
			Segments:      toRouteSegments(in.Properties.Segments),
		}
		if err := store.CreateRoute(r.Context(), rt); err != nil {
			log.Printf("handler: creating route failed: %v", err)
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
			log.Printf("handler: looking up route failed: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		writeJSON(w, http.StatusOK, rt)
	}
}

// resolveScenarioOrFail turns an optional scenario slug into a scenario ID, writing
// the error response itself and reporting ok=false when the caller should stop.
// An empty slug is not an error — it yields a nil (standalone) scenario.
func resolveScenarioOrFail(w http.ResponseWriter, r *http.Request, store RouteStore, slug string) (*string, bool) {
	if slug == "" {
		return nil, true
	}
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		log.Printf("handler: looking up scenario failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if !found {
		writeError(w, http.StatusBadRequest, "unknown scenario_slug "+slug)
		return nil, false
	}
	return &sc.ID, true
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
