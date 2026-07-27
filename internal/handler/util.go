package handler

import (
	"encoding/json"
	"log"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("handler: failed to write response: %v", err)
	}
}

// errorResponse is the shape of every error body the API returns.
//
// error is always present and is prose meant for a person. code and detail are
// the machine-readable half, present only where a client has to do something
// other than display the message — so their absence is itself the signal that
// there is nothing to branch on.
type errorResponse struct {
	Error  string `json:"error"`
	Code   string `json:"code,omitempty"`
	Detail any    `json:"detail,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeErrorCode is writeError plus a machine-readable code, for the handful
// of error conditions a client needs to branch on rather than just display.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

// writeErrorDetail is writeErrorCode plus a payload describing the failure in
// fields, for conditions where knowing *that* it failed is not enough to act —
// a client flagging the specific stop a placement fault names, say.
//
// detail's shape is fixed by the code, so a client reads it only after
// matching one it recognises.
func writeErrorDetail(w http.ResponseWriter, status int, code, msg string, detail any) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code, Detail: detail})
}
