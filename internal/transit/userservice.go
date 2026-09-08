package transit

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type UserService struct {
	ID                 string                `json:"id"`
	Slug               string                `json:"slug"`
	RouteID            string                `json:"route_id"`
	OwnerID            string                `json:"owner_id"`
	Name               string                `json:"name"`
	Description        string                `json:"description,omitempty"`
	Vehicle            VehicleParams         `json:"vehicle"`
	Stops              []ServiceStopPoint    `json:"stops"`
	FrequencyWindows   []FrequencyWindow     `json:"frequency_windows"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	BoardingWait       *BoardingWaitOverride `json:"boarding_wait,omitempty"`
	BoardingWaitPolicy string                `json:"boarding_wait_policy,omitempty"`
	BoardingWaitSecs   int                   `json:"boarding_wait_secs"`
	BoardingWaitSource string                `json:"boarding_wait_source,omitempty"`
}

func (s *UserService) ResolveBoardingWait(scenario *BoardingWaitOverride, global BoardingWaitPolicy) error {
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

type VehicleParams struct {
	MaxSpeedKMH     float64 `json:"max_speed_kmh"`
	AccelerationMS2 float64 `json:"acceleration_ms2"`
	DecelerationMS2 float64 `json:"deceleration_ms2"`
	DwellS          int     `json:"dwell_s"`
}

type ServiceStopPoint struct {
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Seq       int     `json:"seq"`
	ChainageM float64 `json:"chainage_m"`
	OffsetM   float64 `json:"offset_m"`
}

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

func (s *UserService) NormalizeStops() {
	for i := range s.Stops {
		s.Stops[i].Seq = i
	}
}

func (s *UserService) MintStopSlugs() {
	slugs := StopSlugs(*s)
	for i := range s.Stops {
		s.Stops[i].Slug = slugs[i]
	}
}

const maxSlugLen = 80

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
