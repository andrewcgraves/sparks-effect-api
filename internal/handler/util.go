package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// decodeBody reads a JSON request body of type T under a byte cap, writing the
// error response itself and reporting false when it cannot. limit is the
// caller's own cap constant — they differ deliberately per endpoint, and each
// one's comment says why — so the cap stays at the call site and only the
// plumbing lives here.
//
// A body over the cap is a 413 rather than a 400: the payload may be perfectly
// well-formed, and telling a client its JSON is malformed when the real problem
// is size sends it looking in the wrong place.
func decodeBody[T any](w http.ResponseWriter, r *http.Request, limit int64) (T, bool) {
	return decodeJSONBody[T](w, r, limit, false)
}

// decodeBodyStrict is decodeBody with DisallowUnknownFields.
//
// It exists for the payloads where an unrecognised key is a typo the server can
// see and the client cannot: a misspelled physics key (cant__mm) or coordinate
// key (latitude) would otherwise decode to a zero value and pass validation as
// real data — tangent, level track, or a stop in the Gulf of Guinea — blaming
// the author for something the decoder knew about. Strictness is opt-in rather
// than the default because the lenient endpoints accept forward-compatible
// clients that may send fields this build does not know yet.
//
// Its 400 carries the decoder's own message, which names the offending field;
// that is the whole point of rejecting the key, so the prose differs from
// decodeBody's deliberately.
func decodeBodyStrict[T any](w http.ResponseWriter, r *http.Request, limit int64) (T, bool) {
	return decodeJSONBody[T](w, r, limit, true)
}

func decodeJSONBody[T any](w http.ResponseWriter, r *http.Request, limit int64, strict bool) (T, bool) {
	var zero T
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	var req T
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return zero, false
		}
		if strict {
			writeError(w, http.StatusBadRequest, "malformed request body: "+err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		}
		return zero, false
	}
	return req, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("handler: failed to write response", "error", err)
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
