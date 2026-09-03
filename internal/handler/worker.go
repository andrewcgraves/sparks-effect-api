package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ErrJobNotFound is what a WorkerStore returns when a routing-job write
// matched no row — missing, cascaded away, or (for MarkRunning) already
// terminal. The worker treats it as permanent: there is nothing left to
// record an answer against.
var ErrJobNotFound = errors.New("routing job not found")

// IsochroneKey is one cache lookup, matching the worker's store.IsochroneKey
// JSON. The two repositories share no Go code, so the tags are the contract.
//
// DepartsOn is the service date a transit row was computed for (SPA-269).
// Empty means NULL: walk/bike/drive have no service date, and a worker that
// has not started sending one still hits those rows.
type IsochroneKey struct {
	CompileJobID string `json:"compile_job_id"`
	StationSlug  string `json:"station_slug"`
	Mode         string `json:"mode"`
	ContourMins  int    `json:"contour_mins"`
	DepartsOn    string `json:"departs_on,omitempty"`
}

// CachedIsochrone is one cache write, matching the worker's store.CachedIsochrone.
type CachedIsochrone struct {
	Key       IsochroneKey    `json:"key"`
	Geometry  json.RawMessage `json:"geometry"`
	TilesetAt time.Time       `json:"tileset_at,omitempty"`
}

// WorkerStore is the slice of the repository the routing worker writes through
// (SPA-273). The worker used to run this SQL itself against a DATABASE_URL;
// these methods are that SQL, reached over authenticated HTTP so the database
// does not have to be on the private internet.
type WorkerStore interface {
	MarkRoutingJobRunning(ctx context.Context, id string) error
	SucceedRoutingJob(ctx context.Context, id string, result json.RawMessage) error
	FailRoutingJob(ctx context.Context, id, errMsg string) error
	GetIsochroneCache(ctx context.Context, keys []IsochroneKey) (map[IsochroneKey]json.RawMessage, error)
	PutIsochroneCache(ctx context.Context, entries []CachedIsochrone) error
}

// WorkerReady is GET /api/internal/worker: the startup ping. 204 means the
// token is accepted and the process may start consuming. The worker refuses
// to boot on anything else, because a worker that cannot record results would
// run indefinitely doing work it has nowhere to put.
func WorkerReady() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// WorkerMarkRunning is POST /api/internal/routing-jobs/{id}/running.
//
// A redelivered job is already running, and refusing to re-mark it would fail
// work that is otherwise recoverable, so the update matches any status short
// of terminal. Terminal statuses and a missing row both 404, which the worker
// reports as ErrJobNotFound — a late redelivery must not drag a finished job
// backwards.
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

// WorkerMarkSucceeded is POST /api/internal/routing-jobs/{id}/succeeded.
//
// An unconditional overwrite keyed by id: recomputing over an immutable graph
// produces the same answer, so a second write is indistinguishable from the
// first, which is what makes acking after this write safe.
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

// WorkerMarkFailed is POST /api/internal/routing-jobs/{id}/failed.
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

// WorkerCacheLookup is POST /api/internal/isochrone-cache/lookup.
//
// Keys with no row are simply absent from the response. The keys echoed back
// are the caller's own, so a uuid spelled with different casing still hits.
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

// WorkerCachePut is POST /api/internal/isochrone-cache.
//
// An entry whose key is already present is left alone: a conflict means
// another worker computed the same inputs into the same polygon.
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
