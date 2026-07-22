package transit

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// UserService is a service authored by a user on top of an existing Route.
//
// It is deliberately separate from Service, the seeded aggregate compiled into
// the TransitGraph. Where Service references shared Station and VehicleType
// rows, a UserService is self-contained: stops are embedded points and vehicle
// params are inline, so a user can author a service without touching any shared
// catalog. Reconciling the two models is future work; keeping them apart leaves
// the seeded CAHSR compile path untouched.
type UserService struct {
	ID               string             `json:"id"`
	Slug             string             `json:"slug"`
	RouteID          string             `json:"route_id"`
	OwnerID          string             `json:"owner_id"`
	Name             string             `json:"name"`
	Description      string             `json:"description,omitempty"`
	Vehicle          VehicleParams      `json:"vehicle"`
	Stops            []ServiceStopPoint `json:"stops"`
	FrequencyWindows []FrequencyWindow  `json:"frequency_windows"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// VehicleParams are the rolling-stock capabilities a user sets on their own
// service, inline — there is no shared vehicle-type catalog for user services.
type VehicleParams struct {
	MaxSpeedKMH     float64 `json:"max_speed_kmh"`
	AccelerationMS2 float64 `json:"acceleration_ms2"`
	DecelerationMS2 float64 `json:"deceleration_ms2"`
	// DwellS is the default dwell at every stop, in seconds.
	DwellS int `json:"dwell_s"`
}

// ServiceStopPoint is one stop in a user service's pattern, stored embedded as
// a bare coordinate and label rather than a reference to a shared Station.
//
// Lat/Lng are the *snapped* position on the route's alignment, not what the
// author typed: SnapToRoute rewrites them on every write, so a stored stop has
// one coordinate rather than one for the author, one for the preview and one
// the compiler derives. ChainageM and OffsetM are that snap's other two
// products, persisted alongside it so nothing downstream has to re-derive them
// and risk disagreeing.
type ServiceStopPoint struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Seq  int     `json:"seq"`
	// ChainageM is the distance along the route line from its start to this
	// stop, in metres.
	ChainageM float64 `json:"chainage_m"`
	// OffsetM is how far the submitted position sat from the alignment — the
	// distance the stop moved when it was snapped. It is kept rather than
	// discarded because it is the uncertainty attached to this stop's position,
	// which SPA-109's co-located-stop merge needs at compile time (SPA-113).
	//
	// It describes one write, not the stop: a client that resubmits the
	// coordinate a previous write returned has moved the stop zero metres, so
	// this drops to 0 on re-save. Anything treating it as durable uncertainty
	// has to reckon with that.
	OffsetM float64 `json:"offset_m"`
}

// Validate reports whether the service is well-formed enough to persist.
// It checks caller-supplied fields only — ID, Slug, and OwnerID are assigned
// by the server, not the client.
func (s UserService) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(s.RouteID) == "" {
		return errors.New("route_id is required")
	}
	if len(s.Stops) < 2 {
		return errors.New("a service needs at least two stops")
	}
	for i, stop := range s.Stops {
		if strings.TrimSpace(stop.Name) == "" {
			return fmt.Errorf("stop %d: name is required", i)
		}
		if stop.Lat < -90 || stop.Lat > 90 {
			return fmt.Errorf("stop %d: lat must be between -90 and 90", i)
		}
		if stop.Lng < -180 || stop.Lng > 180 {
			return fmt.Errorf("stop %d: lng must be between -180 and 180", i)
		}
	}
	if s.Vehicle.MaxSpeedKMH <= 0 {
		return errors.New("vehicle.max_speed_kmh must be positive")
	}
	if s.Vehicle.AccelerationMS2 <= 0 {
		return errors.New("vehicle.acceleration_ms2 must be positive")
	}
	if s.Vehicle.DecelerationMS2 <= 0 {
		return errors.New("vehicle.deceleration_ms2 must be positive")
	}
	if s.Vehicle.DwellS < 0 {
		return errors.New("vehicle.dwell_s must not be negative")
	}
	for i, fw := range s.FrequencyWindows {
		if strings.TrimSpace(fw.StartTime) == "" {
			return fmt.Errorf("frequency window %d: start_time is required", i)
		}
		if strings.TrimSpace(fw.EndTime) == "" {
			return fmt.Errorf("frequency window %d: end_time is required", i)
		}
		if fw.HeadwayS <= 0 {
			return fmt.Errorf("frequency window %d: headway_s must be positive", i)
		}
	}
	return nil
}

// NormalizeStops renumbers Seq densely from 0 in the order the stops were
// given. Slice order is the source of truth for the stopping pattern; the
// client-supplied Seq is advisory and may be absent or repeated.
func (s *UserService) NormalizeStops() {
	for i := range s.Stops {
		s.Stops[i].Seq = i
	}
}

// maxSlugLen bounds a minted slug so it stays readable in a URL.
const maxSlugLen = 80

// Slugify converts a display name into a URL-safe slug, collapsing every run of
// non-alphanumeric characters to a single dash. It returns "service" when the
// name yields nothing usable, so a slug is always non-empty.
func Slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		return "service"
	}
	return slug
}
