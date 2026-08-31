package handler

import (
	"context"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Ownership predicates shared by the handlers that reach a seeded scenario or
// route by slug.
//
// They exist because owned and curated rows share tables. The collection reads
// filter in SQL (Repository.ListCuratedScenarios, ListCuratedRouteSummaries),
// but a by-slug read cannot: slugs are globally unique across both kinds, so
// minting one has to see the whole namespace. That leaves the by-slug paths to
// decide for themselves, and having them all ask the same question here is what
// keeps a new one from quietly answering it differently.

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
	if sc.OwnerID == nil {
		return true
	}
	user, ok := auth.UserFrom(ctx)
	return ok && auth.CanAccess(user, sc.OwnerID)
}
