package transit

import (
	"fmt"
	"sort"
)

type CompilableService struct {
	ID                   string
	Route                Route
	Vehicle              Kinematics
	Stops                []CompilableStop
	Windows              []FrequencyWindow
	BoardingWait         *BoardingWaitOverride
	ScenarioBoardingWait *BoardingWaitOverride
}

type Kinematics struct {
	MaxSpeedKMH     float64
	AccelerationMS2 float64
	DecelerationMS2 float64
}

type CompilableStop struct {
	Slug    string
	Name    string
	Lat     float64
	Lng     float64
	DwellS  int
	OffsetM float64
}

func compilableFromService(route Route, stations []Station, svc Service, vt VehicleType) (CompilableService, error) {
	stationsByID := make(map[string]Station, len(stations))
	for _, st := range stations {
		stationsByID[st.ID] = st
	}

	stops := append([]ServiceStop(nil), svc.Stops...)
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].Sequence < stops[j].Sequence })

	compiled := make([]CompilableStop, len(stops))
	for i, stop := range stops {
		st, ok := stationsByID[stop.StationID]
		if !ok {
			return CompilableService{}, fmt.Errorf("compile: service %q references unknown station id %q", svc.ID, stop.StationID)
		}
		if len(st.Location.Coordinates) < 2 {
			return CompilableService{}, fmt.Errorf("compile: service %q: station %q has no location", svc.ID, st.Slug)
		}
		compiled[i] = CompilableStop{
			Slug:   st.Slug,
			Name:   st.Name,
			Lng:    st.Location.Coordinates[0],
			Lat:    st.Location.Coordinates[1],
			DwellS: resolveDwell(stop, st, vt),
		}
	}

	return CompilableService{
		ID:    svc.ID,
		Route: route,
		Vehicle: Kinematics{
			MaxSpeedKMH:     vt.MaxSpeedKMH,
			AccelerationMS2: vt.AccelerationMS2,
			DecelerationMS2: vt.DecelerationMS2,
		},
		Stops:        compiled,
		Windows:      svc.FrequencyWindows,
		BoardingWait: svc.BoardingWait,
	}, nil
}

func CompilableFromUserService(route Route, svc UserService) (CompilableService, error) {
	if route.ID != svc.RouteID {
		return CompilableService{}, fmt.Errorf("compile: service %q references route %q, got route %q",
			svc.ID, svc.RouteID, route.ID)
	}

	slugs := StopSlugs(svc)

	// Identities are assigned over svc.Stops as authored and then reordered for
	// compilation, rather than assigned after sorting, so that a stop's slug
	// depends on where its author put it and not on where Seq happens to sort
	// it. That is what lets StopSlugs answer for the same stop out here.
	order := make([]int, len(svc.Stops))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return svc.Stops[order[a]].Seq < svc.Stops[order[b]].Seq })

	compiled := make([]CompilableStop, len(order))
	for i, idx := range order {
		stop := svc.Stops[idx]
		compiled[i] = CompilableStop{
			Slug:    slugs[idx],
			Name:    stop.Name,
			Lat:     stop.Lat,
			Lng:     stop.Lng,
			DwellS:  svc.Vehicle.DwellS,
			OffsetM: stop.OffsetM,
		}
	}

	return CompilableService{
		ID:    svc.ID,
		Route: route,
		Vehicle: Kinematics{
			MaxSpeedKMH:     svc.Vehicle.MaxSpeedKMH,
			AccelerationMS2: svc.Vehicle.AccelerationMS2,
			DecelerationMS2: svc.Vehicle.DecelerationMS2,
		},
		Stops:        compiled,
		Windows:      svc.FrequencyWindows,
		BoardingWait: svc.BoardingWait,
	}, nil
}

func StopSlugs(svc UserService) []string {
	slugs := make([]string, len(svc.Stops))
	taken := make(map[string]bool, len(svc.Stops))
	for i, stop := range svc.Stops {
		// svc.Slug is used verbatim rather than passed through Slugify. It is
		// already a slug — the handler mints it with Slugify and then adds any
		// collision suffix — and Slugify is not idempotent at the margin: it
		// truncates at maxSlugLen, so re-slugifying an 82-character "<80 chars>-2"
		// cuts off the very suffix that distinguishes it, and two different
		// services mint byte-identical stop identities. user_services.slug is
		// UNIQUE, so taking it as given is exactly the guarantee this needs.
		base := svc.Slug + "--" + Slugify(stop.Name)
		slug := base
		for n := 2; taken[slug]; n++ {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		taken[slug] = true
		slugs[i] = slug
	}
	return slugs
}
