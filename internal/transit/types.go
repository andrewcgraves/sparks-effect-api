package transit

import (
	"encoding/json"
	"time"
)

type GeoPoint struct {
	Type        string    `yaml:"type"        json:"type"`
	Coordinates []float64 `yaml:"coordinates" json:"coordinates"`
}

type GeoLineString struct {
	Type        string      `yaml:"type"        json:"type"`
	Coordinates [][]float64 `yaml:"coordinates" json:"coordinates"`
}

type Scenario struct {
	ID          string  `yaml:"id"          json:"id"`
	Slug        string  `yaml:"slug"        json:"slug"`
	Name        string  `yaml:"name"        json:"name"`
	Description string  `yaml:"description" json:"description"`
	Status      string  `yaml:"status"      json:"status"`
	OwnerID     *string `yaml:"owner_id,omitempty" json:"owner_id,omitempty"`
}

type RouteSegment struct {
	CantMM       float64 `yaml:"cant_mm"        json:"cant_mm"`
	CurveRadiusM float64 `yaml:"curve_radius_m" json:"curve_radius_m"`
	GradePct     float64 `yaml:"grade_pct"      json:"grade_pct"`
}

type Route struct {
	ID            string         `yaml:"id"                     json:"id"`
	ScenarioID    *string        `yaml:"scenario_id,omitempty"  json:"scenario_id,omitempty"`
	OwnerID       *string        `yaml:"owner_id,omitempty"     json:"owner_id,omitempty"`
	Slug          string         `yaml:"slug"                   json:"slug"`
	Name          string         `yaml:"name"                   json:"name"`
	Description   string         `yaml:"description,omitempty"  json:"description,omitempty"`
	Mode          string         `yaml:"mode"                   json:"mode"`
	Geometry      GeoLineString  `yaml:"geometry"               json:"geometry"`
	Bidirectional bool           `yaml:"bidirectional"          json:"bidirectional"`
	Segments      []RouteSegment `yaml:"segments,omitempty"     json:"segments"`
}

type RouteSummary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Mode        string `json:"mode"`
}

type RouteDependents struct {
	Services     int `json:"services"`
	UserServices int `json:"user_services"`
	Segments     int `json:"segments"`
}

func (d RouteDependents) Any() bool {
	return d.Services > 0 || d.UserServices > 0 || d.Segments > 0
}

type Station struct {
	ID              string    `yaml:"id"              json:"id"`
	ScenarioID      string    `yaml:"scenario_id"     json:"scenario_id"`
	OwnerID         *string   `yaml:"owner_id,omitempty" json:"owner_id,omitempty"`
	Slug            string    `yaml:"slug"            json:"slug"`
	Name            string    `yaml:"name"            json:"name"`
	Location        GeoPoint  `yaml:"location"        json:"location"`
	RoutingLocation *GeoPoint `yaml:"routing_location,omitempty" json:"routing_location,omitempty"`
	PlatformHeight  string    `yaml:"platform_height" json:"platform_height"`
}

type VehicleType struct {
	ID              string  `yaml:"id"               json:"id"`
	Name            string  `yaml:"name"             json:"name"`
	Propulsion      string  `yaml:"propulsion"       json:"propulsion"`
	MaxSpeedKMH     float64 `yaml:"max_speed_kmh"    json:"max_speed_kmh"`
	AccelerationMS2 float64 `yaml:"acceleration_ms2" json:"acceleration_ms2"`
	DecelerationMS2 float64 `yaml:"deceleration_ms2" json:"deceleration_ms2"`
	FloorHeight     string  `yaml:"floor_height"     json:"floor_height"`
	DwellLevelS     int     `yaml:"dwell_level_s"    json:"dwell_level_s"`
	DwellStepS      int     `yaml:"dwell_step_s"     json:"dwell_step_s"`
}

type ServiceStop struct {
	StationID string `yaml:"station_id" json:"station_id"`
	Sequence  int    `yaml:"sequence"   json:"sequence"`
	DwellS    *int   `yaml:"dwell_s,omitempty" json:"dwell_s,omitempty"`
}

type FrequencyWindow struct {
	StartTime string `yaml:"start_time" json:"start_time"`
	EndTime   string `yaml:"end_time"   json:"end_time"`
	HeadwayS  int    `yaml:"headway_s"  json:"headway_s"`
}

const (
	ProvenanceComputed   = "computed"
	ProvenanceCalibrated = "calibrated"
	ProvenanceFrozen     = "frozen"
)

type Service struct {
	ID                 string                `yaml:"id"               json:"id"`
	ScenarioID         string                `yaml:"scenario_id"      json:"scenario_id"`
	RouteID            string                `yaml:"route_id"         json:"route_id"`
	VehicleTypeID      string                `yaml:"vehicle_type_id"  json:"vehicle_type_id"`
	Name               string                `yaml:"name"             json:"name"`
	Direction          string                `yaml:"direction"        json:"direction"`
	Active             bool                  `yaml:"active"           json:"active"`
	Provenance         string                `yaml:"provenance"       json:"provenance"`
	OwnerID            *string               `yaml:"owner_id,omitempty" json:"owner_id,omitempty"`
	Stops              []ServiceStop         `yaml:"stops"            json:"stops"`
	FrequencyWindows   []FrequencyWindow     `yaml:"frequency_windows" json:"frequency_windows"`
	BoardingWait       *BoardingWaitOverride `yaml:"boarding_wait,omitempty" json:"boarding_wait,omitempty"`
	BoardingWaitPolicy string                `yaml:"-" json:"boarding_wait_policy,omitempty"`
	BoardingWaitSecs   int                   `yaml:"-" json:"boarding_wait_secs"`
	BoardingWaitSource string                `yaml:"-" json:"boarding_wait_source,omitempty"`
}

func (s *Service) ResolveBoardingWait(scenario *BoardingWaitOverride, global BoardingWaitPolicy) error {
	policy, source, err := ResolveBoardingWait(s.BoardingWait, scenario, global)
	if err != nil {
		return err
	}
	if err := policy.resolveInto(s.FrequencyWindows, &s.BoardingWaitPolicy, &s.BoardingWaitSecs); err != nil {
		return err
	}
	s.BoardingWaitSource = source
	return nil
}

type SegmentTime struct {
	FromSlug          string `yaml:"from"                          json:"from"`
	ToSlug            string `yaml:"to"                            json:"to"`
	RunSeconds        int    `yaml:"run_seconds"                   json:"run_seconds"`
	ReverseRunSeconds *int   `yaml:"reverse_run_seconds,omitempty" json:"reverse_run_seconds,omitempty"`
	RouteID           string `yaml:"route_id"                      json:"route_id"`
}

type TravelTimes struct {
	ScenarioSlug string        `yaml:"scenario_slug" json:"scenario_slug"`
	Provenance   string        `yaml:"provenance"    json:"provenance"`
	Source       string        `yaml:"source"        json:"source"`
	Segments     []SegmentTime `yaml:"segments"      json:"segments"`
}

const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
)

const (
	JobKindCompileScenario     = "compile_scenario"
	JobKindCompileUserScenario = "compile_user_scenario"
	JobKindCompileUserService  = "compile_user_service"
)

type Job struct {
	ID                 string        `json:"id"`
	Kind               string        `json:"kind"`
	Status             string        `json:"status"`
	ScenarioID         *string       `json:"scenario_id,omitempty"`
	UserScenarioID     *string       `json:"user_scenario_id,omitempty"`
	UserServiceID      *string       `json:"user_service_id,omitempty"`
	OwnerID            *string       `json:"owner_id,omitempty"`
	Error              string        `json:"error,omitempty"`
	Result             *TransitGraph `json:"result,omitempty"`
	CompiledServiceIDs []string      `json:"compiled_service_ids,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

type RoutingJob struct {
	ID           string          `json:"id"`
	Status       string          `json:"status"`
	CompileJobID string          `json:"compile_job_id"`
	OwnerID      *string         `json:"owner_id,omitempty"`
	Lat          float64         `json:"lat"`
	Lng          float64         `json:"lng"`
	BudgetMins   int             `json:"budget_mins"`
	Mode         TravelMode      `json:"mode"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type ServiceMembership struct {
	ServiceID string
	UpdatedAt time.Time
}

type PrerenderedIsochrone struct {
	ID                 string          `json:"id"`
	ScenarioSlug       string          `json:"scenario_slug"`
	Label              string          `json:"label"`
	Lat                float64         `json:"lat"`
	Lng                float64         `json:"lng"`
	BudgetMins         int             `json:"budget_mins"`
	Mode               TravelMode      `json:"mode"`
	Result             json.RawMessage `json:"result,omitempty"`
	CompiledServiceIDs []string        `json:"compiled_service_ids,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}
