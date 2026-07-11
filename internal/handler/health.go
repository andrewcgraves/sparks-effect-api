// Package handler contains the HTTP handlers exposed by the API.
package handler

import (
	"encoding/json"
	"log"
	"net/http"
)

// Health responds with a basic liveness payload. It exists so the service
// has something to hit while it's being built out; SPA-13 will replace this
// with real readiness/liveness checks against the routing service.
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("health: failed to write response: %v", err)
	}
}
