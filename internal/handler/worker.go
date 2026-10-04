package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-contract/store"
)

var ErrJobNotFound = errors.New("routing job not found")

// A chain's result and its cache put carry the same Valhalla polygons: the put
// holds the egress ones that missed, the result all of them plus the origin. So
// one limit serves both, and a put is never refused for a job whose result was
// accepted. The largest real chain, testdata/cache-put-sj-240-bike.json (12
// egress polygons, 438,897 bytes) and its 499,685-byte result, sit under a
// sixteenth of it. Refusing a put only costs a cache write; refusing a result
// fails the job, which is why this is generous rather than tight.
const maxWorkerPolygonBodyBytes = 8 << 20

// A key is a uuid, a slug, a mode, a contour, a date and a budget: under 300
// bytes, so this admits every lookup maxWorkerCacheBatch allows.
const maxWorkerLookupBodyBytes = 1 << 20

// One key per station the chain reaches by transit, so a job asks for at most
// one per node in its graph. ca-hsr has 15; every rail station in California is
// a few hundred. A lookup or put over the cap is refused, which the worker
// treats as a cache miss or an unwritten row, never a failed job.
const maxWorkerCacheBatch = 1000

// The worker's failure message is one wrapped error string.
const maxWorkerFailedBodyBytes = 64 << 10

type IsochroneKey = store.IsochroneKey
type CachedIsochrone = store.CachedIsochrone
type JobSucceededBody = store.JobSucceededBody

type WorkerStore interface {
	MarkRoutingJobRunning(ctx context.Context, id string) error
	SucceedRoutingJob(ctx context.Context, id string, done JobSucceededBody) error
	FailRoutingJob(ctx context.Context, id, errMsg string) error
	GetIsochroneCache(ctx context.Context, keys []IsochroneKey) (map[IsochroneKey]CachedIsochrone, error)
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
		// tileset_at and reusable_until are what let a repeat request be
		// answered from this job (SPA-331). An older worker sends neither, and
		// its jobs are simply never reused.
		var body JobSucceededBody
		if !decodeWorkerBody(w, r, maxWorkerPolygonBodyBytes, &body) {
			return
		}
		if err := ws.SucceedRoutingJob(r.Context(), r.PathValue("id"), body); err != nil {
			writeWorkerStoreError(r, w, "marking routing job succeeded", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func WorkerMarkFailed(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.JobFailedBody
		if !decodeWorkerBody(w, r, maxWorkerFailedBodyBytes, &body) {
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
		if !decodeWorkerBody(w, r, maxWorkerLookupBodyBytes, &body) {
			return
		}
		if len(body.Keys) > maxWorkerCacheBatch {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("keys: at most %d per lookup", maxWorkerCacheBatch))
			return
		}
		found, err := ws.GetIsochroneCache(r.Context(), body.Keys)
		if err != nil {
			writeInternalError(r.Context(), w, "reading isochrone cache", err)
			return
		}
		// tileset_at is what lets the worker refuse a row cut from tiles it is
		// no longer serving (SPA-325). A NULL stamp is omitted, and the worker
		// reads an absent stamp as a miss.
		out := store.CacheLookupResponse{Entries: []store.CacheLookupEntry{}}
		for _, k := range body.Keys {
			if row, ok := found[k]; ok {
				out.Entries = append(out.Entries, store.CacheLookupEntry{
					Key: k, Geometry: row.Geometry, TilesetAt: row.TilesetAt,
				})
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func WorkerCachePut(ws WorkerStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body store.CachePutRequest
		if !decodeWorkerBody(w, r, maxWorkerPolygonBodyBytes, &body) {
			return
		}
		if len(body.Entries) > maxWorkerCacheBatch {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("entries: at most %d per put", maxWorkerCacheBatch))
			return
		}
		if err := ws.PutIsochroneCache(r.Context(), body.Entries); err != nil {
			writeInternalError(r.Context(), w, "writing isochrone cache", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeWorkerBody(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

func writeWorkerStoreError(r *http.Request, w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrJobNotFound) {
		writeError(w, http.StatusNotFound, "routing job not found")
		return
	}
	writeInternalError(r.Context(), w, op, err)
}
