package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// PrerenderedStore is the slice of the repository the prerendered isochrone
// surface needs: the scenario a curated entry hangs off, that scenario's
// current service membership (which is what makes an entry outdated), and the
// entries themselves.
//
// The membership read is here rather than derived from a compiled graph
// because a prerendered isochrone is not tied to one compile: it is a payload
// someone curated against the scenario as it stood, and the question asked of
// it on every read is whether the scenario has moved on since.
type PrerenderedStore interface {
	GetScenarioBySlug(ctx context.Context, slug string) (transit.Scenario, bool, error)
	ListServiceMembershipByScenario(ctx context.Context, scenarioID string) ([]transit.ServiceMembership, error)
	ListPrerenderedIsochronesByScenario(ctx context.Context, scenarioSlug string) ([]transit.PrerenderedIsochrone, error)
	GetPrerenderedIsochrone(ctx context.Context, id string) (transit.PrerenderedIsochrone, bool, error)
	CreatePrerenderedIsochrone(ctx context.Context, p *transit.PrerenderedIsochrone) error
}

// prerenderedResponse is what every one of these endpoints answers with.
//
// It is a response type of its own rather than transit.PrerenderedIsochrone
// because two of its fields are not the domain type's to have: outdated is
// computed per request against the scenario's live membership and is never
// stored, and result must be absent — not empty, absent — from a list. The
// storage-only fields (scenario_slug, the membership snapshot, updated_at)
// stay off the wire for the same reason: a client renders these, it does not
// audit them.
//
// result carries omitempty so the list read, which never selects the column,
// produces a body with no such key at all. That is the contract: a client
// seeing "result" knows it has the payload, and never has to tell a missing
// payload from an empty one.
type prerenderedResponse struct {
	ID         string             `json:"id"`
	Label      string             `json:"label"`
	Lat        float64            `json:"lat"`
	Lng        float64            `json:"lng"`
	BudgetMins int                `json:"budget_mins"`
	Mode       transit.TravelMode `json:"mode"`
	Outdated   bool               `json:"outdated"`
	CreatedAt  time.Time          `json:"created_at"`
	Result     json.RawMessage    `json:"result,omitempty"`
}

// prerenderedMeta projects one stored entry onto the wire without its payload.
func prerenderedMeta(p transit.PrerenderedIsochrone, outdated bool) prerenderedResponse {
	return prerenderedResponse{
		ID:         p.ID,
		Label:      p.Label,
		Lat:        p.Lat,
		Lng:        p.Lng,
		BudgetMins: p.BudgetMins,
		Mode:       p.Mode,
		Outdated:   outdated,
		CreatedAt:  p.CreatedAt,
	}
}

// prerenderedCreateRequest is the admin POST body. result is json.RawMessage
// so it survives decoding byte for byte: this API does not know what an
// isochrone payload looks like and must not round-trip one through a type that
// decides for it.
type prerenderedCreateRequest struct {
	Label      string          `json:"label"`
	Lat        float64         `json:"lat"`
	Lng        float64         `json:"lng"`
	BudgetMins int             `json:"budget_mins"`
	Mode       string          `json:"mode"`
	Result     json.RawMessage `json:"result"`
}

// PrerenderedIsochrones returns a handler for
// GET /api/scenarios/{slug}/prerendered-isochrones: the metadata of every
// curated isochrone a scenario ships with, in creation order.
//
// Public, like the scenario reads beside it — these are the illustrations a
// scenario's page shows before anyone has signed in or dropped a pin.
//
// The payloads are deliberately absent. Each runs 300-500KB and the store's
// list query does not even select the column, so listing a scenario with a
// dozen entries costs a few kilobytes rather than several megabytes of data
// the caller would immediately throw away. A client picks one from this list
// and fetches it by id.
//
// An unknown slug is 404 rather than an empty list: "this scenario has no
// curated isochrones" and "there is no such scenario" are different answers,
// and only one of them means the caller got the slug wrong.
func PrerenderedIsochrones(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := lookupScenario(w, r, store, r.PathValue("slug"))
		if !ok {
			return
		}

		entries, err := store.ListPrerenderedIsochronesByScenario(r.Context(), sc.Slug)
		if err != nil {
			writeInternalError(r.Context(), w, "listing prerendered isochrones", err)
			return
		}

		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		out := make([]prerenderedResponse, 0, len(entries))
		for _, p := range entries {
			out = append(out, prerenderedMeta(p, transit.PrerenderedOutdated(p, members)))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// PrerenderedIsochrone returns a handler for
// GET /api/prerendered-isochrones/{id}: one curated entry, payload included.
//
// Public for the same reason the list is, and addressed by id rather than
// nested under its scenario because that is what the list hands out — a client
// that has chosen one already knows the only thing this needs.
//
// The payload is served exactly as it was stored. An outdated entry is served
// too: outdated says the scenario has changed since the payload was computed,
// which is worth telling a client and is not a reason to withhold the one
// thing it asked for. That is the whole difference from a stale compiled
// graph, which is refused (see transit.GraphStale's caller).
func PrerenderedIsochrone(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, found, err := store.GetPrerenderedIsochrone(r.Context(), r.PathValue("id"))
		if err != nil {
			writeInternalError(r.Context(), w, "looking up prerendered isochrone", err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "prerendered isochrone not found")
			return
		}

		// The row names its scenario by slug, which is all outdated needs
		// resolving. A missing scenario cannot happen while the row exists —
		// the foreign key CASCADEs — so this is a lookup, not a validation.
		sc, ok := lookupScenario(w, r, store, p.ScenarioSlug)
		if !ok {
			return
		}
		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		out := prerenderedMeta(p, transit.PrerenderedOutdated(p, members))
		out.Result = p.Result
		writeJSON(w, http.StatusOK, out)
	}
}

// CreatePrerenderedIsochrone returns a handler for
// POST /api/scenarios/{slug}/prerendered-isochrones: curating a new entry.
//
// Admin-only (the gate is at registration, auth.RequireAdmin), because these
// are editorial content on a public page rather than anyone's own work.
//
// What it validates is what it can be wrong about: a label that is not there,
// a mode outside transit.TravelMode's set, a budget that is not a duration, a
// scenario that does not exist. It validates nothing about result beyond its
// presence — the API neither produces nor understands an isochrone payload, so
// any shape it insisted on would be a guess about someone else's output that
// could only go stale. Presence is checked because an entry with no payload is an entry
// nothing can display, and the column is NOT NULL.
//
// The membership snapshot is taken here, from the scenario's services as they
// stand at this moment. That is what makes the entry report outdated later
// without anything having to watch it: the snapshot is a statement about when
// the payload was true.
//
// The 201 carries the created entry's metadata and not its payload — the
// caller just sent that, and echoing half a megabyte back proves nothing.
func CreatePrerenderedIsochrone(store PrerenderedStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req prerenderedCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		req.Label = strings.TrimSpace(req.Label)
		if req.Label == "" {
			writeError(w, http.StatusBadRequest, "label is required")
			return
		}
		// The budget and mode checks are the ones every isochrone request
		// already goes through, so what may be curated and what may be
		// requested cannot drift apart.
		if !validateIsochroneParams(w, req.BudgetMins, req.Mode) {
			return
		}
		if len(req.Result) == 0 {
			writeError(w, http.StatusBadRequest, "result is required")
			return
		}

		sc, ok := lookupScenario(w, r, store, r.PathValue("slug"))
		if !ok {
			return
		}
		members, ok := loadMembership(w, r, store, sc)
		if !ok {
			return
		}

		id, err := ids.NewUUID()
		if err != nil {
			writeInternalError(r.Context(), w, "generating prerendered isochrone id", err)
			return
		}

		entry := transit.PrerenderedIsochrone{
			ID:                 id,
			ScenarioSlug:       sc.Slug,
			Label:              req.Label,
			Lat:                req.Lat,
			Lng:                req.Lng,
			BudgetMins:         req.BudgetMins,
			Mode:               transit.TravelMode(req.Mode),
			Result:             req.Result,
			CompiledServiceIDs: transit.MembershipIDs(members),
		}
		if err := store.CreatePrerenderedIsochrone(r.Context(), &entry); err != nil {
			writeInternalError(r.Context(), w, "creating prerendered isochrone", err)
			return
		}

		// Freshly snapshotted against the membership just read, so it cannot be
		// outdated — stated by construction rather than recomputed.
		writeJSON(w, http.StatusCreated, prerenderedMeta(entry, false))
	}
}

// lookupScenario resolves a scenario slug, writing the 404 or 500 and
// reporting ok=false when it cannot.
func lookupScenario(w http.ResponseWriter, r *http.Request, store PrerenderedStore, slug string) (transit.Scenario, bool) {
	sc, found, err := store.GetScenarioBySlug(r.Context(), slug)
	if err != nil {
		writeInternalError(r.Context(), w, "looking up scenario", err)
		return transit.Scenario{}, false
	}
	// Curated scenarios only. A prerendered isochrone is editorial content on a
	// public page — authored by an admin, served to anyone — so an owned
	// scenario is simply not a place one can hang. Reported as not found rather
	// than refused, so this endpoint cannot be used to probe which owned slugs
	// exist.
	if !found || sc.OwnerID != nil {
		writeError(w, http.StatusNotFound, "scenario not found")
		return transit.Scenario{}, false
	}
	return sc, true
}

// loadMembership reads the scenario's current curated services, writing the
// 500 and reporting ok=false when it cannot. It is the input both the
// outdated computation and the create-time snapshot are built from, so both
// see the same definition of "the scenario's services".
func loadMembership(w http.ResponseWriter, r *http.Request, store PrerenderedStore,
	sc transit.Scenario) ([]transit.ServiceMembership, bool) {
	members, err := store.ListServiceMembershipByScenario(r.Context(), sc.ID)
	if err != nil {
		writeInternalError(r.Context(), w, "loading scenario service membership", err)
		return nil, false
	}
	return members, true
}
