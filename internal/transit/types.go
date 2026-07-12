package transit

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
	ID          string `yaml:"id"          json:"id"`
	Slug        string `yaml:"slug"        json:"slug"`
	Name        string `yaml:"name"        json:"name"`
	Description string `yaml:"description" json:"description"`
	Status      string `yaml:"status"      json:"status"`
}

// Route is a physical alignment — geometry and mode only; no stops or schedule.
type Route struct {
	ID            string        `yaml:"id"            json:"id"`
	ScenarioID    string        `yaml:"scenario_id"   json:"scenario_id"`
	Name          string        `yaml:"name"          json:"name"`
	Mode          string        `yaml:"mode"          json:"mode"`
	Geometry      GeoLineString `yaml:"geometry"      json:"geometry"`
	Bidirectional bool          `yaml:"bidirectional" json:"bidirectional"`
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

// Service is an operating pattern: one route + vehicle type + stop list + schedule.
type Service struct {
	ID               string            `yaml:"id"               json:"id"`
	ScenarioID       string            `yaml:"scenario_id"      json:"scenario_id"`
	RouteID          string            `yaml:"route_id"         json:"route_id"`
	VehicleTypeID    string            `yaml:"vehicle_type_id"  json:"vehicle_type_id"`
	Name             string            `yaml:"name"             json:"name"`
	Direction        string            `yaml:"direction"        json:"direction"`
	Active           bool              `yaml:"active"           json:"active"`
	Stops            []ServiceStop     `yaml:"stops"            json:"stops"`
	FrequencyWindows []FrequencyWindow `yaml:"frequency_windows" json:"frequency_windows"`
}

// SegmentTime is the travel time in minutes for one adjacent station pair along a service.
// Segments are stored in service direction (northernmost terminus first for Phase 1).
// For bidirectional services the reverse direction uses the same time.
// Multi-hop origin–destination times are derived by summing consecutive segments;
// see Store.TravelTimeBetween.
type SegmentTime struct {
	FromSlug string `yaml:"from"    json:"from"`
	ToSlug   string `yaml:"to"      json:"to"`
	Minutes  int    `yaml:"minutes" json:"minutes"`
}

// TravelTimes holds adjacent segment travel times for a scenario.
// The full OD matrix is intentionally not stored; callers derive it by summing segments
// via Store.TravelTimeBetween, keeping physics-compiler independence behind a seam.
type TravelTimes struct {
	ScenarioSlug string        `yaml:"scenario_slug" json:"scenario_slug"`
	Segments     []SegmentTime `yaml:"segments"      json:"segments"`
}
