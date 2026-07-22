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

// Two services cannot share a stop identity, and the collision suffix on a
// service slug is the case where that nearly fails: mintSlug appends "-2"
// *after* slugifying, so a maximum-length name yields an 82-character slug.
// Anything that re-slugified that would truncate the suffix back off and hand
// both services the same prefix.
func TestMintStopSlugsKeepsLongServiceSlugsDistinct(t *testing.T) {
	long := strings.Repeat("a", 80)

	first := transit.UserService{Slug: long, Stops: []transit.ServiceStopPoint{{Name: "Downtown"}}}
	second := transit.UserService{Slug: long + "-2", Stops: []transit.ServiceStopPoint{{Name: "Downtown"}}}
	first.MintStopSlugs()
	second.MintStopSlugs()

	if first.Stops[0].Slug == second.Stops[0].Slug {
		t.Fatalf("services %q and %q both minted stop identity %q",
			first.Slug, second.Slug, first.Stops[0].Slug)
	}
}
