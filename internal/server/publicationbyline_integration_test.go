package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Both public reads name the author by the display name they chose, date the
// publication, and never say who the author is in any other way.
func TestIntegration_PublicReadsCarryTheBylineAndNothingElseAboutTheAuthor(t *testing.T) {
	h, repo := integrationServer(t)
	admin := provisionAdminAndLogin(t, h, repo)
	const email = "byline-author@example.com"
	owner := provisionMember(t, h, admin, email, "owner-password")
	if rec := request(t, h, http.MethodPatch, "/api/auth/me", owner, `{"name": "Ada Lovelace"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/auth/me: status %d, body %s", rec.Code, rec.Body.String())
	}
	me := request(t, h, http.MethodGet, "/api/auth/me", owner)
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &user); err != nil || user.ID == "" {
		t.Fatalf("decode /api/auth/me: id=%q err=%v; body %s", user.ID, err, me.Body.String())
	}

	ingestCompileRoute(t, repo, "byline-route")
	slug, _ := publishedServiceOverAPI(t, h, owner, "byline-route", "Byline Line")

	detail := request(t, h, http.MethodGet, "/api/services/"+slug+"/publication", "")
	if detail.Code != http.StatusOK {
		t.Fatalf("GET publication: status %d, body %s", detail.Code, detail.Body.String())
	}
	var pub map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &pub); err != nil {
		t.Fatalf("decode publication: %v", err)
	}

	index := request(t, h, http.MethodGet, "/api/published-services", "")
	if index.Code != http.StatusOK {
		t.Fatalf("GET published-services: status %d, body %s", index.Code, index.Body.String())
	}
	var cards []map[string]any
	if err := json.Unmarshal(index.Body.Bytes(), &cards); err != nil {
		t.Fatalf("decode index: %v; body %s", err, index.Body.String())
	}
	if len(cards) != 1 {
		t.Fatalf("index = %v, want one card", cards)
	}

	for what, got := range map[string]map[string]any{"publication": pub, "card": cards[0]} {
		if got["author_name"] != "Ada Lovelace" {
			t.Errorf("%s author_name = %v, want %q", what, got["author_name"], "Ada Lovelace")
		}
		at, _ := got["published_at"].(string)
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			t.Errorf("%s published_at = %v, want an RFC 3339 time", what, got["published_at"])
		}
	}
	if cards[0]["published_at"] != pub["published_at"] {
		t.Errorf("card published_at = %v, want the publication's %v", cards[0]["published_at"], pub["published_at"])
	}

	for what, body := range map[string]string{"publication": detail.Body.String(), "index": index.Body.String()} {
		for _, leak := range []string{email, user.ID, "owner_id", "email"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s body contains %q: %s", what, leak, body)
			}
		}
	}
}
