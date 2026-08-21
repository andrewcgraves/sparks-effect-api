// Package handler contains the HTTP handlers exposed by the API.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Health responds with a basic liveness payload. It exists so the service
// has something to hit while it's being built out; SPA-13 will replace this
// with real readiness/liveness checks against the routing service.
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "oki"}); err != nil {
		slog.ErrorContext(r.Context(), "health: failed to write response", "error", err)
	}
}
