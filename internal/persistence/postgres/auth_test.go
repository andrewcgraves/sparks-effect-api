package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// execSQL runs a statement outside the repository, for asserting on behaviour
// the schema owns (here, the ON DELETE CASCADE from users to sessions) rather
// than behaviour the Go code implements.
func execSQL(t *testing.T, url, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("execSQL connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("execSQL %q: %v", sql, err)
	}
}

const (
	ownerAID = "00000000-0000-4009-8003-00000000000a"
	ownerBID = "00000000-0000-4009-8003-00000000000b"
)

func mustCreateUser(t *testing.T, repo interface {
	CreateUser(context.Context, transit.User, string) error
}, u transit.User, password string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := repo.CreateUser(context.Background(), u, hash); err != nil {
		t.Fatalf("CreateUser %s: %v", u.Email, err)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := transit.User{ID: ownerAID, Email: "owner@example.com", Name: "Owner"}
	mustCreateUser(t, repo, u, "s3cret-password")

	got, hash, ok, err := repo.GetUserCredentialsByEmail(ctx, u.Email)
	if err != nil || !ok {
		t.Fatalf("GetUserCredentialsByEmail: ok=%v err=%v", ok, err)
	}
	if got.ID != u.ID {
		t.Errorf("user id: want %s, got %s", u.ID, got.ID)
	}
	if hash == "s3cret-password" {
		t.Fatal("password was stored in plaintext")
	}
	if !auth.VerifyPassword(hash, "s3cret-password") {
		t.Error("stored hash does not verify the original password")
	}

	if _, _, ok, err := repo.GetUserCredentialsByEmail(ctx, "nobody@example.com"); ok || err != nil {
		t.Errorf("unknown email: ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := transit.User{ID: ownerAID, Email: "owner@example.com", IsAdmin: true}
	mustCreateUser(t, repo, u, "pw")

	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, transit.Session{
		TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// A presented token resolves to its user via the hash.
	got, ok, err := repo.GetSessionUser(ctx, auth.HashToken(token))
	if err != nil || !ok {
		t.Fatalf("GetSessionUser: ok=%v err=%v", ok, err)
	}
	if got.ID != u.ID || !got.IsAdmin {
		t.Errorf("GetSessionUser returned %+v, want %s (admin)", got, u.ID)
	}

	// Logout revokes it.
	if err := repo.DeleteSession(ctx, hash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, ok, err := repo.GetSessionUser(ctx, hash); ok || err != nil {
		t.Errorf("after logout: ok=%v err=%v, want false/nil", ok, err)
	}
}

// An expired session must not authenticate, and must be prunable.
func TestExpiredSessionIsRejectedAndPruned(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := transit.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")

	_, expiredHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, transit.Session{
		TokenHash: expiredHash, UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_, liveHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, transit.Session{
		TokenHash: liveHash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, ok, err := repo.GetSessionUser(ctx, expiredHash); ok || err != nil {
		t.Errorf("expired session: ok=%v err=%v, want false/nil", ok, err)
	}

	n, err := repo.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d sessions, want 1", n)
	}
	// Pruning must not touch live sessions.
	if _, ok, err := repo.GetSessionUser(ctx, liveHash); !ok || err != nil {
		t.Errorf("live session after prune: ok=%v err=%v, want true/nil", ok, err)
	}
}

// Deprovisioning a user must revoke their sessions, via the FK cascade rather
// than application cleanup code.
func TestDeletingUserCascadesToSessions(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	u := transit.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")

	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, transit.Session{
		TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	execSQL(t, url, `DELETE FROM users WHERE id = $1`, u.ID)

	if _, ok, err := repo.GetSessionUser(ctx, hash); ok || err != nil {
		t.Errorf("session survived user deletion: ok=%v err=%v", ok, err)
	}
}

// Owner-scoped reads are the read half of "a user sees only what they own".
func TestOwnerScopedReads(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	mustCreateUser(t, repo, transit.User{ID: ownerAID, Email: "a@example.com"}, "pw")
	mustCreateUser(t, repo, transit.User{ID: ownerBID, Email: "b@example.com"}, "pw")

	const (
		scenarioA = "00000000-0000-4001-8003-000000000001"
		scenarioB = "00000000-0000-4001-8003-000000000002"
		scenarioU = "00000000-0000-4001-8003-000000000003"
		routeA    = "00000000-0000-4002-8003-000000000001"
		vehicleID = "00000000-0000-4003-8003-000000000001"
		stationA  = "00000000-0000-4005-8003-000000000001"
		serviceA  = "00000000-0000-4004-8003-000000000001"
		serviceB  = "00000000-0000-4004-8003-000000000002"
	)

	ownerA, ownerB := ownerAID, ownerBID
	for _, sc := range []transit.Scenario{
		{ID: scenarioA, Slug: "a-net", Name: "A Net", OwnerID: &ownerA},
		{ID: scenarioB, Slug: "b-net", Name: "B Net", OwnerID: &ownerB},
		{ID: scenarioU, Slug: "curated", Name: "Curated"}, // unowned platform data
	} {
		if err := repo.CreateScenario(ctx, sc); err != nil {
			t.Fatalf("CreateScenario %s: %v", sc.Slug, err)
		}
	}

	if err := repo.CreateVehicleType(ctx, transit.VehicleType{
		ID: vehicleID, Name: "EMU", MaxSpeedKMH: 200, AccelerationMS2: 0.5, DecelerationMS2: 0.6,
		DwellLevelS: 30, DwellStepS: 60,
	}); err != nil {
		t.Fatalf("CreateVehicleType: %v", err)
	}
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeA, ScenarioID: ptr(scenarioA), Slug: "main-a", Name: "Main", Mode: "rail",
		Geometry: transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if err := repo.CreateStation(ctx, transit.Station{
		ID: stationA, ScenarioID: scenarioA, Slug: "a", Name: "A",
		Location: transit.GeoPoint{Type: "Point", Coordinates: []float64{-122, 37}},
	}); err != nil {
		t.Fatalf("CreateStation: %v", err)
	}

	for _, svc := range []transit.Service{
		{ID: serviceA, ScenarioID: scenarioA, RouteID: routeA, VehicleTypeID: vehicleID,
			Name: "A Service", Active: true, OwnerID: &ownerA,
			Stops: []transit.ServiceStop{{StationID: stationA, Sequence: 1}}},
		{ID: serviceB, ScenarioID: scenarioA, RouteID: routeA, VehicleTypeID: vehicleID,
			Name: "B Service", Active: true, OwnerID: &ownerB},
	} {
		if err := repo.CreateService(ctx, svc); err != nil {
			t.Fatalf("CreateService %s: %v", svc.Name, err)
		}
	}

	scenarios, err := repo.ListScenariosByOwner(ctx, ownerAID)
	if err != nil {
		t.Fatalf("ListScenariosByOwner: %v", err)
	}
	if len(scenarios) != 1 || scenarios[0].ID != scenarioA {
		t.Errorf("owner A scenarios = %+v, want only %s", scenarios, scenarioA)
	}

	services, err := repo.ListServicesByOwner(ctx, ownerAID)
	if err != nil {
		t.Fatalf("ListServicesByOwner: %v", err)
	}
	if len(services) != 1 || services[0].ID != serviceA {
		t.Fatalf("owner A services = %+v, want only %s", services, serviceA)
	}
	// The owner-scoped read must hydrate the same aggregate shape as the
	// scenario-scoped one, not a stripped-down row.
	if len(services[0].Stops) != 1 {
		t.Errorf("owner-scoped service stops = %d, want 1 (aggregate not hydrated)", len(services[0].Stops))
	}

	// A user with nothing of their own sees an empty list, never someone
	// else's rows and never the unowned curated scenario.
	empty, err := repo.ListScenariosByOwner(ctx, "00000000-0000-4009-8003-0000000000ff")
	if err != nil {
		t.Fatalf("ListScenariosByOwner (no rows): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("unknown owner saw %d scenarios, want 0", len(empty))
	}
}
