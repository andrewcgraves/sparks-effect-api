package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("handler: failed to write response", "error", err)
	}
}

type errorResponse struct {
	Error  string `json:"error"`
	Code   string `json:"code,omitempty"`
	Detail any    `json:"detail,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

func writeErrorDetail(w http.ResponseWriter, status int, code, msg string, detail any) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code, Detail: detail})
}
