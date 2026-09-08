package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

var ErrJobNotFound = errors.New("routing job not found")

type IsochroneKey struct {
	CompileJobID string `json:"compile_job_id"`
	StationSlug  string `json:"station_slug"`
	Mode         string `json:"mode"`
	ContourMins  int    `json:"contour_mins"`
	DepartsOn    string `json:"departs_on,omitempty"`
}

type CachedIsochrone struct {
	Key       IsochroneKey    `json:"key"`
	Geometry  json.RawMessage `json:"geometry"`
	TilesetAt time.Time       `json:"tileset_at,omitempty"`
}

type WorkerStore interface {
	MarkRoutingJobRunning(ctx context.Context, id string) error
	SucceedRoutingJob(ctx context.Context, id string, result json.RawMessage) error
	FailRoutingJob(ctx context.Context, id, errMsg string) error
	GetIsochroneCache(ctx context.Context, keys []IsochroneKey) (map[IsochroneKey]json.RawMessage, error)
	PutIsochroneCache(ctx context.Context, entries []CachedIsochrone) error
}

func WorkerReady() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func WorkerMarkRunning(store WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.MarkRoutingJobRunning(r.Context(), r.PathValue("id")); err != nil {
			writeWorkerStoreError(r, w, "marking routing job running", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type jobSucceededBody struct {
	Result json.RawMessage `json:"result"`
}

func WorkerMarkSucceeded(store WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body jobSucceededBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := store.SucceedRoutingJob(r.Context(), r.PathValue("id"), body.Result); err != nil {
			writeWorkerStoreError(r, w, "marking routing job succeeded", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type jobFailedBody struct {
	Error string `json:"error"`
}

func WorkerMarkFailed(store WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body jobFailedBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := store.FailRoutingJob(r.Context(), r.PathValue("id"), body.Error); err != nil {
			writeWorkerStoreError(r, w, "marking routing job failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type cacheLookupRequest struct {
	Keys []IsochroneKey `json:"keys"`
}

type cacheLookupEntry struct {
	Key      IsochroneKey    `json:"key"`
	Geometry json.RawMessage `json:"geometry"`
}

type cacheLookupResponse struct {
	Entries []cacheLookupEntry `json:"entries"`
}

func WorkerCacheLookup(store WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body cacheLookupRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		found, err := store.GetIsochroneCache(r.Context(), body.Keys)
		if err != nil {
			writeInternalError(r.Context(), w, "reading isochrone cache", err)
			return
		}
		out := cacheLookupResponse{Entries: []cacheLookupEntry{}}
		for _, k := range body.Keys {
			if geom, ok := found[k]; ok {
				out.Entries = append(out.Entries, cacheLookupEntry{Key: k, Geometry: geom})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type cachePutRequest struct {
	Entries []CachedIsochrone `json:"entries"`
}

func WorkerCachePut(store WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body cachePutRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := store.PutIsochroneCache(r.Context(), body.Entries); err != nil {
			writeInternalError(r.Context(), w, "writing isochrone cache", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeWorkerStoreError(r *http.Request, w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrJobNotFound) {
		writeError(w, http.StatusNotFound, "routing job not found")
		return
	}
	writeInternalError(r.Context(), w, op, err)
}
