package handler_test

import (
	"context"
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

type fakeHandoverStore struct {
	services  map[string]transit.UserService
	users     map[string]account.User
	handovers []transit.ServiceHandover
	offerTTL  time.Duration
}

func newFakeHandoverStore() *fakeHandoverStore {
	now := time.Now()
	disabled := now.Add(-time.Hour)
	return &fakeHandoverStore{
		services: map[string]transit.UserService{
			"line-a": {ID: "svc-1", Slug: "line-a", Name: "Line A", OwnerID: svcOwner.ID},
		},
		users: map[string]account.User{
			svcOwner.Email:     {ID: svcOwner.ID, Email: svcOwner.Email, Name: "Owner"},
			svcStranger.Email:  {ID: svcStranger.ID, Email: svcStranger.Email, Name: "Stranger"},
			"gone@example.com": {ID: "user-gone", Email: "gone@example.com", DisabledAt: &disabled},
		},
	}
}

func effectiveStatus(h transit.ServiceHandover) string {
	if h.Status == transit.HandoverPending && !time.Now().Before(h.ExpiresAt) {
		return transit.HandoverExpired
	}
	return h.Status
}

func (s *fakeHandoverStore) GetUserServiceBySlug(_ context.Context, slug string) (transit.UserService, bool, error) {
	svc, ok := s.services[slug]
	return svc, ok, nil
}

func (s *fakeHandoverStore) GetUserByEmail(_ context.Context, email string) (account.User, bool, error) {
	u, ok := s.users[email]
	return u, ok, nil
}

func (s *fakeHandoverStore) userByID(id string) account.User {
	for _, u := range s.users {
		if u.ID == id {
			return u
		}
	}
	return account.User{}
}

func (s *fakeHandoverStore) HasPendingServiceHandover(_ context.Context, serviceID string) (bool, error) {
	for _, h := range s.handovers {
		if h.UserServiceID == serviceID && effectiveStatus(h) == transit.HandoverPending {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeHandoverStore) OfferServiceHandover(ctx context.Context, h transit.ServiceHandover, ttl time.Duration) (transit.ServiceHandover, error) {
	if pending, _ := s.HasPendingServiceHandover(ctx, h.UserServiceID); pending {
		return transit.ServiceHandover{}, handler.ErrHandoverPending
	}
	s.offerTTL = ttl
	h.Status = transit.HandoverPending
	h.CreatedAt = time.Now()
	h.ExpiresAt = h.CreatedAt.Add(ttl)
	s.handovers = append(s.handovers, h)
	return h, nil
}

func (s *fakeHandoverStore) hydrate(h transit.ServiceHandover) transit.ServiceHandover {
	for _, svc := range s.services {
		if svc.ID == h.UserServiceID {
			h.ServiceSlug, h.ServiceName = svc.Slug, svc.Name
		}
	}
	h.FromName = s.userByID(h.FromUserID).Name
	h.ToName = s.userByID(h.ToUserID).Name
	h.Status = effectiveStatus(h)
	return h
}

func (s *fakeHandoverStore) ListPendingServiceHandovers(_ context.Context, userID string) ([]transit.ServiceHandover, error) {
	var out []transit.ServiceHandover
	for _, h := range s.handovers {
		if (h.FromUserID == userID || h.ToUserID == userID) && effectiveStatus(h) == transit.HandoverPending {
			out = append(out, s.hydrate(h))
		}
	}
	return out, nil
}

func (s *fakeHandoverStore) close(id, callerID string, sender bool, status string) (transit.ServiceHandover, error) {
	for i, h := range s.handovers {
		if h.ID != id {
			continue
		}
		party := h.ToUserID
		if sender {
			party = h.FromUserID
		}
		if party != callerID {
			return transit.ServiceHandover{}, handler.ErrHandoverNotFound
		}
		if effectiveStatus(h) != transit.HandoverPending {
			return transit.ServiceHandover{}, handler.ErrHandoverNotPending
		}
		now := time.Now()
		s.handovers[i].Status = status
		s.handovers[i].DecidedAt = &now
		return s.hydrate(s.handovers[i]), nil
	}
	return transit.ServiceHandover{}, handler.ErrHandoverNotFound
}

func (s *fakeHandoverStore) CancelServiceHandover(_ context.Context, id, fromUserID string) (transit.ServiceHandover, error) {
	return s.close(id, fromUserID, true, transit.HandoverCancelled)
}

func (s *fakeHandoverStore) DeclineServiceHandover(_ context.Context, id, toUserID string) (transit.ServiceHandover, error) {
	return s.close(id, toUserID, false, transit.HandoverDeclined)
}

func (s *fakeHandoverStore) seedPending(id, from, to string, expiresAt time.Time) {
	s.handovers = append(s.handovers, transit.ServiceHandover{
		ID: id, UserServiceID: "svc-1", FromUserID: from, ToUserID: to,
		Status: transit.HandoverPending, CreatedAt: expiresAt.Add(-handler.HandoverTTL), ExpiresAt: expiresAt,
	})
}

func handoverMux(store handler.HandoverStore) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /api/services/{slug}/handovers", handler.OfferHandover(store))
	mux.Handle("GET /api/me/handovers", handler.MyHandovers(store))
	mux.Handle("POST /api/handovers/{id}/cancel", handler.CancelHandover(store))
	mux.Handle("POST /api/handovers/{id}/decline", handler.DeclineHandover(store))
	return mux
}

func handoverAs(t *testing.T, store handler.HandoverStore, user account.User, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	handoverMux(store).ServeHTTP(rec, req)
	return rec
}

const handoverID = "00000000-0000-4000-8000-0000000000aa"

func TestOfferHandover_recordsAPendingOfferToARealAccount(t *testing.T) {
	store := newFakeHandoverStore()

	rec := handoverAs(t, store, svcOwner, http.MethodPost, "/api/services/line-a/handovers",
		`{"to_email":"  Stranger@Example.com "}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if len(store.handovers) != 1 {
		t.Fatalf("recorded %d handovers, want 1", len(store.handovers))
	}
	h := store.handovers[0]
	if h.UserServiceID != "svc-1" || h.FromUserID != svcOwner.ID || h.ToUserID != svcStranger.ID {
		t.Fatalf("recorded %+v, want svc-1 from owner to stranger", h)
	}
	if store.offerTTL != 14*24*time.Hour {
		t.Fatalf("ttl = %v, want 14 days", store.offerTTL)
	}
}

func TestOfferHandover_unknownAndDisabledEmailsAnswerExactlyAsARealOne(t *testing.T) {
	real := handoverAs(t, newFakeHandoverStore(), svcOwner, http.MethodPost,
		"/api/services/line-a/handovers", `{"to_email":"stranger@example.com"}`)

	for _, email := range []string{"nobody@example.com", "gone@example.com"} {
		store := newFakeHandoverStore()
		// The body echoes the normalised address, so the comparison uses the
		// same one the real offer was sent to.
		rec := handoverAs(t, store, svcOwner, http.MethodPost, "/api/services/line-a/handovers",
			`{"to_email":"`+email+`"}`)
		if rec.Code != real.Code {
			t.Fatalf("%s: status = %d, real account answered %d", email, rec.Code, real.Code)
		}
		want := strings.Replace(real.Body.String(), "stranger@example.com", email, 1)
		if rec.Body.String() != want {
			t.Fatalf("%s: body = %s, want %s", email, rec.Body.String(), want)
		}
		if len(store.handovers) != 0 {
			t.Fatalf("%s: recorded %+v, want nothing", email, store.handovers)
		}
	}
}

func TestOfferHandover_onlyTheOwnerOrAnAdminCanOffer(t *testing.T) {
	store := newFakeHandoverStore()
	rec := handoverAs(t, store, svcStranger, http.MethodPost, "/api/services/line-a/handovers",
		`{"to_email":"stranger@example.com"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger: status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if len(store.handovers) != 0 {
		t.Fatalf("stranger recorded %+v", store.handovers)
	}

	rec = handoverAs(t, store, svcAdmin, http.MethodPost, "/api/services/line-a/handovers",
		`{"to_email":"stranger@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admin: status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if len(store.handovers) != 1 || store.handovers[0].FromUserID != svcOwner.ID {
		t.Fatalf("admin offer recorded %+v, want one sent from the owner", store.handovers)
	}
}

func TestOfferHandover_strangerGets404WhateverTheBody(t *testing.T) {
	for _, body := range []string{``, `{}`, `not json`, `{"to_email":"stranger@example.com"}`} {
		rec := handoverAs(t, newFakeHandoverStore(), svcStranger, http.MethodPost, "/api/services/line-a/handovers", body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%q: status = %d, want 404", body, rec.Code)
		}
	}
}

func TestOfferHandover_unknownServiceIs404(t *testing.T) {
	rec := handoverAs(t, newFakeHandoverStore(), svcOwner, http.MethodPost, "/api/services/nope/handovers",
		`{"to_email":"stranger@example.com"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestOfferHandover_toTheOwnerIs422(t *testing.T) {
	for _, caller := range []account.User{svcOwner, svcAdmin} {
		store := newFakeHandoverStore()
		rec := handoverAs(t, store, caller, http.MethodPost, "/api/services/line-a/handovers",
			`{"to_email":"OWNER@example.com"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422; body %s", caller.Email, rec.Code, rec.Body.String())
		}
		if len(store.handovers) != 0 {
			t.Fatalf("%s: recorded %+v", caller.Email, store.handovers)
		}
	}
}

func TestOfferHandover_rejectsAMissingOrMalformedEmailBody(t *testing.T) {
	for _, body := range []string{`{}`, `{"to_email":"   "}`, `not json`} {
		rec := handoverAs(t, newFakeHandoverStore(), svcOwner, http.MethodPost, "/api/services/line-a/handovers", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestOfferHandover_secondPendingOfferIs409ForAnyAddress(t *testing.T) {
	for _, email := range []string{"stranger@example.com", "nobody@example.com", "gone@example.com"} {
		store := newFakeHandoverStore()
		store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))

		rec := handoverAs(t, store, svcOwner, http.MethodPost, "/api/services/line-a/handovers",
			`{"to_email":"`+email+`"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409; body %s", email, rec.Code, rec.Body.String())
		}
		if len(store.handovers) != 1 {
			t.Fatalf("%s: handovers = %+v, want only the seeded one", email, store.handovers)
		}
	}
}

func TestOfferHandover_anExpiredOfferDoesNotBlockANewOne(t *testing.T) {
	store := newFakeHandoverStore()
	store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(-time.Minute))

	rec := handoverAs(t, store, svcOwner, http.MethodPost, "/api/services/line-a/handovers",
		`{"to_email":"stranger@example.com"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	if len(store.handovers) != 2 {
		t.Fatalf("handovers = %+v, want the expired one plus a new one", store.handovers)
	}
}

func TestMyHandovers_splitsIncomingFromOutgoingAndNamesTheOtherParty(t *testing.T) {
	store := newFakeHandoverStore()
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, expires)
	store.seedPending("00000000-0000-4000-8000-0000000000bb", svcOwner.ID, svcStranger.ID, time.Now().Add(-time.Minute))

	rec := handoverAs(t, store, svcOwner, http.MethodGet, "/api/me/handovers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status = %d", rec.Code)
	}
	want := `{"incoming":[],"outgoing":[{"id":"` + handoverID + `","service_slug":"line-a","service_name":"Line A",` +
		`"to_name":"Stranger","status":"pending","created_at":"` + expires.Add(-handler.HandoverTTL).Format(time.RFC3339) +
		`","expires_at":"` + expires.Format(time.RFC3339) + `"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("owner body:\n got  %s\n want %s", got, want)
	}

	rec = handoverAs(t, store, svcStranger, http.MethodGet, "/api/me/handovers", "")
	if !strings.Contains(rec.Body.String(), `"incoming":[{"id":"`+handoverID+`"`) ||
		!strings.Contains(rec.Body.String(), `"from_name":"Owner"`) ||
		!strings.Contains(rec.Body.String(), `"outgoing":[]`) {
		t.Fatalf("recipient body = %s, want the one live offer as incoming from Owner", rec.Body.String())
	}

	rec = handoverAs(t, store, svcAdmin, http.MethodGet, "/api/me/handovers", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"incoming":[],"outgoing":[]}` {
		t.Fatalf("admin body = %s, want only their own (none)", got)
	}
}

func TestCancelHandover_onlyTheSender(t *testing.T) {
	for _, caller := range []account.User{svcStranger, svcAdmin} {
		store := newFakeHandoverStore()
		store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))
		rec := handoverAs(t, store, caller, http.MethodPost, "/api/handovers/"+handoverID+"/cancel", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", caller.Email, rec.Code)
		}
	}

	store := newFakeHandoverStore()
	store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))
	rec := handoverAs(t, store, svcOwner, http.MethodPost, "/api/handovers/"+handoverID+"/cancel", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sender: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"cancelled"`) || !strings.Contains(rec.Body.String(), `"decided_at"`) {
		t.Fatalf("body = %s, want the cancelled offer", rec.Body.String())
	}

	rec = handoverAs(t, store, svcOwner, http.MethodPost, "/api/handovers/"+handoverID+"/cancel", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second cancel: status = %d, want 409", rec.Code)
	}
}

func TestDeclineHandover_onlyTheRecipient(t *testing.T) {
	for _, caller := range []account.User{svcOwner, svcAdmin} {
		store := newFakeHandoverStore()
		store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))
		rec := handoverAs(t, store, caller, http.MethodPost, "/api/handovers/"+handoverID+"/decline", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", caller.Email, rec.Code)
		}
	}

	store := newFakeHandoverStore()
	store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(time.Hour))
	rec := handoverAs(t, store, svcStranger, http.MethodPost, "/api/handovers/"+handoverID+"/decline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("recipient: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"declined"`) || !strings.Contains(rec.Body.String(), `"from_name":"Owner"`) {
		t.Fatalf("body = %s, want the declined offer", rec.Body.String())
	}
}

func TestDecideHandover_anExpiredOfferCannotBeCancelledOrDeclined(t *testing.T) {
	for _, tc := range []struct {
		caller account.User
		action string
	}{{svcOwner, "cancel"}, {svcStranger, "decline"}} {
		store := newFakeHandoverStore()
		store.seedPending(handoverID, svcOwner.ID, svcStranger.ID, time.Now().Add(-time.Minute))
		rec := handoverAs(t, store, tc.caller, http.MethodPost, "/api/handovers/"+handoverID+"/"+tc.action, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409; body %s", tc.action, rec.Code, rec.Body.String())
		}
	}
}

func TestDecideHandover_unknownOrMalformedIdIs404(t *testing.T) {
	for _, id := range []string{"00000000-0000-4000-8000-000000000999", "not-a-uuid"} {
		for _, action := range []string{"cancel", "decline"} {
			rec := handoverAs(t, newFakeHandoverStore(), svcOwner, http.MethodPost, "/api/handovers/"+id+"/"+action, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s %s: status = %d, want 404", action, id, rec.Code)
			}
		}
	}
}
