package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-contract/store"
)

var ErrJobNotFound = errors.New("routing job not found")

type IsochroneKey = store.IsochroneKey
type CachedIsochrone = store.CachedIsochrone

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

func WorkerMarkRunning(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ws.MarkRoutingJobRunning(r.Context(), r.PathValue("id")); err != nil {
			writeWorkerStoreError(r, w, "marking routing job running", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func WorkerMarkSucceeded(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.JobSucceededBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := ws.SucceedRoutingJob(r.Context(), r.PathValue("id"), body.Result); err != nil {
			writeWorkerStoreError(r, w, "marking routing job succeeded", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func WorkerMarkFailed(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.JobFailedBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := ws.FailRoutingJob(r.Context(), r.PathValue("id"), body.Error); err != nil {
			writeWorkerStoreError(r, w, "marking routing job failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func WorkerCacheLookup(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.CacheLookupRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		found, err := ws.GetIsochroneCache(r.Context(), body.Keys)
		if err != nil {
			writeInternalError(r.Context(), w, "reading isochrone cache", err)
			return
		}
		out := store.CacheLookupResponse{Entries: []store.CacheLookupEntry{}}
		for _, k := range body.Keys {
			if geom, ok := found[k]; ok {
				out.Entries = append(out.Entries, store.CacheLookupEntry{Key: k, Geometry: geom})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func WorkerCachePut(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.CachePutRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := ws.PutIsochroneCache(r.Context(), body.Entries); err != nil {
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
