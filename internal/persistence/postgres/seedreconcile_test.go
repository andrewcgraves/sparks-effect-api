package postgres_test

import (
	"context"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func TestReconcileSeedRestoresADriftedSeededStation(t *testing.T) {
	repo, _ := freshRepo(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after seed: %v", err)
	}
	if written != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows on a just-seeded database, want 0", written)
	}

	sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
	}
	st, ok, err := repo.GetStationBySlug(ctx, sc.ID, "las-vegas")
	if err != nil || !ok {
		t.Fatalf("GetStationBySlug las-vegas: ok=%v err=%v", ok, err)
	}
	want := st.Location
	st.Location = transit.GeoPoint{Type: "Point", Coordinates: []float64{-115.136, 36.174}}
	if err := repo.UpdateStation(ctx, st); err != nil {
		t.Fatalf("UpdateStation: %v", err)
	}

	written, err = transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after drift: %v", err)
	}
	if written == 0 {
		t.Fatal("ReconcileSeed wrote nothing; the YAML correction did not reach the database")
	}

	got, ok, err := repo.GetStationBySlug(ctx, sc.ID, "las-vegas")
	if err != nil || !ok {
		t.Fatalf("GetStationBySlug after reconcile: ok=%v err=%v", ok, err)
	}
	if len(got.Location.Coordinates) != 2 ||
		got.Location.Coordinates[0] != want.Coordinates[0] ||
		got.Location.Coordinates[1] != want.Coordinates[1] {
		t.Errorf("las-vegas location = %v, want %v", got.Location.Coordinates, want.Coordinates)
	}

	again, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after restore: %v", err)
	}
	if again != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows after restore, want 0", again)
	}
}

func TestReconcileSeedDoesNotClobberAnAuthoredScenario(t *testing.T) {
	repo, _ := freshRepo(t)
	ctx := context.Background()

	owner := account.User{ID: "00000000-0000-4007-8009-000000000001", Email: "author@example.com", Name: "Author"}
	if err := repo.CreateUser(ctx, owner, ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerID := owner.ID
	sc := transit.Scenario{
		ID: "00000000-0000-4001-8009-000000000001", Slug: "authored",
		Name: "Mine", OwnerID: &ownerID,
	}
	if err := repo.CreateScenario(ctx, sc); err != nil {
		t.Fatalf("CreateScenario: %v", err)
	}

	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed: %v", err)
	}
	if written == 0 {
		t.Fatal("expected the seeded ca-hsr scenario to be inserted alongside the authored one")
	}

	got, ok, err := repo.GetScenarioBySlug(ctx, "authored")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug authored: ok=%v err=%v", ok, err)
	}
	if got.Name != "Mine" {
		t.Errorf("authored name = %q, want Mine", got.Name)
	}
	if got.OwnerID == nil || *got.OwnerID != ownerID {
		t.Errorf("authored owner = %v, want %s", got.OwnerID, ownerID)
	}
}

func TestReconcileSeedMovesMercedOntoItsSpur(t *testing.T) {
	// A database seeded before SPA-364 has Merced as a Phase 1 stop, a
	// gilroy→merced→madera segment chain, and no spur route or service.
	// ReconcileSeed is the only thing that reaches it, so it has to carry the
	// whole topology change: new rows created, changed rows replaced.
	repo, _ := freshRepo(t)
	ctx := context.Background()

	if _, err := transit.SeedIfEmpty(ctx, repo); err != nil {
		t.Fatalf("SeedIfEmpty: %v", err)
	}
	sc, ok, err := repo.GetScenarioBySlug(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetScenarioBySlug: ok=%v err=%v", ok, err)
	}

	const (
		phase1RouteID   = "00000000-0000-4002-8001-000000000001"
		spurRouteID     = "00000000-0000-4002-8001-000000000003"
		localID         = "00000000-0000-4004-8001-000000000002"
		shuttleID       = "00000000-0000-4004-8001-000000000004"
		gilroyStationID = "00000000-0000-4005-8001-000000000004"
		mercedStationID = "00000000-0000-4005-8001-000000000005"
	)

	tt, ok, err := repo.GetTravelTimes(ctx, "ca-hsr")
	if err != nil || !ok {
		t.Fatalf("GetTravelTimes: ok=%v err=%v", ok, err)
	}
	var old []transit.SegmentTime
	for _, seg := range tt.Segments {
		switch {
		case seg.RouteID == spurRouteID:
			continue
		case seg.FromSlug == "gilroy" && seg.ToSlug == "madera":
			rev := 2940
			old = append(old,
				transit.SegmentTime{FromSlug: "gilroy", ToSlug: "merced", RunSeconds: 3050, ReverseRunSeconds: &rev, RouteID: phase1RouteID},
				transit.SegmentTime{FromSlug: "merced", ToSlug: "madera", RunSeconds: 1040, RouteID: phase1RouteID})
		default:
			old = append(old, seg)
		}
	}
	tt.Segments = old
	if err := repo.UpsertTravelTimes(ctx, tt); err != nil {
		t.Fatalf("UpsertTravelTimes (pre-SPA-364 segments): %v", err)
	}

	local, ok, err := repo.GetServiceByID(ctx, localID)
	if err != nil || !ok {
		t.Fatalf("GetServiceByID HSR Local: ok=%v err=%v", ok, err)
	}
	var stops []transit.ServiceStop
	for _, stop := range local.Stops {
		stops = append(stops, transit.ServiceStop{StationID: stop.StationID, Sequence: len(stops) + 1})
		if stop.StationID == gilroyStationID {
			stops = append(stops, transit.ServiceStop{StationID: mercedStationID, Sequence: len(stops) + 1})
		}
	}
	local.Stops = stops
	if err := repo.UpdateService(ctx, local); err != nil {
		t.Fatalf("UpdateService (Merced back on HSR Local): %v", err)
	}
	if err := repo.DeleteService(ctx, shuttleID); err != nil {
		t.Fatalf("DeleteService (Merced shuttle): %v", err)
	}
	if err := repo.DeleteRoute(ctx, spurRouteID); err != nil {
		t.Fatalf("DeleteRoute (Merced spur): %v", err)
	}

	written, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed over the pre-SPA-364 shape: %v", err)
	}
	if written == 0 {
		t.Fatal("ReconcileSeed wrote nothing; the Merced spur did not reach the database")
	}

	services, err := repo.ListServicesByScenario(ctx, sc.ID)
	if err != nil {
		t.Fatalf("ListServicesByScenario: %v", err)
	}
	var shuttleFound bool
	for _, svc := range services {
		for _, stop := range svc.Stops {
			if stop.StationID == mercedStationID && svc.RouteID != spurRouteID {
				t.Errorf("service %q still calls at Merced over route %q after reconcile", svc.Name, svc.RouteID)
			}
		}
		if svc.ID == shuttleID {
			shuttleFound = true
		}
	}
	if !shuttleFound {
		t.Error("Merced shuttle not recreated by reconcile")
	}

	graph, err := transit.CompileSeededScenario(ctx, repo, sc, transit.DefaultBoardingWaitPolicy())
	if err != nil {
		t.Fatalf("CompileSeededScenario after reconcile: %v", err)
	}
	if secs, svcID := hopSeconds(t, graph, "gilroy", "madera"); secs != 1940 || svcID != localID {
		t.Errorf("gilroy→madera after reconcile: got %d s on %q, want 1940 s on HSR Local", secs, svcID)
	}
	for _, sg := range graph.Services {
		for _, e := range sg.Edges {
			if e.FromSlug == "gilroy" && e.ToSlug == "merced" {
				t.Errorf("service %s still has a gilroy→merced edge after reconcile", sg.ServiceID)
			}
		}
	}

	again, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		t.Fatalf("ReconcileSeed after the spur landed: %v", err)
	}
	if again != 0 {
		t.Fatalf("ReconcileSeed wrote %d rows on a second pass, want 0", again)
	}
}
