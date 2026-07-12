package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type scenarioListItem struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type vehicleTypeSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Propulsion  string  `json:"propulsion"`
	MaxSpeedKMH float64 `json:"max_speed_kmh"`
}

type serviceSummary struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	VehicleType      vehicleTypeSummary        `json:"vehicle_type"`
	Direction        string                    `json:"direction"`
	StopCount        int                       `json:"stop_count"`
	FrequencyWindows []transit.FrequencyWindow `json:"frequency_windows"`
}

type scenarioDetail struct {
	ID          string            `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Status      string            `json:"status"`
	Routes      []transit.Route   `json:"routes"`
	Stations    []transit.Station `json:"stations"`
	Services    []serviceSummary  `json:"services"`
}

// Scenarios returns a handler that lists all scenarios.
func Scenarios(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		all := store.GetScenarios()
		items := make([]scenarioListItem, 0, len(all))
		for _, sc := range all {
			items = append(items, scenarioListItem{
				ID:          sc.ID,
				Slug:        sc.Slug,
				Name:        sc.Name,
				Description: sc.Description,
				Status:      sc.Status,
			})
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ScenarioBySlug returns a handler that fetches one scenario by slug with its
// routes, stations, and service summaries.
func ScenarioBySlug(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		sc, ok := store.GetScenarioBySlug(slug)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}

		routes := store.GetRoutesByScenario(sc.ID)
		stations := store.GetStationsByScenario(sc.ID)
		rawServices := store.GetServicesByScenario(sc.ID)

		summaries := make([]serviceSummary, 0, len(rawServices))
		for _, svc := range rawServices {
			vt, _ := store.GetVehicleTypeByID(svc.VehicleTypeID)
			summaries = append(summaries, serviceSummary{
				ID:   svc.ID,
				Name: svc.Name,
				VehicleType: vehicleTypeSummary{
					ID:          vt.ID,
					Name:        vt.Name,
					Propulsion:  vt.Propulsion,
					MaxSpeedKMH: vt.MaxSpeedKMH,
				},
				Direction:        svc.Direction,
				StopCount:        len(svc.Stops),
				FrequencyWindows: svc.FrequencyWindows,
			})
		}

		writeJSON(w, http.StatusOK, scenarioDetail{
			ID:          sc.ID,
			Slug:        sc.Slug,
			Name:        sc.Name,
			Description: sc.Description,
			Status:      sc.Status,
			Routes:      routes,
			Stations:    stations,
			Services:    summaries,
		})
	}
}

// ScenarioRoutes returns a handler that lists the routes for a scenario.
func ScenarioRoutes(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		sc, ok := store.GetScenarioBySlug(slug)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		writeJSON(w, http.StatusOK, store.GetRoutesByScenario(sc.ID))
	}
}

// ScenarioServices returns a handler that lists the services for a scenario.
func ScenarioServices(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		sc, ok := store.GetScenarioBySlug(slug)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		writeJSON(w, http.StatusOK, store.GetServicesByScenario(sc.ID))
	}
}

// ScenarioStations returns a handler that lists the stations for a scenario.
func ScenarioStations(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		sc, ok := store.GetScenarioBySlug(slug)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		writeJSON(w, http.StatusOK, store.GetStationsByScenario(sc.ID))
	}
}

// ScenarioTravelTimes returns a handler that returns the adjacent segment travel times for a scenario.
func ScenarioTravelTimes(store *transit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		tt, ok := store.GetTravelTimes(slug)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		writeJSON(w, http.StatusOK, tt)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("scenario handler: failed to write response: %v", err)
	}
}
