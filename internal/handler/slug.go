package handler

import (
	"context"
	"fmt"
)

// maxSlugAttempts bounds the collision-suffix search. Exhausting it means
// something is badly wrong (or a name is absurdly popular); either way the
// caller gets a 500 rather than an unbounded loop.
const maxSlugAttempts = 100

// mintUniqueSlug walks base, base-2, base-3, ... and returns the first
// candidate taken reports free.
//
// Every mint* helper in this package is this loop; they differ only in how they
// derive base (transit.Slugify for services, route.Slugify where a nameless
// name must become a 422 rather than the literal "service") and in which
// namespace they probe. So the derivation stays with the caller and the probe
// arrives as a closure — scenario-scoped for stations, whose slugs are unique
// per scenario rather than globally, and global for everything else. What is
// shared is what must not drift: the attempt bound and the message a caller
// turns into a 500.
//
// This is check-then-insert, so two concurrent creates of the same name can
// both see a slug free and the loser will fail the UNIQUE constraint with a
// 500. Acceptable at present scale; the constraint is what keeps it correct.
func mintUniqueSlug(ctx context.Context, base string, taken func(context.Context, string) (bool, error)) (string, error) {
	for attempt := 1; attempt <= maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		inUse, err := taken(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !inUse {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after %d attempts", base, maxSlugAttempts)
}
