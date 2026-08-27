package transit

import (
	"encoding/json"
	"time"
)

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

// RouteSummary is a route reduced to what is needed to *choose* one: how it is
// addressed, what it is called, and what it is. Geometry and per-segment
// physics are deliberately absent — they dominate a route's size and nothing
// about picking one depends on them. The internal ID is absent too, since a
// route is addressed by slug everywhere on the wire.
type RouteSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// Station is a named boarding point owned by a scenario.
//
// RoutingLocation is nil for most stations (SPA-234, SPA-258): it exists for a
// station whose real, surveyed Location the routing worker's Valhalla graph
// cannot usefully route from — a site still under construction has no walkable
// network OSM can see yet, and a pin on a proposed alignment or a viaduct
// snaps onto an edge connected to almost nothing — so an egress isochrone
// centred on Location itself is either rejected outright ("Locations are in
// unconnected regions") or comes back orders of magnitude too small. Location
// stays the accurate place the station is (what the map pin and the route
// geometry's terminus show); RoutingLocation, when set, is only where the
// compiled graph node's egress isochrone is centred instead — see seededNodes. Moving Location itself to whatever is
// walkable today was rejected: that point is not the station and would need
// correcting again once the real thing opens (see migration 00015 and SPA-222,
// which did exactly that correction the other way).
type Station struct {
	ID              string    `yaml:"id"              json:"id"`
	ScenarioID      string    `yaml:"scenario_id"     json:"scenario_id"`
	Slug            string    `yaml:"slug"            json:"slug"`
	Name            string    `yaml:"name"            json:"name"`
	Location        GeoPoint  `yaml:"location"        json:"location"`
	RoutingLocation *GeoPoint `yaml:"routing_location,omitempty" json:"routing_location,omitempty"`
	PlatformHeight  string    `yaml:"platform_height" json:"platform_height"`
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

// FrequencyWindow describes a headway-based operating window. Times are
// free-form clock strings ("06:00"); the compiler interprets them.
//
// Shared by both service models: the seeded Service and the user-authored
// UserService express headways identically, so this is one type rather than
// two. It deliberately carries no row identity — a window has no meaning
// outside the service that owns it, and both persistence paths write the whole
// ordered set together — which is what lets BoardingWaitPolicy.WaitSecs run
// against either model.
type FrequencyWindow struct {
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
	// BoardingWaitPolicy and BoardingWaitSecs are the resolved boarding wait
	// that would be compiled into this service's graph under the current global
	// policy (SPA-236). They are response-only — never persisted — and filled
	// by the read handlers so a client never has to re-derive them.
	BoardingWaitPolicy string `yaml:"-" json:"boarding_wait_policy,omitempty"`
	BoardingWaitSecs   int    `yaml:"-" json:"boarding_wait_secs"`
}

// ResolveBoardingWait fills the response-only boarding-wait fields from policy
// and this service's own frequency windows. Both service models expose this,
// so a caller can fill either without a parallel implementation.
func (s *Service) ResolveBoardingWait(policy BoardingWaitPolicy) error {
	return policy.resolveInto(s.FrequencyWindows, &s.BoardingWaitPolicy, &s.BoardingWaitSecs)
}

// SegmentTime is the run-time-only seconds for one adjacent station pair along a service.
// Values are run time only (train in motion); dwell is added at compile time.
// Segments are stored in service direction (northernmost terminus first for Phase 1).
// ReverseRunSeconds, when nil, means the reverse direction reuses RunSeconds; when
// present, it is the reverse-direction run time. Multi-hop origin–destination
// times are derived by summing consecutive segments; see Store.TravelTimeBetween.
//
// RouteID names the route this span is track of, not the service that runs over
// it: several services share one corridor (CA HSR Express and Local both run
// Phase 1), so the route is the only identity a segment actually has. It is the
// grouping key for a "time between stations" table once a scenario spans more
// than one corridor. Compilation stays route-blind — buildSegmentAdj still walks
// every segment as one physical graph, so services can path across corridors
// that meet at a shared station.
type SegmentTime struct {
	FromSlug          string `yaml:"from"                          json:"from"`
	ToSlug            string `yaml:"to"                            json:"to"`
	RunSeconds        int    `yaml:"run_seconds"                   json:"run_seconds"`
	ReverseRunSeconds *int   `yaml:"reverse_run_seconds,omitempty" json:"reverse_run_seconds,omitempty"`
	RouteID           string `yaml:"route_id"                      json:"route_id"`
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

// Job kind values name what a compile job is compiling — the discriminator that
// says which of a Job's target FKs is populated (ScenarioID, UserScenarioID, or
// UserServiceID) and which loader the worker runs. A single-service compile is a
// degenerate scenario compile (one member, only singleton clusters), but it
// carries its own kind so the target FK it sets is unambiguous.
const (
	JobKindCompileScenario     = "compile_scenario"
	JobKindCompileUserScenario = "compile_user_scenario"
	JobKindCompileUserService  = "compile_user_service"
)

// Job is a unit of async work (e.g. compile or compute) whose status survives
// restarts so callers can poll by job_id.
//
// Exactly one of ScenarioID / UserScenarioID / UserServiceID names the compile
// target, chosen by Kind; the database CHECK jobs_one_target enforces at most
// one (the seeded FK's ON DELETE SET NULL can legitimately leave none).
type Job struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	// ScenarioID targets a seeded scenario; UserScenarioID a user-authored
	// curated scenario; UserServiceID a single user-authored service compiled
	// alone. Kind says which is set.
	ScenarioID     *string `json:"scenario_id,omitempty"`
	UserScenarioID *string `json:"user_scenario_id,omitempty"`
	UserServiceID  *string `json:"user_service_id,omitempty"`
	OwnerID        *string `json:"owner_id,omitempty"`
	Error          string  `json:"error,omitempty"`
	// Result is the compiled TransitGraph, set only once Status is
	// JobStatusSucceeded. A caller can also reach it directly by scenario slug
	// via Repository.GetLatestSucceededJob, without knowing the job id.
	Result *TransitGraph `json:"result,omitempty"`
	// CompiledServiceIDs records which member service ids this job actually
	// compiled — a snapshot taken at compile time, so a member since deleted
	// stays listed. It backs staleness detection (SPA-116): comparing this to a
	// user scenario's current membership reveals a member that has gone away.
	CompiledServiceIDs []string  `json:"compiled_service_ids,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TravelMode is how a person covers the access and egress legs of an isochrone:
// the domain's own vocabulary, and what the routing_jobs row stores.
//
// Valhalla calls the same concept "costing" and spells it pedestrian /
// bicycle / auto. That translation belongs at the routing client's boundary, in
// the worker; persisting both would be two columns that must agree forever.
type TravelMode string

const (
	TravelModeWalk  TravelMode = "walk"
	TravelModeBike  TravelMode = "bike"
	TravelModeDrive TravelMode = "drive"
)

// Valid reports whether m is one of the three modes. It is the single
// definition of the set, so the request validator, the queue message, and the
// database cannot drift apart on what a mode is.
func (m TravelMode) Valid() bool {
	switch m {
	case TravelModeWalk, TravelModeBike, TravelModeDrive:
		return true
	default:
		return false
	}
}

// RoutingJob is one isochrone the API has handed to the routing worker: a
// request resolved down to a point, a budget, a mode, and the one immutable
// compiled graph it is to be plotted over.
//
// It is a separate table from Job, not another Job kind, because the two are
// owned by different processes. The API inserts a RoutingJob and polls it; the
// worker in the other repository transitions it and writes its Result. A Job,
// by contrast, is compiled in this process. Sharing a table would mean two
// writers on rows whose lifecycles have nothing in common.
//
// It reuses the JobStatus* vocabulary rather than minting a second one — queued
// → running → succeeded/failed means the same thing here, and a client polling
// both surfaces should not have to learn two spellings of it.
//
// OwnerID is nil for the public seeded isochrone, which no one authenticates to
// request. An ownerless job is readable by anyone holding its id, which is safe
// only because that id is an unguessable UUID; see handler.RoutingJobStatus.
type RoutingJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// CompileJobID names the compile job whose result is the graph this
	// isochrone is plotted over. A compiled graph's identity is the job that
	// produced it (SPA-181), so this is what makes the request reproducible and
	// what the worker's result cache keys on.
	CompileJobID string     `json:"compile_job_id"`
	OwnerID      *string    `json:"owner_id,omitempty"`
	Lat          float64    `json:"lat"`
	Lng          float64    `json:"lng"`
	BudgetMins   int        `json:"budget_mins"`
	Mode         TravelMode `json:"mode"`
	// Result is whatever the worker computed, held as raw JSON rather than a
	// struct. The API neither produces nor interprets it: giving it a Go type
	// here would be a second copy of the worker's output contract, in a
	// repository with no compiler able to check the two still agree.
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ServiceMembership is one member of a scenario's curated service set: the
// service's id, and when that service last changed.
//
// It exists because staleness needs exactly those two facts and nothing else
// (see MembershipStale). Loading whole Service aggregates to reach them would
// pull every stop and frequency window of every member through the database
// for a comparison that looks at neither, and Service carries no updated_at
// of its own — it is a domain value, not a row.
type ServiceMembership struct {
	ServiceID string
	UpdatedAt time.Time
}

// PrerenderedIsochrone is a curated, already-computed isochrone a scenario
// ships with: authored once and served as-is, rather than resolved to a
// compiled graph and handed to the routing worker the way a dropped pin is
// (see RoutingJob).
//
// It is keyed on ScenarioSlug rather than a scenario id because the public
// isochrone surface already addresses a scenario that way, and scenarios.slug
// is unique — so the storage key and the request key are the same string.
//
// Result is the payload, held as raw JSON for RoutingJob.Result's reason: this
// repository neither produces nor interprets an isochrone, so a Go type here
// would be a second copy of someone else's output contract with no compiler
// able to check the two still agree. It is omitted from every list read, since
// payloads run 300-500KB.
//
// CompiledServiceIDs is the service-membership snapshot taken when the entry
// was curated, the same thing Job.CompiledServiceIDs records for a compiled
// graph. Comparing it against the scenario's live membership is what makes an
// entry "outdated" on read; nothing about that is persisted.
type PrerenderedIsochrone struct {
	ID           string     `json:"id"`
	ScenarioSlug string     `json:"scenario_slug"`
	Label        string     `json:"label"`
	Lat          float64    `json:"lat"`
	Lng          float64    `json:"lng"`
	BudgetMins   int        `json:"budget_mins"`
	Mode         TravelMode `json:"mode"`
	// Result is absent from a list read, which never selects the column.
	Result             json.RawMessage `json:"result,omitempty"`
	CompiledServiceIDs []string        `json:"compiled_service_ids,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}
