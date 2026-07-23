package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// This file exercises the SPA-74 acceptance criteria end-to-end against a real
// database and the real mux: an admin-provisioned account logs in, its token
// works on protected routes, admin gating holds, and ownership scoping is
// enforced server-side.

func integrationServer(t *testing.T) (http.Handler, *postgres.Repo) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL (or DATABASE_URL) must be set for integration tests in CI")
		}
		t.Skip("set TEST_DATABASE_URL to run auth integration tests (see `make db-up`)")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("reset connect: %v", err)
	}
	if _, err := conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	_ = conn.Close(ctx)

	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo, err := postgres.Connect(ctx, url, 0)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(repo.Close)

	store, err := transit.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	chainer := isochrone.New(&stadia.FakeClient{}, store, logger.Discard())
	cfg := config.Config{Port: "8080", SessionTTL: time.Hour}
	return New(cfg, store, repo, chainer, &stadia.FakeClient{}, logger.Discard()).Handler, repo
}

// provisionAdmin stands in for the bootstrap-admin path in main.
func provisionAdmin(t *testing.T, repo *postgres.Repo, email, password string) string {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	id, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	if err := repo.CreateUser(context.Background(), transit.User{
		ID: id, Email: email, Name: "Admin", IsAdmin: true,
	}, hash); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

func login(t *testing.T, h http.Handler, email, password string) (token string, status int) {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		return "", rec.Code
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return resp.Token, rec.Code
}

// AC1 + AC2: an admin-provisioned account logs in and its token opens the
// protected routes; a wrong password does not.
func TestIntegration_ProvisionedAccountLogsInAndUsesItsToken(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")

	if _, status := login(t, h, "admin@example.com", "wrong-password"); status != http.StatusUnauthorized {
		t.Errorf("bad password: status %d, want 401", status)
	}

	token, status := login(t, h, "admin@example.com", "admin-password")
	if status != http.StatusOK {
		t.Fatalf("login: status %d, want 200", status)
	}

	rec := request(t, h, http.MethodGet, "/api/auth/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/auth/me: status %d, want 200", rec.Code)
	}
	var me transit.User
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.Email != "admin@example.com" || !me.IsAdmin {
		t.Errorf("/api/auth/me returned %+v", me)
	}

	// Logout revokes the token for good.
	if rec := request(t, h, http.MethodPost, "/api/auth/logout", token); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status %d, want 204", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/api/auth/me", token); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still works: status %d, want 401", rec.Code)
	}
}

// AC1 + AC3: an admin provisions a second account over the API; that account
// can log in, but cannot itself reach the admin-gated endpoint. There is no
// public path by which it could have created itself.
func TestIntegration_AdminProvisioningAndGating(t *testing.T) {
	h, repo := integrationServer(t)
	provisionAdmin(t, repo, "admin@example.com", "admin-password")
	adminToken, _ := login(t, h, "admin@example.com", "admin-password")

	body := `{"email":"member@example.com","name":"Member","password":"member-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin provisioning: status %d, want 201; body %s", rec.Code, rec.Body.String())
	}

	memberToken, status := login(t, h, "member@example.com", "member-password")
	if status != http.StatusOK {
		t.Fatalf("provisioned member could not log in: status %d", status)
	}

	// The member must not be able to provision further accounts — this is the
	// gate that keeps the system invite-only rather than transitively open.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users",
		strings.NewReader(`{"email":"sneaky@example.com","password":"pw"}`))
	req.Header.Set("Authorization", "Bearer "+memberToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reached the admin endpoint: status %d, want 403", rec.Code)
	}
	if _, _, found, err := repo.GetUserCredentialsByEmail(context.Background(), "sneaky@example.com"); found || err != nil {
		t.Error("a non-admin managed to provision an account")
	}
}

// AC4 (read half): each user sees only the scenarios they own, enforced by the
// server from the token's identity — not by anything the client sends.
func TestIntegration_OwnershipScopingIsEnforcedServerSide(t *testing.T) {
	h, repo := integrationServer(t)
	ctx := context.Background()
	adminID := provisionAdmin(t, repo, "admin@example.com", "admin-password")

	// Two members, each with one scenario, plus an unowned curated scenario.
	adminToken, _ := login(t, h, "admin@example.com", "admin-password")
	memberIDs := map[string]string{}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users",
			strings.NewReader(`{"email":"`+email+`","password":"pw"}`))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("provisioning %s: status %d", email, rec.Code)
		}
		var u transit.User
		if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
			t.Fatalf("decode: %v", err)
		}
		memberIDs[email] = u.ID
	}

	idA, idB := memberIDs["a@example.com"], memberIDs["b@example.com"]
	for _, sc := range []transit.Scenario{
		{ID: mustUUID(t), Slug: "a-net", Name: "A Net", OwnerID: &idA},
		{ID: mustUUID(t), Slug: "b-net", Name: "B Net", OwnerID: &idB},
		{ID: mustUUID(t), Slug: "curated", Name: "Curated"},
		{ID: mustUUID(t), Slug: "admin-net", Name: "Admin Net", OwnerID: &adminID},
	} {
		if err := repo.CreateScenario(ctx, sc); err != nil {
			t.Fatalf("CreateScenario %s: %v", sc.Slug, err)
		}
	}

	tokenA, _ := login(t, h, "a@example.com", "pw")
	tokenB, _ := login(t, h, "b@example.com", "pw")

	assertOwnScenarios := func(token, wantSlug string) {
		t.Helper()
		rec := request(t, h, http.MethodGet, "/api/me/scenarios", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("/api/me/scenarios: status %d", rec.Code)
		}
		var got []transit.Scenario
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].Slug != wantSlug {
			t.Errorf("got %+v, want only %s", got, wantSlug)
		}
	}

	assertOwnScenarios(tokenA, "a-net")
	assertOwnScenarios(tokenB, "b-net")
	// Being an admin does not turn "my scenarios" into "all scenarios".
	assertOwnScenarios(adminToken, "admin-net")
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := ids.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	return id
}
