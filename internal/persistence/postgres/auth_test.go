package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

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

func queryInt(t *testing.T, url, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("queryInt connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("queryInt %q: %v", sql, err)
	}
	return n
}

const (
	ownerAID = "00000000-0000-4009-8003-00000000000a"
	ownerBID = "00000000-0000-4009-8003-00000000000b"
)

func mustCreateUser(t *testing.T, repo interface {
	CreateUser(context.Context, account.User, string) error
}, u account.User, password string) {
	t.Helper()
	hash, err := auth.NewHasher(bcrypt.MinCost).Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if err := repo.CreateUser(context.Background(), u, hash); err != nil {
		t.Fatalf("CreateUser %s: %v", u.Email, err)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com", Name: "Owner"}
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

	u := account.User{ID: ownerAID, Email: "owner@example.com", IsAdmin: true}
	mustCreateUser(t, repo, u, "pw")

	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
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

func TestExpiredSessionIsRejectedAndPruned(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")

	_, expiredHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: expiredHash, UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_, liveHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
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

func TestDeletingUserCascadesToSessions(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com"}
	mustCreateUser(t, repo, u, "pw")

	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	execSQL(t, url, `DELETE FROM users WHERE id = $1`, u.ID)

	if _, ok, err := repo.GetSessionUser(ctx, hash); ok || err != nil {
		t.Errorf("session survived user deletion: ok=%v err=%v", ok, err)
	}
}

func TestDisabledUserCannotAuthenticateAndReenableRestoresSignIn(t *testing.T) {
	ctx := context.Background()
	repo, url := freshRepo(t)

	u := account.User{ID: ownerAID, Email: "owner@example.com", Name: "Owner"}
	mustCreateUser(t, repo, u, "pw")
	other := account.User{ID: ownerBID, Email: "other@example.com", Name: "Other"}
	mustCreateUser(t, repo, other, "pw")

	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: hash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_, otherHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken other: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: otherHash, UserID: other.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession other: %v", err)
	}

	const (
		routeID   = "00000000-0000-4002-8003-0000000000d1"
		serviceID = "00000000-0000-4008-8003-0000000000d1"
	)
	if err := repo.CreateRoute(ctx, transit.Route{
		ID: routeID, Slug: "owned-alignment", Name: "Owned Alignment", Mode: "rail",
		Bidirectional: true,
		Geometry:      transit.GeoLineString{Type: "LineString", Coordinates: [][]float64{{-122, 37}, {-121, 37}}},
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	svc := transit.UserService{
		ID: serviceID, Slug: "owned-line", RouteID: routeID, OwnerID: u.ID, Name: "Owned Line",
		Vehicle: transit.VehicleParams{MaxSpeedKMH: 200, AccelerationMS2: 1, DecelerationMS2: 1, DwellS: 30},
		Stops: []transit.ServiceStopPoint{
			{Name: "A", Lat: 37, Lng: -121.8, Seq: 0, ChainageM: 10, OffsetM: 0},
			{Name: "B", Lat: 37, Lng: -121.4, Seq: 1, ChainageM: 20, OffsetM: 0},
		},
	}
	svc.MintStopSlugs()
	if err := repo.CreateUserService(ctx, svc); err != nil {
		t.Fatalf("CreateUserService: %v", err)
	}

	if err := repo.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}

	if _, ok, err := repo.GetSessionUser(ctx, hash); ok || err != nil {
		t.Errorf("disabled session: ok=%v err=%v, want false/nil", ok, err)
	}
	if n := queryInt(t, url, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID); n != 0 {
		t.Errorf("sessions for disabled user = %d, want 0", n)
	}

	disabledUser, disabledHash, disabledOK, disabledErr := repo.GetUserCredentialsByEmail(ctx, u.Email)
	unknownUser, unknownHash, unknownOK, unknownErr := repo.GetUserCredentialsByEmail(ctx, "nobody@example.com")
	if disabledOK || disabledErr != nil || unknownOK || unknownErr != nil {
		t.Fatalf("credentials: disabled ok=%v err=%v, unknown ok=%v err=%v; want false/nil both",
			disabledOK, disabledErr, unknownOK, unknownErr)
	}
	if disabledUser != unknownUser || disabledHash != unknownHash {
		t.Fatalf("disabled credentials = %+v %q, unknown email = %+v %q",
			disabledUser, disabledHash, unknownUser, unknownHash)
	}

	still, found, err := repo.GetUserByID(ctx, u.ID)
	if err != nil || !found {
		t.Fatalf("GetUserByID after disable: found=%v err=%v", found, err)
	}
	if still.DisabledAt == nil {
		t.Fatal("GetUserByID DisabledAt = nil, want the disable instant")
	}
	if still.Name != u.Name {
		t.Errorf("display name = %q, want %q", still.Name, u.Name)
	}
	byEmail, found, err := repo.GetUserByEmail(ctx, u.Email)
	if err != nil || !found || byEmail.DisabledAt == nil {
		t.Fatalf("GetUserByEmail after disable: found=%v err=%v disabled_at=%v", found, err, byEmail.DisabledAt)
	}
	listed, err := repo.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var sawDisabled, sawOther bool
	for _, row := range listed {
		switch row.ID {
		case u.ID:
			sawDisabled = row.DisabledAt != nil
		case other.ID:
			sawOther = row.DisabledAt == nil
		}
	}
	if !sawDisabled || !sawOther {
		t.Errorf("ListUsers disabled=%v other-enabled=%v, want both present", sawDisabled, sawOther)
	}

	kept, found, err := repo.GetUserServiceByID(ctx, serviceID)
	if err != nil || !found {
		t.Fatalf("GetUserServiceByID after disable: found=%v err=%v", found, err)
	}
	if kept.OwnerID != u.ID || kept.Name != svc.Name {
		t.Errorf("authored service = %+v, want owner %s name %q", kept, u.ID, svc.Name)
	}

	// The other account was never disabled; its live session still resolves.
	if got, ok, err := repo.GetSessionUser(ctx, otherHash); !ok || err != nil || got.ID != other.ID {
		t.Errorf("other session after disable: ok=%v err=%v user=%s", ok, err, got.ID)
	}
	if _, _, ok, err := repo.GetUserCredentialsByEmail(ctx, other.Email); !ok || err != nil {
		t.Errorf("other credentials after disable: ok=%v err=%v, want true/nil", ok, err)
	}

	// A repeat keeps the original disable instant and still drops sessions.
	past := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	execSQL(t, url, `UPDATE users SET disabled_at = $2 WHERE id = $1`, u.ID, past)
	_, againHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken again: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: againHash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession again: %v", err)
	}
	if err := repo.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetUserDisabled again: %v", err)
	}
	repeated, found, err := repo.GetUserByID(ctx, u.ID)
	if err != nil || !found || repeated.DisabledAt == nil || !repeated.DisabledAt.Equal(past) {
		t.Fatalf("second disable DisabledAt = %v, want %s", repeated.DisabledAt, past.Format(time.RFC3339))
	}
	if n := queryInt(t, url, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID); n != 0 {
		t.Errorf("sessions after a second disable = %d, want 0", n)
	}

	if err := repo.SetUserDisabled(ctx, u.ID, false); err != nil {
		t.Fatalf("SetUserDisabled re-enable: %v", err)
	}
	enabled, found, err := repo.GetUserByID(ctx, u.ID)
	if err != nil || !found {
		t.Fatalf("GetUserByID after re-enable: found=%v err=%v", found, err)
	}
	if enabled.DisabledAt != nil {
		t.Fatalf("DisabledAt after re-enable = %v, want nil", enabled.DisabledAt)
	}
	restored, _, ok, err := repo.GetUserCredentialsByEmail(ctx, u.Email)
	if err != nil || !ok || restored.ID != u.ID {
		t.Fatalf("credentials after re-enable: ok=%v err=%v id=%s", ok, err, restored.ID)
	}
	_, freshHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken fresh: %v", err)
	}
	if err := repo.CreateSession(ctx, account.Session{
		TokenHash: freshHash, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession fresh: %v", err)
	}
	if got, ok, err := repo.GetSessionUser(ctx, freshHash); !ok || err != nil || got.ID != u.ID {
		t.Errorf("session after re-enable: ok=%v err=%v user=%s", ok, err, got.ID)
	}

	const unknownID = "00000000-0000-4009-8003-0000000000ff"
	err = repo.SetUserDisabled(ctx, unknownID, true)
	if err == nil || !strings.Contains(err.Error(), unknownID) {
		t.Fatalf("SetUserDisabled unknown id: err=%v, want an error naming %s", err, unknownID)
	}
}

func TestOwnerScopedReads(t *testing.T) {
	ctx := context.Background()
	repo, _ := freshRepo(t)

	mustCreateUser(t, repo, account.User{ID: ownerAID, Email: "a@example.com"}, "pw")
	mustCreateUser(t, repo, account.User{ID: ownerBID, Email: "b@example.com"}, "pw")

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
