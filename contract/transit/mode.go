package transit

import (
	"slices"
	"strings"
)

type TravelMode string

const (
	TravelModeWalk    TravelMode = "walk"
	TravelModeBike    TravelMode = "bike"
	TravelModeDrive   TravelMode = "drive"
	TravelModeTransit TravelMode = "transit"
)

var travelModes = []TravelMode{
	TravelModeWalk,
	TravelModeBike,
	TravelModeDrive,
	TravelModeTransit,
}

func (m TravelMode) Valid() bool {
	return slices.Contains(travelModes, m)
}

func TravelModes() []TravelMode {
	return slices.Clone(travelModes)
}

func TravelModeList() string {
	names := make([]string, len(travelModes))
	for i, m := range travelModes {
		names[i] = string(m)
	}
	return strings.Join(names, ", ")
}
