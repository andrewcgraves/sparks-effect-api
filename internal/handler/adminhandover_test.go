package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// Real uuids, unlike svcOwner and friends: the handler refuses a user_id that
// could not name a row before it reaches the store.
var (
	forcedRecipient = account.User{ID: "00000000-0000-4000-8000-0000000000b1", Email: "recipient@example.com", Name: "Recipient"}
	forcedDisabled  = account.User{ID: "00000000-0000-4000-8000-0000000000b2", Email: "disabled@example.com", Name: "Disabled"}
	forcedUnknownID = "00000000-0000-4000-8000-0000000000b3"
)

func newFakeAdminHandoverStore() *fakeHandoverStore {
	s := newFakeHandoverStore()
	disabled := time.Now().Add(-time.Hour)
	s.users[forcedRecipient.Email] = forcedRecipient
	gone := forcedDisabled
	gone.DisabledAt = &disabled
	s.users[forcedDisabled.Email] = gone
	s.users[svcAdmin.Email] = account.User{ID: svcAdmin.ID, Email: svcAdmin.Email, Name: "Admin", IsAdmin: true}
	return s
}

func (s *fakeHandoverStore) GetUserByID(_ context.Context, id string) (account.User, bool, error) {
	for _, u := range s.users {
		if u.ID == id {
			return u, true, nil
		}
	}
	return account.User{}, false, nil
}

func (s *fakeHandoverStore) TransferUserService(_ context.Context, serviceID, toUserID, forcedBy string) (transit.ServiceHandover, error) {
	for slug, svc := range s.services {
		if svc.ID != serviceID {
			continue
		}
		if svc.OwnerID == toUserID {
			return transit.ServiceHandover{}, handler.ErrHandoverToOwner
		}
		if s.userByID(toUserID).DisabledAt != nil {
			return transit.ServiceHandover{}, handler.ErrHandoverRecipientDisabled
		}
		if len(s.scenarioSlugs) > 0 {
			return transit.ServiceHandover{}, &handler.ServiceInScenariosError{Slugs: s.scenarioSlugs}
		}
		now := time.Now()
		for i := range s.handovers {
			if s.handovers[i].UserServiceID == serviceID && effectiveStatus(s.handovers[i]) == transit.HandoverPending {
				s.handovers[i].Status = transit.HandoverCancelled
				s.handovers[i].DecidedAt = &now
			}
		}
		h := transit.ServiceHandover{
			ID: "forced-1", UserServiceID: serviceID, FromUserID: svc.OwnerID, ToUserID: toUserID,
			Status: transit.HandoverAccepted, CreatedAt: now, DecidedAt: &now, ExpiresAt: now, ForcedBy: &forcedBy,
		}
		svc.OwnerID = toUserID
		s.services[slug] = svc
		s.handovers = append(s.handovers, h)
		return s.hydrate(h), nil
	}
	return transit.ServiceHandover{}, errors.New("no such service")
}

func forceHandoverAs(t *testing.T, store handler.AdminHandoverStore, user account.User, slug, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("PUT /api/admin/services/{slug}/owner", handler.ForceHandover(store))
	req := httptest.NewRequest(http.MethodPut, "/api/admin/services/"+slug+"/owner", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestForceHandover_movesTheServiceCancelsThePendingOfferAndRecordsTheAdmin(t *testing.T) {
	store := newFakeAdminHandoverStore()
	store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))

	rec := forceHandoverAs(t, store, svcAdmin, "line-a", `{"user_id":"`+forcedRecipient.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"status":"accepted"`, `"service_slug":"line-a"`, `"service_name":"Line A"`,
		`"from_user_id":"` + svcOwner.ID + `"`, `"from_name":"Owner"`,
		`"to_user_id":"` + forcedRecipient.ID + `"`, `"to_name":"Recipient"`,
		`"forced_by":"` + svcAdmin.ID + `"`, `"decided_at"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want it to contain %s", body, want)
		}
	}
	if store.services["line-a"].OwnerID != forcedRecipient.ID {
		t.Fatalf("owner = %s, want the named user", store.services["line-a"].OwnerID)
	}
	if store.handovers[0].Status != transit.HandoverCancelled || store.handovers[0].DecidedAt == nil {
		t.Fatalf("pending offer = %+v, want it cancelled so the recipient cannot accept a service that has moved", store.handovers[0])
	}
}

func TestForceHandover_refusesAUserWhoCouldNotOwnIt(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"disabled account": {`{"user_id":"` + forcedDisabled.ID + `"}`, http.StatusUnprocessableEntity},
		"unknown account":  {`{"user_id":"` + forcedUnknownID + `"}`, http.StatusUnprocessableEntity},
		"not a uuid":       {`{"user_id":"user-2"}`, http.StatusUnprocessableEntity},
		"current owner":    {`{"user_id":"` + forcedRecipient.ID + `"}`, http.StatusUnprocessableEntity},
		"missing user_id":  {`{}`, http.StatusBadRequest},
		"malformed body":   {`{"user_id":`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeAdminHandoverStore()
			// svcOwner's id is not a uuid, so the owner case gives the service
			// a uuid-shaped owner and names that same account.
			if name == "current owner" {
				svc := store.services["line-a"]
				svc.OwnerID = forcedRecipient.ID
				store.services["line-a"] = svc
			}
			rec := forceHandoverAs(t, store, svcAdmin, "line-a", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if n := len(store.handovers); n != 0 {
				t.Fatalf("%d handover rows recorded, want none", n)
			}
		})
	}
}

func TestForceHandover_unknownServiceIs404(t *testing.T) {
	rec := forceHandoverAs(t, newFakeAdminHandoverStore(), svcAdmin, "nope", `{"user_id":"`+forcedRecipient.ID+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestForceHandover_aServiceInAScenarioIs409WithTheSlugs(t *testing.T) {
	store := newFakeAdminHandoverStore()
	store.scenarioSlugs = []string{"commute"}
	rec := forceHandoverAs(t, store, svcAdmin, "line-a", `{"user_id":"`+forcedRecipient.ID+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	want := `{"error":"the service is still in the sender's scenarios: commute",` +
		`"code":"service_in_scenarios","detail":{"scenarios":["commute"]}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body:\n got  %s\n want %s", got, want)
	}
	if store.services["line-a"].OwnerID != svcOwner.ID {
		t.Fatalf("the service moved despite the refusal")
	}
}
