package isochrone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

const (
	stadiaMaxMatrixDests  = 600
	stadiaMaxIsoDistanceM = 20_000.0
	roadDetourFactor      = 1.4
	walkSpeedKmH          = 5.0
	bikeSpeedKmH          = 15.0
	driveSpeedKmH         = 80.0

	// caHSRScenarioSlug identifies the California High-Speed Rail scenario. For
	// this scenario boarding wait is omitted from the isochrone budget so the
	// map reflects optimistic, wait-free connectivity. WaitModel is reported as
	// "none" to match.
	caHSRScenarioSlug = "ca-hsr"
)

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func modeSpeedKmH(mode Mode) float64 {
	switch mode {
	case ModeWalk:
		return walkSpeedKmH
	case ModeBike:
		return bikeSpeedKmH
	case ModeDrive:
		return driveSpeedKmH
	default:
		return 0
	}
}

func approxAccessReachKm(mode Mode, budgetMins int) float64 {
	budgetReachKm := modeSpeedKmH(mode) * float64(budgetMins) / 60.0 / roadDetourFactor
	stadiaPathLimitKm := stadiaMaxIsoDistanceM / 1000.0
	return math.Min(budgetReachKm, stadiaPathLimitKm)
}

func safeIsoBudgetSecs(mode Mode, budgetSecs int) int {
	speedMS := modeSpeedKmH(mode) * 1000 / 3600
	maxSecs := int(stadiaMaxIsoDistanceM / speedMS)
	if budgetSecs > maxSecs {
		return maxSecs
	}
	return budgetSecs
}

func wrapStadiaErr(err error) error {
	switch {
	case errors.Is(err, stadia.ErrStadiaBadRequest):
		return fmt.Errorf("%w: %v", ErrStadiaClientError, err)
	case errors.Is(err, stadia.ErrStadiaRateLimit):
		return fmt.Errorf("%w: %v", ErrStadiaRateLimit, err)
	default:
		return fmt.Errorf("%w: %v", ErrStadiaUnavailable, err)
	}
}

type chainImpl struct {
	stadia stadia.Client
	store  transit.IsochroneData
	log    *logger.Logger
}

// New constructs the production chainer.
func New(stadiaClient stadia.Client, store transit.IsochroneData, log *logger.Logger) Chainer {
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

	nodes, ok := c.store.Nodes(req.ScenarioSlug)
	if !ok {
		return nil, ErrScenarioNotFound
	}
	c.log.Debugf("chain: %d nodes in scenario", len(nodes))

	reachKm := approxAccessReachKm(req.Mode, req.BudgetMins)
	var nearbyNodes []transit.Node
	for _, n := range nodes {
		if haversineKm(req.Lat, req.Lng, n.Lat, n.Lng) <= reachKm {
			nearbyNodes = append(nearbyNodes, n)
		}
	}
	c.log.Debugf("chain: %d/%d nodes within haversine reach (%.1f km)", len(nearbyNodes), len(nodes), reachKm)

	if len(nearbyNodes) > stadiaMaxMatrixDests {
		sort.Slice(nearbyNodes, func(i, j int) bool {
			di := haversineKm(req.Lat, req.Lng, nearbyNodes[i].Lat, nearbyNodes[i].Lng)
			dj := haversineKm(req.Lat, req.Lng, nearbyNodes[j].Lat, nearbyNodes[j].Lng)
			return di < dj
		})
		nearbyNodes = nearbyNodes[:stadiaMaxMatrixDests]
		c.log.Debugf("chain: truncated to %d nodes (matrix destination cap)", stadiaMaxMatrixDests)
	}

	clampedBudget := safeIsoBudgetSecs(req.Mode, req.BudgetMins*60)
	originIsoClamped := clampedBudget < req.BudgetMins*60
	// Drive-mode origin isochrone is misleading when clamped: a 15-min auto polygon
	// captures almost no meaningful area and implies false reachability. Skip it and
	// report origin_iso_available: false so clients can respond appropriately.
	driveOriginUnavailable := req.Mode == ModeDrive && originIsoClamped

	var (
		originIso  *stadia.IsochroneResponse
		matrixResp *stadia.MatrixResponse
	)

	g, gctx := errgroup.WithContext(ctx)

	if !driveOriginUnavailable {
		g.Go(func() error {
			iso, isoErr := c.stadia.Isochrone(gctx, stadia.IsochroneRequest{
				Origin:     stadia.LatLng{Lat: req.Lat, Lng: req.Lng},
				Costing:    costing,
				BudgetSecs: clampedBudget,
			})
			if isoErr != nil {
				return wrapStadiaErr(isoErr)
			}
			originIso = iso
			return nil
		})
	}

	if len(nearbyNodes) > 0 {
		g.Go(func() error {
			dests := make([]stadia.LatLng, len(nearbyNodes))
			for i, n := range nearbyNodes {
				dests[i] = stadia.LatLng{Lat: n.Lat, Lng: n.Lng}
			}
			m, mErr := c.stadia.Matrix(gctx, stadia.MatrixRequest{
				Origins:      []stadia.LatLng{{Lat: req.Lat, Lng: req.Lng}},
				Destinations: dests,
				Costing:      costing,
			})
			if mErr != nil {
				return wrapStadiaErr(mErr)
			}
			matrixResp = m
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	c.log.Debugf("chain: stadia calls done (origin iso + matrix)")

	accessSecs := make(map[string]int)
	if matrixResp != nil && len(matrixResp.SourcesToTargets) > 0 {
		row := matrixResp.SourcesToTargets[0]
		for i, n := range nearbyNodes {
			if i < len(row) && row[i].Time >= 0 {
				accessSecs[n.Slug] = row[i].Time
			}
		}
	}

	c.log.Debugf("chain: matrix done, %d/%d nodes reachable", len(accessSecs), len(nodes))

	nodeBySlug := make(map[string]transit.Node, len(nodes))
	for _, n := range nodes {
		nodeBySlug[n.Slug] = n
	}

	type pathResult struct {
		accessMins    int
		remainingMins int
		serviceID     string
	}

	budgetSecs := req.BudgetMins * 60
	skipWait := req.ScenarioSlug == caHSRScenarioSlug
	bestPaths := make(map[string]pathResult)
	for egressSlug := range nodeBySlug {
		var best *pathResult
		bestRSecs := 0
		for accessSlug, aSecs := range accessSecs {
			var rSecs int
			var serviceID string
			if accessSlug == egressSlug {
				rSecs = budgetSecs - aSecs
			} else {
				transitSecs, transitWait, transitService, transitOK := c.store.TravelTimeBetween(req.ScenarioSlug, accessSlug, egressSlug)
				if !transitOK {
					continue
				}
				effectiveWait := transitWait
				if skipWait {
					effectiveWait = 0
				}
				rSecs = budgetSecs - aSecs - transitSecs - effectiveWait
				serviceID = transitService
			}
			if rSecs > 0 && (best == nil || rSecs > bestRSecs) {
				bestRSecs = rSecs
				p := pathResult{accessMins: aSecs / 60, remainingMins: rSecs / 60, serviceID: serviceID}
				best = &p
			}
		}
		if best != nil {
			bestPaths[egressSlug] = *best
		}
	}

	type egressCandidate struct {
		node          transit.Node
		remainingMins int
		accessMins    int
		serviceID     string
	}
	var egressCandidates []egressCandidate
	for _, n := range nodes {
		if p, hasBest := bestPaths[n.Slug]; hasBest {
			egressCandidates = append(egressCandidates, egressCandidate{
				node:          n,
				remainingMins: p.remainingMins,
				accessMins:    p.accessMins,
				serviceID:     p.serviceID,
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
					Origin:     stadia.LatLng{Lat: ec.node.Lat, Lng: ec.node.Lng},
					Costing:    costing,
					BudgetSecs: safeIsoBudgetSecs(req.Mode, ec.remainingMins*60),
				})
				if isoErr != nil {
					return wrapStadiaErr(isoErr)
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
				"station_slug":   ec.node.Slug,
				"remaining_mins": ec.remainingMins,
			})
			if injErr != nil {
				return nil, fmt.Errorf("isochrone: inject egress: %w", injErr)
			}
			features = append(features, injected)
		}
		reachableStations = append(reachableStations, ReachableStation{
			StationSlug:   ec.node.Slug,
			AccessMins:    ec.accessMins,
			RemainingMins: ec.remainingMins,
			ViaService:    ec.serviceID,
		})
	}

	c.log.Debugf("chain: complete features=%d reachable_stations=%d", len(features), len(reachableStations))

	waitModel := "headway_over_2_peak"
	if skipWait {
		waitModel = "none"
	}

	return &ChainResponse{
		Type:     "FeatureCollection",
		Features: features,
		Metadata: ChainMetadata{
			ReachableStations:  reachableStations,
			OriginBudgetMins:   req.BudgetMins,
			ScenarioSlug:       req.ScenarioSlug,
			Mode:               string(req.Mode),
			WaitModel:          waitModel,
			OriginIsoClamped:   originIsoClamped,
			OriginIsoAvailable: !driveOriginUnavailable,
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
