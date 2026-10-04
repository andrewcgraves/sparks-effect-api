package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

type RetentionReport struct {
	DryRun                     bool  `json:"dry_run"`
	IsochroneCacheRows         int64 `json:"isochrone_cache_rows"`
	IsochroneCacheSuperseded   int64 `json:"isochrone_cache_superseded"`
	IsochroneCacheStaleTransit int64 `json:"isochrone_cache_stale_transit"`
	RoutingJobResultsCleared   int64 `json:"routing_job_results_cleared"`
}

type RetentionStore interface {
	ApplyRetention(ctx context.Context, apply bool) (RetentionReport, error)
}

func Retention(store RetentionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apply, ok := readRetentionApply(w, r)
		if !ok {
			return
		}
		report, err := store.ApplyRetention(r.Context(), apply)
		if err != nil {
			writeInternalError(r.Context(), w, "retention", err)
			return
		}
		msg := "handler: retention dry-run"
		if apply {
			msg = "handler: retention applied"
		}
		slog.InfoContext(r.Context(), msg,
			"dry_run", report.DryRun,
			"isochrone_cache_rows", report.IsochroneCacheRows,
			"isochrone_cache_superseded", report.IsochroneCacheSuperseded,
			"isochrone_cache_stale_transit", report.IsochroneCacheStaleTransit,
			"routing_job_results_cleared", report.RoutingJobResultsCleared,
		)
		writeJSON(w, http.StatusOK, report)
	}
}

func readRetentionApply(w http.ResponseWriter, r *http.Request) (bool, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPatchUserBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false, false
		}
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return false, true
	}
	// A JSON null (or any non-object) is not an empty body. Unmarshalling null
	// into a struct succeeds and would otherwise dry-run.
	if bytes.TrimSpace(body)[0] != '{' {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	// A typo such as "aplly" must not decode as a missing field and dry-run.
	dec.DisallowUnknownFields()
	var req struct {
		Apply json.RawMessage `json:"apply"`
	}
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false, false
	}
	if len(bytes.TrimSpace(req.Apply)) == 0 {
		return false, true
	}
	// null unmarshals into a bool as false with no error, which would dry-run.
	if bytes.Equal(bytes.TrimSpace(req.Apply), []byte("null")) {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false, false
	}
	var apply bool
	if err := json.Unmarshal(req.Apply, &apply); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false, false
	}
	return apply, true
}
