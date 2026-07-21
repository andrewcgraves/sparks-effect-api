package transit

import "time"

// GeoPoint is a GeoJSON Point geometry (WGS84, [longitude, latitude]).
type GeoPoint struct {
	Type        string    `yaml:"type"        json:"type"`
	Coordinates []float64 `yaml:"coordinates" json:"coordinates"`
}

// GeoLineString is a GeoJSON LineString geometry (WGS84, [[lng, lat], ...]).
type GeoLineString struct {
	Type        string      `yaml:"type"        json:"type"`
	Coordinates [][]float64 `yaml:"coordinates" json:"coordinates"`
}

// Scenario is the top-level container for a transit scenario.
type Scenario struct {
	ID          string  `yaml:"id"          json:"id"`
	Slug        string  `yaml:"slug"        json:"slug"`
	Name        string  `yaml:"name"        json:"name"`
	Description string  `yaml:"description" json:"description"`
	Status      string  `yaml:"status"      json:"status"`
	OwnerID     *string `yaml:"owner_id,omitempty" json:"owner_id,omitempty"`
}

// RouteSegment is the authored track physics for one span of a route — the
// stretch between two consecutive geometry coordinates. A route of n
// coordinates therefore has n-1 segments.
//
// The zero value describes tangent, level, uncanted track, so a route may omit
// its physics entirely and still be meaningful. Grade is stored as a percent
// (the unit routes are authored in); internal/physics consumes it as a ratio,
// so callers divide by 100 at that boundary.
type RouteSegment struct {
	CantMM       float64 `yaml:"cant_mm"        json:"cant_mm"`
	CurveRadiusM float64 `yaml:"curve_radius_m" json:"curve_radius_m"` // 0 means tangent track
	GradePct     float64 `yaml:"grade_pct"      json:"grade_pct"`
}

// Route is a physical alignment — geometry, mode, and per-segment track
// physics. A route never carries stops or a schedule; those belong to the
// services that run over it.
//
// ScenarioID is optional. Seeded routes belong to the scenario they were
// authored for, but a route ingested through the admin endpoint is a standalone
// alignment addressed by its slug, so it has no scenario until one adopts it.
type Route struct {
	ID            string        `yaml:"id"                     json:"id"`
	ScenarioID    *string       `yaml:"scenario_id,omitempty"  json:"scenario_id,omitempty"`
	Slug          string        `yaml:"slug"                   json:"slug"`
	Name          string        `yaml:"name"                   json:"name"`
	Mode          string        `yaml:"mode"                   json:"mode"`
	Geometry      GeoLineString `yaml:"geometry"               json:"geometry"`
	Bidirectional bool          `yaml:"bidirectional"          json:"bidirectional"`
	// Segments is always emitted, even when empty: a client reading a route
	// back should see an explicit empty list rather than a missing key it has
	// to interpret.
	Segments []RouteSegment `yaml:"segments,omitempty"     json:"segments"`
}

// Station is a named boarding point owned by a scenario.
type Station struct {
	ID             string   `yaml:"id"              json:"id"`
	ScenarioID     string   `yaml:"scenario_id"     json:"scenario_id"`
	Slug           string   `yaml:"slug"            json:"slug"`
	Name           string   `yaml:"name"            json:"name"`
	Location       GeoPoint `yaml:"location"        json:"location"`
	PlatformHeight string   `yaml:"platform_height" json:"platform_height"`
}

// VehicleType describes rolling stock capabilities.
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

// ServiceStop is one station in a service's stopping pattern.
type ServiceStop struct {
	StationID string `yaml:"station_id" json:"station_id"`
	Sequence  int    `yaml:"sequence"   json:"sequence"`
	DwellS    *int   `yaml:"dwell_s,omitempty" json:"dwell_s,omitempty"`
}

// FrequencyWindow describes a headway-based operating window for a service.
type FrequencyWindow struct {
	ID        string `yaml:"id"         json:"id"`
	ServiceID string `yaml:"service_id" json:"service_id"`
	StartTime string `yaml:"start_time" json:"start_time"`
	EndTime   string `yaml:"end_time"   json:"end_time"`
	HeadwayS  int    `yaml:"headway_s"  json:"headway_s"`
}

// Provenance tiers classify who owns a service's parameter values.
// computed: derived entirely by the physics compiler — editor should grey out levers.
// calibrated: seeded from real-world data; levers are editable but flagged.
// frozen: locked by policy; compiler will assert geometry is unchanged.
const (
	ProvenanceComputed   = "computed"
	ProvenanceCalibrated = "calibrated"
	ProvenanceFrozen     = "frozen"
)

// Service is an operating pattern: one route + vehicle type + stop list + schedule.
type Service struct {
	ID               string            `yaml:"id"               json:"id"`
	ScenarioID       string            `yaml:"scenario_id"      json:"scenario_id"`
	RouteID          string            `yaml:"route_id"         json:"route_id"`
	VehicleTypeID    string            `yaml:"vehicle_type_id"  json:"vehicle_type_id"`
	Name             string            `yaml:"name"             json:"name"`
	Direction        string            `yaml:"direction"        json:"direction"`
	Active           bool              `yaml:"active"           json:"active"`
	Provenance       string            `yaml:"provenance"       json:"provenance"`
	OwnerID          *string           `yaml:"owner_id,omitempty" json:"owner_id,omitempty"`
	Stops            []ServiceStop     `yaml:"stops"            json:"stops"`
	FrequencyWindows []FrequencyWindow `yaml:"frequency_windows" json:"frequency_windows"`
}

// SegmentTime is the run-time-only seconds for one adjacent station pair along a service.
// Values are run time only (train in motion); dwell is added at compile time.
// Segments are stored in service direction (northernmost terminus first for Phase 1).
// For bidirectional services the reverse direction uses the same time.
// Multi-hop origin–destination times are derived by summing consecutive segments;
// see Store.TravelTimeBetween.
type SegmentTime struct {
	FromSlug   string `yaml:"from"        json:"from"`
	ToSlug     string `yaml:"to"          json:"to"`
	RunSeconds int    `yaml:"run_seconds" json:"run_seconds"`
}

// TravelTimes holds adjacent segment run times for a scenario.
// The full OD matrix is intentionally not stored; callers derive it by summing segments
// via Store.TravelTimeBetween, keeping physics-compiler independence behind a seam.
type TravelTimes struct {
	ScenarioSlug string        `yaml:"scenario_slug" json:"scenario_slug"`
	Provenance   string        `yaml:"provenance"    json:"provenance"`
	Source       string        `yaml:"source"        json:"source"`
	Segments     []SegmentTime `yaml:"segments"      json:"segments"`
}

// User is an invite-only account that can own scenarios and services. Accounts
// are provisioned by an admin — there is no self-serve signup path.
//
// The password hash is deliberately not a field here: User is serialized
// straight to JSON by the auth endpoints, so keeping the credential out of the
// struct means it cannot leak through a response by accident. Credentials are
// read explicitly via Repository.GetUserCredentialsByEmail.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Session is an authenticated login, addressed by the SHA-256 hash of the
// bearer token handed to the client. The token itself is never persisted, so
// the stored row cannot be replayed as a credential.
type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Job status values track an async compile/compute unit of work through its lifecycle.
const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
)

// Job is a unit of async work (e.g. compile or compute) whose status survives
// restarts so callers can poll by job_id.
type Job struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	ScenarioID *string `json:"scenario_id,omitempty"`
	OwnerID    *string `json:"owner_id,omitempty"`
	Error      string  `json:"error,omitempty"`
	// Result is the compiled TransitGraph, set only once Status is
	// JobStatusSucceeded. A caller can also reach it directly by scenario slug
	// via Repository.GetLatestSucceededJob, without knowing the job id.
	Result    *TransitGraph `json:"result,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}
