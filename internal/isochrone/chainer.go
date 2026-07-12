package isochrone

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type chainImpl struct {
	stadia stadia.Client
	store  transit.TransitData
}

// New constructs the production chainer.
func New(stadiaClient stadia.Client, store transit.TransitData) Chainer {
	return &chainImpl{stadia: stadiaClient, store: store}
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

	scenario, ok := c.store.GetScenarioBySlug(req.ScenarioSlug)
	if !ok {
		return nil, ErrScenarioNotFound
	}

	stations := c.store.GetStationsByScenario(scenario.ID)

	var (
		originIso  *stadia.IsochroneResponse
		matrixResp *stadia.MatrixResponse
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		iso, isoErr := c.stadia.Isochrone(gctx, stadia.IsochroneRequest{
			Origin:     stadia.LatLng{Lat: req.Lat, Lng: req.Lng},
			Costing:    costing,
			BudgetSecs: req.BudgetMins * 60,
		})
		if isoErr != nil {
			return fmt.Errorf("%w: %v", ErrStadiaUnavailable, isoErr)
		}
		originIso = iso
		return nil
	})

	if len(stations) > 0 {
		g.Go(func() error {
			dests := make([]stadia.LatLng, len(stations))
			for i, st := range stations {
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

	accessMins := make(map[string]int)
	if matrixResp != nil && len(matrixResp.SourcesToTargets) > 0 {
		row := matrixResp.SourcesToTargets[0]
		for i, st := range stations {
			if i < len(row) && row[i].Time >= 0 {
				accessMins[st.Slug] = row[i].Time / 60
			}
		}
	}

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
					BudgetSecs: ec.remainingMins * 60,
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

	return &ChainResponse{
		Type:     "FeatureCollection",
		Features: features,
		Metadata: ChainMetadata{
			ReachableStations: reachableStations,
			OriginBudgetMins:  req.BudgetMins,
			ScenarioSlug:      req.ScenarioSlug,
			Mode:              string(req.Mode),
			WaitModel:         "none",
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
