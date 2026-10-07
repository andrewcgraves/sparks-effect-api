package transit

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

type UserService struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	// RouteSlug and RouteName are copied from the route this service points at.
	// A write names that route by slug, and a service that has never compiled
	// has no graph to recover the slug from.
	RouteID            string                `json:"route_id"`
	RouteSlug          string                `json:"route_slug"`
	RouteName          string                `json:"route_name"`
	OwnerID            string                `json:"owner_id"`
	Name               string                `json:"name"`
	Subtext            string                `json:"subtext,omitempty"`
	Description        string                `json:"description,omitempty"`
	Vehicle            VehicleParams         `json:"vehicle"`
	Stops              []ServiceStopPoint    `json:"stops"`
	FrequencyWindows   []FrequencyWindow     `json:"frequency_windows"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	PublishedAt        *time.Time            `json:"-"`
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

const (
	MaxSubtextChars     = 140
	MaxDescriptionChars = 4000
)

func (s UserService) Validate() error {
	var faults fault.ValidationFaults
	if strings.TrimSpace(s.Name) == "" {
		faults = append(faults, fault.Whole("name", fault.RuleRequired, "name is required"))
	}
	// Prose is bounded in characters rather than bytes, because characters are
	// what an author sees counted in the form. Both limits are generous for
	// their shape — a one-line descriptor, a few paragraphs — and exist so the
	// 1 MiB request cap is not the only thing between a public page and a
	// megabyte of text.
	if utf8.RuneCountInString(s.Subtext) > MaxSubtextChars {
		faults = append(faults, fault.Whole("subtext", fault.RuleMaxLength,
			fmt.Sprintf("subtext must be at most %d characters", MaxSubtextChars)))
	}
	if utf8.RuneCountInString(s.Description) > MaxDescriptionChars {
		faults = append(faults, fault.Whole("description", fault.RuleMaxLength,
			fmt.Sprintf("description must be at most %d characters", MaxDescriptionChars)))
	}
	if strings.TrimSpace(s.RouteID) == "" {
		faults = append(faults, fault.Whole("route_id", fault.RuleRequired, "route_id is required"))
	}
	if len(s.Stops) < 2 {
		faults = append(faults, fault.Whole("stops", fault.RuleMinCount, "a service needs at least two stops"))
	}
	for i, stop := range s.Stops {
		if strings.TrimSpace(stop.Name) == "" {
			faults = append(faults, fault.At("stops.name", i, fault.RuleRequired,
				fmt.Sprintf("stop %d: name is required", i)))
		}
		if stop.Lat < -90 || stop.Lat > 90 {
			faults = append(faults, fault.At("stops.lat", i, fault.RuleRange,
				fmt.Sprintf("stop %d: lat must be between -90 and 90", i)))
		}
		if stop.Lng < -180 || stop.Lng > 180 {
			faults = append(faults, fault.At("stops.lng", i, fault.RuleRange,
				fmt.Sprintf("stop %d: lng must be between -180 and 180", i)))
		}
	}
	if s.Vehicle.MaxSpeedKMH <= 0 {
		faults = append(faults, fault.Whole("vehicle.max_speed_kmh", fault.RulePositive,
			"vehicle.max_speed_kmh must be positive"))
	}
	if s.Vehicle.AccelerationMS2 <= 0 {
		faults = append(faults, fault.Whole("vehicle.acceleration_ms2", fault.RulePositive,
			"vehicle.acceleration_ms2 must be positive"))
	}
	if s.Vehicle.DecelerationMS2 <= 0 {
		faults = append(faults, fault.Whole("vehicle.deceleration_ms2", fault.RulePositive,
			"vehicle.deceleration_ms2 must be positive"))
	}
	if s.Vehicle.DwellS < 0 {
		faults = append(faults, fault.Whole("vehicle.dwell_s", fault.RuleNonNegative,
			"vehicle.dwell_s must not be negative"))
	}
	faults = append(faults, validateFrequencyWindows(s.FrequencyWindows)...)
	return faults.Err()
}

type windowSpan struct {
	index      int
	start, end int
}

func validateFrequencyWindows(windows []FrequencyWindow) fault.ValidationFaults {
	var faults fault.ValidationFaults
	var spans []windowSpan
	for i, fw := range windows {
		start, startFault := parseWindowTime("start_time", i, fw.StartTime)
		if startFault != nil {
			faults = append(faults, *startFault)
		}
		end, endFault := parseWindowTime("end_time", i, fw.EndTime)
		if endFault != nil {
			faults = append(faults, *endFault)
		}
		if fw.HeadwayS <= 0 {
			faults = append(faults, fault.At("frequency_windows.headway_s", i, fault.RulePositive,
				fmt.Sprintf("frequency window %d: headway_s must be positive", i)))
		}
		if startFault != nil || endFault != nil {
			continue
		}
		// Windows crossing midnight are not supported: a late-night service
		// is authored as two windows, one each side of 00:00.
		if end <= start {
			faults = append(faults, fault.At("frequency_windows.end_time", i, fault.RuleOrder,
				fmt.Sprintf("frequency window %d: end_time must be after start_time", i)))
			continue
		}
		// Spans are half-open, so a window ending at 10:00 and one starting
		// at 10:00 meet without overlapping. The fault goes on the later
		// window in the list, naming the first earlier one it collides with.
		for _, prev := range spans {
			if start < prev.end && prev.start < end {
				faults = append(faults, fault.At("frequency_windows", i, fault.RuleOverlap,
					fmt.Sprintf("frequency window %d: overlaps frequency window %d", i, prev.index)))
				break
			}
		}
		spans = append(spans, windowSpan{index: i, start: start, end: end})
	}
	return faults
}

func parseWindowTime(name string, i int, value string) (int, *fault.ValidationFault) {
	field := "frequency_windows." + name
	if strings.TrimSpace(value) == "" {
		f := fault.At(field, i, fault.RuleRequired, fmt.Sprintf("frequency window %d: %s is required", i, name))
		return 0, &f
	}
	minutes, ok := minutesOfDay(value)
	if !ok {
		f := fault.At(field, i, fault.RuleFormat,
			fmt.Sprintf("frequency window %d: %s must be HH:MM between 00:00 and 23:59", i, name))
		return 0, &f
	}
	return minutes, nil
}

func minutesOfDay(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	digits := [4]byte{value[0], value[1], value[3], value[4]}
	for _, d := range digits {
		if d < '0' || d > '9' {
			return 0, false
		}
	}
	hour := int(digits[0]-'0')*10 + int(digits[1]-'0')
	minute := int(digits[2]-'0')*10 + int(digits[3]-'0')
	if hour > 23 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
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
