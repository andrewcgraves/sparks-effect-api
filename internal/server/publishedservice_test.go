package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestPublishedServicesIsPublicAndTheSameForEveryCaller(t *testing.T) {
	deps := newStubDeps()
	deps.published = []transit.PublishedServiceSummary{
		{Slug: "coast-line", Name: "Coast Line", Subtext: "Electrified", Description: "Up the coast."},
	}
	h := newTestServer(t, deps)

	anon := request(t, h, http.MethodGet, "/api/published-services", "")
	if anon.Code != http.StatusOK {
		t.Fatalf("anonymous status = %d, want 200; body %s", anon.Code, anon.Body.String())
	}
	var got []transit.PublishedServiceSummary
	if err := json.Unmarshal(anon.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0] != deps.published[0] {
		t.Fatalf("index = %+v, want %+v", got, deps.published)
	}

	// A signed-in caller gets byte-for-byte what an anonymous one does: the
	// response depends on nothing about who asks.
	for _, token := range []string{userToken, adminToken} {
		rec := request(t, h, http.MethodGet, "/api/published-services", token)
		if rec.Code != http.StatusOK || rec.Body.String() != anon.Body.String() {
			t.Fatalf("with a session: status %d body %s, want 200 and %s", rec.Code, rec.Body.String(), anon.Body.String())
		}
	}
}

func TestPublishedServicesListsNothingAsAnEmptyArray(t *testing.T) {
	h := newTestServer(t, newStubDeps())

	rec := request(t, h, http.MethodGet, "/api/published-services", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "[]\n" {
		t.Fatalf("body = %q, want an empty JSON array", body)
	}
}
