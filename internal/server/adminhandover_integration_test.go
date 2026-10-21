package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestIntegration_AdminMovesAServiceToANamedOwnerWithoutAnOffer(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminToken := provisionAdminAndLogin(t, h, repo)
	ownerToken := provisionMember(t, h, adminToken, "owner@example.com", "owner-password")
	recipientToken := provisionMember(t, h, adminToken, "recipient@example.com", "recipient-password")
	admin, _, _ := repo.GetUserByEmail(ctx, "admin@example.com")
	owner, _, _ := repo.GetUserByEmail(ctx, "owner@example.com")
	recipient, _, _ := repo.GetUserByEmail(ctx, "recipient@example.com")

	if err := repo.CreateRoute(ctx, transit.Route{
		ID: mustUUID(t), Slug: "admin-handover-route", Name: "Alignment", Mode: "rail", Bidirectional: true,
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	serviceID := createUserServiceOverAPI(t, h, ownerToken, "admin-handover-route", "Owner Line")
	svc, _, err := repo.GetUserServiceByID(ctx, serviceID)
	if err != nil {
		t.Fatalf("GetUserServiceByID: %v", err)
	}
	path := "/api/admin/services/" + svc.Slug + "/owner"
	body := `{"user_id":"` + recipient.ID + `"}`

	// A pending offer to a third party, to be closed by the move.
	if rec := request(t, h, http.MethodPost, "/api/services/"+svc.Slug+"/handovers", ownerToken,
		`{"to_email":"admin@example.com"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("offer: status %d, body %s", rec.Code, rec.Body.String())
	}

	if rec := request(t, h, http.MethodPut, path, ownerToken, body); rec.Code != http.StatusForbidden {
		t.Fatalf("owner on the admin route: status %d, want 403", rec.Code)
	}

	rec := request(t, h, http.MethodPut, path, adminToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin move: status %d, body %s", rec.Code, rec.Body.String())
	}
	var moved struct {
		Status     string  `json:"status"`
		FromUserID string  `json:"from_user_id"`
		ToUserID   string  `json:"to_user_id"`
		ForcedBy   *string `json:"forced_by"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&moved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if moved.Status != "accepted" || moved.FromUserID != owner.ID || moved.ToUserID != recipient.ID ||
		moved.ForcedBy == nil || *moved.ForcedBy != admin.ID {
		t.Fatalf("moved = %+v, want accepted from the owner to the recipient, forced by the admin", moved)
	}

	// The recipient now owns it and the old owner is a stranger to it.
	if rec := request(t, h, http.MethodGet, "/api/services/"+svc.Slug, recipientToken); rec.Code != http.StatusOK {
		t.Errorf("recipient read: status %d, want 200", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/api/services/"+svc.Slug, ownerToken); rec.Code != http.StatusNotFound {
		t.Errorf("old owner read: status %d, want 404", rec.Code)
	}
	// The pending offer is gone from both parties' lists.
	for name, token := range map[string]string{"sender": ownerToken, "offer recipient": adminToken} {
		rec := request(t, h, http.MethodGet, "/api/me/handovers", token)
		if rec.Code != http.StatusOK || rec.Body.String() != "{\"incoming\":[],\"outgoing\":[]}\n" {
			t.Errorf("%s list after the move: status %d body %s, want empty", name, rec.Code, rec.Body.String())
		}
	}
	// Moving it to the account that already owns it is refused.
	if rec := request(t, h, http.MethodPut, path, adminToken, body); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("move to the current owner: status %d, want 422", rec.Code)
	}
}
