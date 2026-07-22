package transit_test

import (
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// slugsOf reads the minted identities off a service, so a test can state the
// whole pattern in one comparison rather than one assertion per stop.
func slugsOf(svc transit.UserService) []string {
	out := make([]string, len(svc.Stops))
	for i, stop := range svc.Stops {
		out[i] = stop.Slug
	}
	return out
}

func TestMintStopSlugsNamespacesByService(t *testing.T) {
	svc := validUserService()
	svc.Slug = "bay-area-express"
	svc.MintStopSlugs()

	want := []string{"bay-area-express--san-francisco", "bay-area-express--san-jose"}
	for i, got := range slugsOf(svc) {
		if got != want[i] {
			t.Errorf("stop %d: got slug %q, want %q", i, got, want[i])
		}
	}
}

// Two services that each stop at a "Downtown" must not claim one identity —
// that is the whole reason the service slug is in the prefix.
func TestMintStopSlugsSeparatesTwoServicesWithTheSameStopName(t *testing.T) {
	stops := []transit.ServiceStopPoint{{Name: "Downtown"}, {Name: "Airport"}}

	a := transit.UserService{Slug: "line-a", Stops: append([]transit.ServiceStopPoint(nil), stops...)}
	b := transit.UserService{Slug: "line-b", Stops: append([]transit.ServiceStopPoint(nil), stops...)}
	a.MintStopSlugs()
	b.MintStopSlugs()

	for i := range stops {
		if a.Stops[i].Slug == b.Stops[i].Slug {
			t.Fatalf("stop %d: both services minted %q; identities must be per-service", i, a.Stops[i].Slug)
		}
	}
}

// Stop names are not unique within a service, so a repeat has to be
// disambiguated or two stops answer to one identity.
func TestMintStopSlugsDisambiguatesRepeatedNames(t *testing.T) {
	svc := transit.UserService{
		Slug: "loop",
		Stops: []transit.ServiceStopPoint{
			{Name: "Central"}, {Name: "North"}, {Name: "Central"}, {Name: "Central"},
		},
	}
	svc.MintStopSlugs()

	want := []string{"loop--central", "loop--north", "loop--central-2", "loop--central-3"}
	for i, got := range slugsOf(svc) {
		if got != want[i] {
			t.Errorf("stop %d: got slug %q, want %q", i, got, want[i])
		}
	}
}

// The slug is server-assigned identity. A client that posts one must not be
// able to make a stop answer to a name of its choosing — which is what would
// let it collide with another service's stop on purpose.
func TestMintStopSlugsOverwritesClientSuppliedSlugs(t *testing.T) {
	svc := transit.UserService{
		Slug: "line-a",
		Stops: []transit.ServiceStopPoint{
			{Name: "Downtown", Slug: "line-b--downtown"},
			{Name: "Airport", Slug: ""},
		},
	}
	svc.MintStopSlugs()

	if got := svc.Stops[0].Slug; got != "line-a--downtown" {
		t.Errorf("client-supplied slug survived: got %q, want %q", got, "line-a--downtown")
	}
	if got := svc.Stops[1].Slug; got != "line-a--airport" {
		t.Errorf("stop 1: got %q, want %q", got, "line-a--airport")
	}
}

// Re-minting an unchanged service must not drift — an update rewrites the whole
// aggregate, so a slug that grew a suffix on every save would rename stops for
// no reason.
func TestMintStopSlugsIsIdempotent(t *testing.T) {
	svc := transit.UserService{
		Slug:  "loop",
		Stops: []transit.ServiceStopPoint{{Name: "Central"}, {Name: "Central"}},
	}
	svc.MintStopSlugs()
	first := append([]string(nil), slugsOf(svc)...)
	svc.MintStopSlugs()

	for i, got := range slugsOf(svc) {
		if got != first[i] {
			t.Errorf("stop %d: re-minting changed %q to %q", i, first[i], got)
		}
	}
}

// The defect this whole ticket exists to avoid: a stop persisted under one
// identity and compiled under another. The compiler derives its keys from
// StopSlugs, so what is stored has to be the same list.
func TestMintStopSlugsMatchesWhatTheCompilerDerives(t *testing.T) {
	svc := transit.UserService{
		Slug: "bay-area-express",
		Stops: []transit.ServiceStopPoint{
			{Name: "San Francisco"}, {Name: "Central"}, {Name: "Central"}, {Name: "San Jose"},
		},
	}
	derived := transit.StopSlugs(svc)
	svc.MintStopSlugs()

	for i, got := range slugsOf(svc) {
		if got != derived[i] {
			t.Errorf("stop %d: stored %q but the compiler derives %q", i, got, derived[i])
		}
	}
}

// A slug is only useful as identity if it is one — the suffix rule has to hold
// even where two stops slugify to the same base from different display names.
func TestMintStopSlugsAreUniqueWithinAService(t *testing.T) {
	svc := transit.UserService{
		Slug: "line",
		Stops: []transit.ServiceStopPoint{
			{Name: "St. Paul"}, {Name: "St Paul"}, {Name: "st-paul"}, {Name: "  St.  Paul!  "},
		},
	}
	svc.MintStopSlugs()

	seen := map[string]int{}
	for i, slug := range slugsOf(svc) {
		if !strings.HasPrefix(slug, "line--") {
			t.Errorf("stop %d: slug %q is not namespaced by the service", i, slug)
		}
		if first, dup := seen[slug]; dup {
			t.Errorf("stops %d and %d share slug %q", first, i, slug)
		}
		seen[slug] = i
	}
}

// An empty service is not an error here — Validate is what refuses it — so
// minting must simply do nothing rather than panic on the way to that message.
func TestMintStopSlugsToleratesNoStops(t *testing.T) {
	svc := transit.UserService{Slug: "line"}
	svc.MintStopSlugs()

	if len(svc.Stops) != 0 {
		t.Fatalf("minting invented %d stops", len(svc.Stops))
	}
}
