package isochrone

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"golang.org/x/sync/errgroup"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const stadiaMaxIsoDistanceM = 20_000.0

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func maxReachKm(mode Mode, budgetMins int) float64 {
	const roadFactor = 1.4
	var speedKmH float64
	switch mode {
	case ModeWalk:
		speedKmH = 5.0
	case ModeBike:
		speedKmH = 15.0
	case ModeDrive:
		speedKmH = 80.0
	}
	return speedKmH * float64(budgetMins) / 60.0 / roadFactor
}

func safeIsoBudgetSecs(mode Mode, budgetSecs int) int {
	var speedMS float64
	switch mode {
	case ModeWalk:
		speedMS = 5.0 * 1000 / 3600
	case ModeBike:
		speedMS = 15.0 * 1000 / 3600
	case ModeDrive:
		speedMS = 80.0 * 1000 / 3600
	}
	maxSecs := int(stadiaMaxIsoDistanceM / speedMS)
	if budgetSecs > maxSecs {
		return maxSecs
	}
	return budgetSecs
}

type chainImpl struct {
	stadia stadia.Client
	store  transit.TransitData
	log    *logger.Logger
}

// New constructs the production chainer.
func New(stadiaClient stadia.Client, store transit.TransitData, log *logger.Logger) Chainer {
	return &chainImpl{stadia: stadiaClient, store: store, log: log}
}

func modeToCosting(m Mode) (stadia.Costing, error) {
	switch m {
	case ModeWalk:
		return stadia.CostingPedestrian, nil
	case ModeBike:
		return stadia.CostingBicycle, nil
	case ModeDrive:
		return stadia.CostingAuto, nil
	default:
		return "", ErrInvalidMode
	}
}

func (c *chainImpl) Chain(ctx context.Context, req ChainRequest) (*ChainResponse, error) {
	costing, err := modeToCosting(req.Mode)
	if err != nil {
		return nil, ErrInvalidMode
	}

	c.log.Debugf("chain: lat=%.6f lng=%.6f budget_mins=%d mode=%s scenario=%s",
		req.Lat, req.Lng, req.BudgetMins, req.Mode, req.ScenarioSlug)

	scenario, ok := c.store.GetScenarioBySlug(req.ScenarioSlug)
	if !ok {
		return nil, ErrScenarioNotFound
	}

	stations := c.store.GetStationsByScenario(scenario.ID)
	c.log.Debugf("chain: %d stations in scenario", len(stations))

	reachKm := maxReachKm(req.Mode, req.BudgetMins)
	var nearbyStations []transit.Station
	for _, st := range stations {
		if haversineKm(req.Lat, req.Lng, st.Location.Coordinates[1], st.Location.Coordinates[0]) <= reachKm {
			nearbyStations = append(nearbyStations, st)
		}
	}
	c.log.Debugf("chain: %d/%d stations within haversine reach (%.1f km)", len(nearbyStations), len(stations), reachKm)

	clampedBudget := safeIsoBudgetSecs(req.Mode, req.BudgetMins*60)
	originIsoClamped := clampedBudget < req.BudgetMins*60

	var (
		originIso  *stadia.IsochroneResponse
		matrixResp *stadia.MatrixResponse
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		iso, isoErr := c.stadia.Isochrone(gctx, stadia.IsochroneRequest{
			Origin:     stadia.LatLng{Lat: req.Lat, Lng: req.Lng},
			Costing:    costing,
			BudgetSecs: clampedBudget,
		})
		if isoErr != nil {
			return fmt.Errorf("%w: %v", ErrStadiaUnavailable, isoErr)
		}
		originIso = iso
		return nil
	})

	if len(nearbyStations) > 0 {
		g.Go(func() error {
			dests := make([]stadia.LatLng, len(nearbyStations))
			for i, st := range nearbyStations {
				dests[i] = stadia.LatLng{Lat: st.Location.Coordinates[1], Lng: st.Location.Coordinates[0]}
			}
			m, mErr := c.stadia.Matrix(gctx, stadia.MatrixRequest{
				Origins:      []stadia.LatLng{{Lat: req.Lat, Lng: req.Lng}},
				Destinations: dests,
				Costing:      costing,
			})
			if mErr != nil {
				return fmt.Errorf("%w: %v", ErrStadiaUnavailable, mErr)
			}
			matrixResp = m
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	c.log.Debugf("chain: stadia calls done (origin iso + matrix)")

	accessMins := make(map[string]int)
	if matrixResp != nil && len(matrixResp.SourcesToTargets) > 0 {
		row := matrixResp.SourcesToTargets[0]
		for i, st := range nearbyStations {
			if i < len(row) && row[i].Time >= 0 {
				accessMins[st.Slug] = row[i].Time / 60
			}
		}
	}

	c.log.Debugf("chain: matrix done, %d/%d stations reachable", len(accessMins), len(stations))

	stationBySlug := make(map[string]transit.Station, len(stations))
	for _, st := range stations {
		stationBySlug[st.Slug] = st
	}

	type pathResult struct {
		accessMins    int
		remainingMins int
	}

	bestPaths := make(map[string]pathResult)
	for egressSlug := range stationBySlug {
		var best *pathResult
		for accessSlug, aMins := range accessMins {
			var r int
			if accessSlug == egressSlug {
				r = req.BudgetMins - aMins
			} else {
				hsrMins, hsrOK := c.store.TravelTimeBetween(req.ScenarioSlug, accessSlug, egressSlug)
				if !hsrOK {
					continue
				}
				r = req.BudgetMins - aMins - hsrMins
			}
			if r > 0 && (best == nil || r > best.remainingMins) {
				p := pathResult{accessMins: aMins, remainingMins: r}
				best = &p
			}
		}
		if best != nil {
			bestPaths[egressSlug] = *best
		}
	}

	type egressCandidate struct {
		station       transit.Station
		remainingMins int
		accessMins    int
	}
	var egressCandidates []egressCandidate
	for _, st := range stations {
		if p, hasBest := bestPaths[st.Slug]; hasBest {
			egressCandidates = append(egressCandidates, egressCandidate{
				station:       st,
				remainingMins: p.remainingMins,
				accessMins:    p.accessMins,
			})
		}
	}

	c.log.Debugf("chain: egress fan-out %d candidates", len(egressCandidates))

	egressIsos := make([]*stadia.IsochroneResponse, len(egressCandidates))

	if len(egressCandidates) > 0 {
		g2, gctx2 := errgroup.WithContext(ctx)
		sem := make(chan struct{}, 10)
		for i, ec := range egressCandidates {
			i, ec := i, ec
			g2.Go(func() error {
				sem <- struct{}{}
				defer func() { <-sem }()
				iso, isoErr := c.stadia.Isochrone(gctx2, stadia.IsochroneRequest{
					Origin:     stadia.LatLng{Lat: ec.station.Location.Coordinates[1], Lng: ec.station.Location.Coordinates[0]},
					Costing:    costing,
					BudgetSecs: safeIsoBudgetSecs(req.Mode, ec.remainingMins*60),
				})
				if isoErr != nil {
					return fmt.Errorf("%w: %v", ErrStadiaUnavailable, isoErr)
				}
				egressIsos[i] = iso
				return nil
			})
		}
		if err := g2.Wait(); err != nil {
			return nil, err
		}
	}

	features := make([]json.RawMessage, 0)
	if originIso != nil {
		for _, f := range originIso.Features {
			injected, injErr := injectProperties(f, map[string]any{"source": "origin"})
			if injErr != nil {
				return nil, fmt.Errorf("isochrone: inject origin: %w", injErr)
			}
			features = append(features, injected)
		}
	}

	reachableStations := make([]ReachableStation, 0, len(egressCandidates))
	for i, ec := range egressCandidates {
		if egressIsos[i] == nil {
			continue
		}
		for _, f := range egressIsos[i].Features {
			injected, injErr := injectProperties(f, map[string]any{
				"source":         "egress",
				"station_slug":   ec.station.Slug,
				"remaining_mins": ec.remainingMins,
			})
			if injErr != nil {
				return nil, fmt.Errorf("isochrone: inject egress: %w", injErr)
			}
			features = append(features, injected)
		}
		reachableStations = append(reachableStations, ReachableStation{
			StationSlug:   ec.station.Slug,
			AccessMins:    ec.accessMins,
			RemainingMins: ec.remainingMins,
		})
	}

	c.log.Debugf("chain: complete features=%d reachable_stations=%d", len(features), len(reachableStations))

	return &ChainResponse{
		Type:     "FeatureCollection",
		Features: features,
		Metadata: ChainMetadata{
			ReachableStations: reachableStations,
			OriginBudgetMins:  req.BudgetMins,
			ScenarioSlug:      req.ScenarioSlug,
			Mode:              string(req.Mode),
			WaitModel:         "none",
			OriginIsoClamped:  originIsoClamped,
		},
	}, nil
}

func injectProperties(feature json.RawMessage, props map[string]any) (json.RawMessage, error) {
	var f map[string]json.RawMessage
	if err := json.Unmarshal(feature, &f); err != nil {
		return nil, err
	}

	existing := make(map[string]any)
	if raw, ok := f["properties"]; ok && string(raw) != "null" {
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			existing = parsed
		}
	}

	for k, v := range props {
		existing[k] = v
	}

	propsBytes, err := json.Marshal(existing)
	if err != nil {
		return nil, err
	}
	f["properties"] = propsBytes

	return json.Marshal(f)
}
