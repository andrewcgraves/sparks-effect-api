package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.ErrorContext(r.Context(), "health: failed to write response", "error", err)
	}
}
