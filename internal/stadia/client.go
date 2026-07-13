package stadia

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrStadiaBadRequest = errors.New("stadia: bad request")
	ErrStadiaRateLimit  = errors.New("stadia: rate limit exceeded")
	ErrStadiaUpstream   = errors.New("stadia: upstream error")
)

type Costing string

const (
	CostingPedestrian Costing = "pedestrian"
	CostingBicycle    Costing = "bicycle"
	CostingAuto       Costing = "auto"
)

type LatLng struct {
	Lat float64
	Lng float64
}

type IsochroneRequest struct {
	Origin     LatLng
	Costing    Costing
	BudgetSecs int
}

// IsochroneResponse is the raw GeoJSON FeatureCollection from Stadia.
// Features carry polygon geometry; preserved as raw json.RawMessage.
type IsochroneResponse struct {
	Type     string            `json:"type"`
	Features []json.RawMessage `json:"features"`
}

type MatrixRequest struct {
	Origins      []LatLng
	Destinations []LatLng
	Costing      Costing
}

// MatrixCell holds travel time (seconds) for one origin→destination pair.
// Time == -1 means unreachable.
type MatrixCell struct {
	Time     int     `json:"time"`
	Distance float64 `json:"distance"`
}

type MatrixResponse struct {
	// SourcesToTargets[i][j] is origin i → destination j.
	SourcesToTargets [][]MatrixCell `json:"sources_to_targets"`
}

// Client is the Stadia routing seam.
type Client interface {
	Isochrone(ctx context.Context, req IsochroneRequest) (*IsochroneResponse, error)
	Matrix(ctx context.Context, req MatrixRequest) (*MatrixResponse, error)
}
