package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
	"github.com/andrewcgraves/sparks-effect-api/internal/httpcache"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("handler: failed to write response", "error", err)
	}
}

// For a public read with no cheaper key than its own bytes: the published
// index, which moves on anyone's publish, unpublish or rename, and the
// prerendered list, whose outdated flags move with the scenario. Both are
// small, so encoding before deciding on a 304 costs little.
func writePublicJSON(w http.ResponseWriter, r *http.Request, tags httpcache.Tagger, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeInternalError(r.Context(), w, "encoding response", err)
		return
	}
	// The newline json.Encoder would have written, so the body is byte for
	// byte what writeJSON sends.
	body = append(body, '\n')
	etag := tags.ETag(string(body))
	if httpcache.NotModified(w, r, etag) {
		return
	}
	httpcache.MarkPublic(w, etag)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
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

const ValidationErrorCode = "validation"

type validationDetail struct {
	Faults fault.ValidationFaults `json:"faults"`
}

func writeUnprocessable(w http.ResponseWriter, err error) {
	var faults fault.ValidationFaults
	if errors.As(err, &faults) {
		writeErrorDetail(w, http.StatusUnprocessableEntity,
			ValidationErrorCode, faults.Error(), validationDetail{Faults: faults})
		return
	}
	var placement *transit.StopPlacementFault
	if errors.As(err, &placement) {
		writeErrorDetail(w, http.StatusUnprocessableEntity,
			StopPlacementErrorCode, placement.Error(), stopPlacementDetailFrom(placement))
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}
