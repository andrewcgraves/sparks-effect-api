package transit_test

import (
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

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

func TestMintStopSlugsToleratesNoStops(t *testing.T) {
	svc := transit.UserService{Slug: "line"}
	svc.MintStopSlugs()

	if len(svc.Stops) != 0 {
		t.Fatalf("minting invented %d stops", len(svc.Stops))
	}
}

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
