package handler

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Ownership predicates shared by the handlers that reach a seeded scenario or
// route by slug, plus the two rules that keep a child row's owner in step with
// its parent's.
//
// They exist because owned and curated rows share tables. The collection reads
// filter in SQL (Repository.ListCuratedScenarios, ListCuratedRouteSummaries),
// but a by-slug read cannot: slugs are globally unique across both kinds, so
// minting one has to see the whole namespace. That leaves the by-slug paths to
// decide for themselves, and having them all ask the same question here is what
// keeps a new one from quietly answering it differently.
//
// Three questions get asked, and they are not the same question:
//
//   - mayReach*  — may this request see or act on the row? Curated rows are
//     public, so anyone the endpoint admits passes.
//   - isCuratedScenario — is this row curated at all? Stricter than mayReach:
//     it refuses the owner too, for the surfaces that exist only for the
//     public baseline.
//   - mayAuthorInScenario — may this caller add a child to it? Stricter again,
//     and the enforcement point for the ownership-uniformity invariant.

// mayReachOwnedRow is the shape mayReachScenario and mayReadRoute share: a row
// with no owner is curated and public; an owned one belongs to its owner or an
// admin. It takes the owner column rather than a row so the two callers can
// pass a Scenario's and a Route's without a type parameter buying anything.
//
// The identity is read rather than required, because the endpoints these guard
// are public: registering them behind RequireAuth would take the curated
// alignments and the ca-hsr baseline away from the anonymous callers they exist
// for.
func mayReachOwnedRow(ctx context.Context, ownerID *string) bool {
	if ownerID == nil {
		return true
	}
	user, ok := auth.UserFrom(ctx)
	return ok && auth.CanAccess(user, ownerID)
}

// mayReachScenario reports whether this request may act on sc.
//
// A curated scenario is reachable by anyone the endpoint itself admits: it is
// the platform's public data, and gating it on identity would take the ca-hsr
// baseline away from the anonymous callers it exists for. An owned scenario is
// reachable only by its owner or an admin.
//
// Note this governs *reaching* a scenario — compiling it, reading its graph,
// plotting over it. Mutating one is auth.CanAccess, which is stricter: it makes
// a curated scenario admin-only.
func mayReachScenario(ctx context.Context, sc transit.Scenario) bool {
	return mayReachOwnedRow(ctx, sc.OwnerID)
}

// mayReadRoute reports whether this request may see rt. A curated route is
// public and needs no identity; an owned one is visible only to its owner or an
// admin.
//
// The list read needs no equivalent — ListCuratedRouteSummaries filters in SQL
// — but a by-slug read would otherwise confirm a draft's existence to anyone
// who guessed its name-derived slug, which is the leak SPA-80 and SPA-81
// answered with 404s.
func mayReadRoute(ctx context.Context, rt transit.Route) bool {
	return mayReachOwnedRow(ctx, rt.OwnerID)
}

// isCuratedScenario reports whether sc is platform data rather than somebody's
// draft. Curated is the absence of an owner: migration 00021 left every
// pre-existing row NULL precisely so that "unowned" and "curated" would mean
// the same thing from then on.
//
// This is stricter than mayReachScenario, and deliberately so. mayReachScenario
// asks "may this caller reach it", which an owner passes for their own
// scenario; this asks "is it curated at all", which an owner fails for their
// own. The surfaces that need it — admin route ingestion, prerendered
// isochrones — exist only for the public baseline, so an owned scenario is not
// a place their content can hang, whoever is asking.
//
// Callers report the failure as not-found rather than forbidden, so neither
// endpoint can be used to probe which owned slugs exist.
func isCuratedScenario(sc transit.Scenario) bool {
	return sc.OwnerID == nil
}

// mayAuthorInScenario reports whether user may create a child row — a route, a
// station, a service — inside sc through the owner-scoped /api/me surface.
//
// It is auth.CanAccess plus one refusal: a curated scenario (OwnerID == nil) is
// never an authoring target here, admins included. CanAccess short-circuits on
// IsAdmin, so on its own it admits an admin to the curated ca-hsr baseline, and
// every /api/me create then stamps the row with an owner — leaving an owned
// child under a curated parent. That breaks the ownership-uniformity invariant,
// and because ListRoutesByScenario and ListServicesByScenario are deliberately
// unfiltered — the invariant is what makes that safe — the row would be pulled
// into LoadStore and published in the public compiled store.
//
// This forbids nothing an admin needs: authoring curated platform data is what
// the admin endpoints are for (POST /api/admin/routes), and they deliberately
// create unowned rows. It is the exact mirror of resolveCuratedScenarioOrFail,
// which refuses an *owned* parent on that side.
//
// A standalone route (no scenario named at all) is untouched by this: it has no
// parent to agree with, and carries its own owner — the invariant's sole stated
// exception.
func mayAuthorInScenario(user transit.User, sc transit.Scenario) bool {
	return sc.OwnerID != nil && auth.CanAccess(user, sc.OwnerID)
}

// childOwnership resolves the (scenario_id, owner_id) pair a child row must
// carry. A child inherits its parent scenario's owner rather than taking the
// caller's — that is the uniformity invariant stated as an assignment, and it
// is what stops an admin authoring into somebody else's scenario from leaving a
// row whose owner differs from its parent's. With no parent the row is
// standalone and keeps standaloneOwner: the caller on a create, the stored
// owner on an update.
func childOwnership(parent *transit.Scenario, standaloneOwner *string) (scenarioID, ownerID *string) {
	if parent == nil {
		return nil, standaloneOwner
	}
	return &parent.ID, parent.OwnerID
}
