package auth

import "github.com/andrewcgraves/sparks-effect-api/internal/transit"

// CanAccess reports whether user may read or mutate a resource owned by
// ownerID. It is the single server-side ownership rule; handlers must consult
// it rather than trusting any owner_id supplied by the client.
//
// Admins reach everything. Otherwise a user reaches only rows they own.
// A nil ownerID means the row is unowned — the seeded, curated platform data
// (e.g. the ca-hsr baseline) — which is admin-only, so no account can quietly
// rewrite shared scenarios.
//
// Note this governs *owned* resources: it gates the owner-scoped reads and the
// mutating paths. It is not consulted by the public GET endpoints, which today
// serve only unowned curated data because that is all the seed produces.
//
// SPA-80 (UserService) and SPA-81 (UserScenario) both resolved the risk this
// comment used to warn about — a user-authored row leaking into the public
// compiled store — by keeping user-authored content in its own type and table,
// entirely outside the seeded Scenario/Service pair that GET /api/scenarios
// compiles and serves.
//
// Ownership on the seeded models themselves cannot buy safety that cheaply,
// since owned and curated rows now share tables. Two things hold the line
// instead. First, the ownership-uniformity invariant: a scenario and all of its
// children share one owner, so scoping a read to a scenario has already scoped
// it to that scenario's owner. Second, Repository.ListCuratedScenarios and
// ListCuratedRouteSummaries filter on owner_id IS NULL — the boot compile
// (LoadStore, CompileSeededIfNeeded) and the public route picker read those and
// nothing else, so no owned row reaches the compiled store or the
// unauthenticated surface. The invariant is enforced by the create and update
// handlers, which refuse to attach an owned child to a scenario the caller does
// not own.
func CanAccess(user transit.User, ownerID *string) bool {
	if user.IsAdmin {
		return true
	}
	if ownerID == nil {
		return false
	}
	// Guard against a zero-valued user matching a blank owner column: without
	// this, an unidentified caller would "own" every row with owner_id = ''.
	if user.ID == "" {
		return false
	}
	return *ownerID == user.ID
}

// CanReference reports whether user may build owned content on top of a row
// owned by ownerID — pointing a service at a route, or authoring inside a
// scenario.
//
// Deliberately not CanAccess. The two ask different questions about the same
// nil owner: an unowned row is curated platform data, which is a public
// building block anyone may reference, but admin-only to *mutate*. Reusing
// CanAccess here would make the curated ca-hsr alignments unreferenceable by
// everyone but admins, which is the opposite of what curated data is for.
func CanReference(user transit.User, ownerID *string) bool {
	return ownerID == nil || CanAccess(user, ownerID)
}
