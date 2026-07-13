package isochrone

import (
	"context"
	"encoding/json"
	"errors"
)

type Mode string

const (
	ModeWalk  Mode = "walk"
	ModeBike  Mode = "bike"
	ModeDrive Mode = "drive"
)

var (
	ErrInvalidMode       = errors.New("invalid mode")
	ErrScenarioNotFound  = errors.New("scenario not found")
	ErrStadiaUnavailable = errors.New("stadia unavailable")
)

type ChainRequest struct {
	Lat          float64
	Lng          float64
	BudgetMins   int
	Mode         Mode
	ScenarioSlug string
}

type ReachableStation struct {
	StationSlug   string `json:"station_slug"`
	AccessMins    int    `json:"access_mins"`
	RemainingMins int    `json:"remaining_mins"`
}

type ChainMetadata struct {
	ReachableStations []ReachableStation `json:"reachable_stations"`
	OriginBudgetMins  int                `json:"origin_budget_mins"`
	ScenarioSlug      string             `json:"scenario_slug"`
	Mode              string             `json:"mode"`
	WaitModel         string             `json:"wait_model"`
	OriginIsoClamped  bool               `json:"origin_iso_clamped,omitempty"`
}

type ChainResponse struct {
	Type     string            `json:"type"`
	Features []json.RawMessage `json:"features"`
	Metadata ChainMetadata     `json:"metadata"`
}

// Chainer is the deep module interface.
type Chainer interface {
	Chain(ctx context.Context, req ChainRequest) (*ChainResponse, error)
}
