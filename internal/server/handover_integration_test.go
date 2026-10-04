package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type handoverList struct {
	Incoming []struct {
		ID          string `json:"id"`
		ServiceSlug string `json:"service_slug"`
		Status      string `json:"status"`
	} `json:"incoming"`
	Outgoing []struct {
		ID          string `json:"id"`
		ServiceSlug string `json:"service_slug"`
		Status      string `json:"status"`
	} `json:"outgoing"`
}

func listHandovers(t *testing.T, h http.Handler, token string) handoverList {
	t.Helper()
	rec := request(t, h, http.MethodGet, "/api/me/handovers", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me/handovers: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out handoverList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode handovers: %v", err)
	}
	return out
}

func TestIntegration_HandoverOfferListCancelDecline(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	owner := provisionMember(t, h, adminToken, "owner@example.com", "owner-password-1")
	recipient := provisionMember(t, h, adminToken, "recipient@example.com", "recipient-password-1")
	stranger := provisionMember(t, h, adminToken, "stranger@example.com", "stranger-password-1")

	if err := repo.CreateRoute(ctx, transit.Route{
		ID: mustUUID(t), Slug: "handover-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svc, found, err := repo.GetUserServiceByID(ctx, createUserServiceOverAPI(t, h, owner, "handover-route", "Handed Over"))
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID: found=%v err=%v", found, err)
	}
	slug := svc.Slug
	offerPath := "/api/services/" + slug + "/handovers"

	// Enumeration: a real account and an unknown address answer alike.
	real := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`)
	if real.Code != http.StatusAccepted {
		t.Fatalf("offer: status %d, body %s", real.Code, real.Body.String())
	}
	if rec := request(t, h, http.MethodPost, offerPath, stranger, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger offer: status %d, want 404", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second pending offer: status %d, want 409", rec.Code)
	}

	out := listHandovers(t, h, owner)
	if len(out.Outgoing) != 1 || len(out.Incoming) != 0 || out.Outgoing[0].ServiceSlug != slug {
		t.Fatalf("owner list = %+v", out)
	}
	id := out.Outgoing[0].ID
	in := listHandovers(t, h, recipient)
	if len(in.Incoming) != 1 || in.Incoming[0].ID != id {
		t.Fatalf("recipient list = %+v", in)
	}

	for _, tc := range []struct{ token, action string }{
		{stranger, "cancel"}, {stranger, "decline"}, {adminToken, "cancel"}, {recipient, "cancel"}, {owner, "decline"},
	} {
		if rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/"+tc.action, tc.token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s by a non-party: status %d, want 404", tc.action, rec.Code)
		}
	}

	rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/decline", recipient)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"declined"`) {
		t.Fatalf("decline: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, h, http.MethodPost, "/api/handovers/"+id+"/cancel", owner); rec.Code != http.StatusConflict {
		t.Fatalf("cancel after decline: status %d, want 409", rec.Code)
	}

	unknown := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"nobody@example.com"}`)
	if unknown.Code != real.Code ||
		unknown.Body.String() != strings.Replace(real.Body.String(), "recipient@example.com", "nobody@example.com", 1) {
		t.Fatalf("unknown address: status %d body %s; real account: status %d body %s",
			unknown.Code, unknown.Body.String(), real.Code, real.Body.String())
	}
	if out := listHandovers(t, h, owner); len(out.Outgoing) != 0 {
		t.Fatalf("an offer to an unknown address was recorded: %+v", out)
	}

	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"owner@example.com"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("offer to self: status %d, want 422", rec.Code)
	}

	if rec := request(t, h, http.MethodPost, offerPath, owner, `{"to_email":"recipient@example.com"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("re-offer after decline: status %d, body %s", rec.Code, rec.Body.String())
	}
	id = listHandovers(t, h, owner).Outgoing[0].ID
	rec = request(t, h, http.MethodPost, "/api/handovers/"+id+"/cancel", owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: status %d, body %s", rec.Code, rec.Body.String())
	}
	if in := listHandovers(t, h, recipient); len(in.Incoming) != 0 {
		t.Fatalf("recipient still sees a cancelled offer: %+v", in)
	}
}
