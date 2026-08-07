package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// AnalyticsEventStore is the write side the public ingestion endpoint needs.
type AnalyticsEventStore interface {
	InsertAnalyticsEvents(ctx context.Context, events []transit.AnalyticsEventRecord) error
}

// AnalyticsReadStore is the read side the admin aggregate endpoint needs.
type AnalyticsReadStore interface {
	AnalyticsSummary(ctx context.Context, since, until time.Time) (transit.AnalyticsSummary, error)
}

// maxAnalyticsBodyBytes caps a batch's request body. A few hundred small
// event objects stays well under this; anything larger is a client bug or an
// attack.
const maxAnalyticsBodyBytes = 1 << 16 // 64 KiB

// maxAnalyticsEventsPerBatch bounds how many events one request may carry, so
// a single oversized array cannot substitute for the volume rate limiting is
// meant to bound. The website's sink batches on a short flush interval or a
// small queue length (see src/analytics), so a well-behaved client never gets
// close to this.
const maxAnalyticsEventsPerBatch = 200

type analyticsIngestRequest struct {
	Events []json.RawMessage `json:"events"`
}

// analyticsEventEnvelope is the fixed part of every event: its type and, for
// page_view, the path it happened on. Everything else is type-specific and
// is never unmarshalled into a Go struct — it round-trips into the
// properties jsonb column exactly as the client sent it.
type analyticsEventEnvelope struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// IngestAnalyticsEvents returns the handler for POST /api/analytics/events —
// the public, unauthenticated sink the website's batching AnalyticsSink POSTs
// to (SPA-218).
//
// It always answers 204 for a well-formed batch, whether or not anything was
// actually stored: an unrecognised event type or a request that fails the bot
// check is dropped silently rather than reported, since telling a caller
// which of those happened would just teach a bad actor how to pass the
// filter. Malformed JSON, an empty batch, and an oversized batch are the only
// things that answer with an error — those are client bugs worth surfacing,
// not abuse to stay quiet about.
func IngestAnalyticsEvents(store AnalyticsEventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeAnalyticsIngestRequest(w, r)
		if !ok {
			return
		}

		if looksLikeBot(r.Header.Get("User-Agent")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		records := make([]transit.AnalyticsEventRecord, 0, len(req.Events))
		for _, raw := range req.Events {
			var env analyticsEventEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			if !transit.AnalyticsEventTypes[env.Type] {
				continue
			}
			id, err := ids.NewUUID()
			if err != nil {
				writeInternalError(r.Context(), w, "minting analytics event id", err)
				return
			}
			records = append(records, transit.AnalyticsEventRecord{
				ID:         id,
				Type:       env.Type,
				Path:       env.Path,
				Properties: json.RawMessage(raw),
			})
		}

		if err := store.InsertAnalyticsEvents(r.Context(), records); err != nil {
			writeInternalError(r.Context(), w, "storing analytics events", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeAnalyticsIngestRequest(w http.ResponseWriter, r *http.Request) (analyticsIngestRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAnalyticsBodyBytes)

	var req analyticsIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return analyticsIngestRequest{}, false
		}
		writeError(w, http.StatusBadRequest, "malformed request body")
		return analyticsIngestRequest{}, false
	}

	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "at least one event is required")
		return analyticsIngestRequest{}, false
	}
	if len(req.Events) > maxAnalyticsEventsPerBatch {
		writeError(w, http.StatusBadRequest, "too many events in one batch")
		return analyticsIngestRequest{}, false
	}
	return req, true
}

// botUserAgentSubstrings are lowercase substrings that flag a request as a
// bot or script rather than a browser. This is deliberately basic (SPA-218
// scopes out anything more, e.g. a JS challenge): it stops crawlers, HTTP
// libraries' default user agents, and known feed/preview fetchers from
// polluting product-usage stats, not a determined script that spoofs a
// browser's user agent.
var botUserAgentSubstrings = []string{
	"bot", "spider", "crawl", "slurp",
	"curl/", "wget", "python-requests", "python-urllib", "scrapy",
	"headlesschrome", "phantomjs", "go-http-client", "okhttp",
	"postmanruntime", "libwww-perl", "java/",
}

// looksLikeBot reports whether ua identifies a non-browser client. An empty
// user agent is treated the same way: every real browser sends one, so its
// absence is itself the signal.
func looksLikeBot(ua string) bool {
	if strings.TrimSpace(ua) == "" {
		return true
	}
	lower := strings.ToLower(ua)
	for _, s := range botUserAgentSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// defaultAnalyticsWindow is how far back GetAnalyticsSummary looks when the
// caller does not specify since/until.
const defaultAnalyticsWindow = 30 * 24 * time.Hour

// maxAnalyticsWindow bounds since..until so an admin cannot accidentally ask
// for an aggregate over the table's entire history and turn a dashboard
// click into a full scan.
const maxAnalyticsWindow = 366 * 24 * time.Hour

// AnalyticsSummary returns the handler for GET /api/admin/analytics/summary:
// event counts, a daily time series, and top pages, aggregated over
// [since, until). Admin-only, gated at registration by RequireAdmin the same
// way every other admin route is.
//
// since and until are optional query parameters (RFC3339 or YYYY-MM-DD);
// unset defaults to the trailing 30 days ending now.
func AnalyticsSummary(store AnalyticsReadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		until, ok := parseAnalyticsTimeParam(w, r, "until", time.Now().UTC())
		if !ok {
			return
		}
		since, ok := parseAnalyticsTimeParam(w, r, "since", until.Add(-defaultAnalyticsWindow))
		if !ok {
			return
		}
		if !since.Before(until) {
			writeError(w, http.StatusBadRequest, "since must be before until")
			return
		}
		if until.Sub(since) > maxAnalyticsWindow {
			writeError(w, http.StatusBadRequest, "since..until may span at most 366 days")
			return
		}

		summary, err := store.AnalyticsSummary(r.Context(), since, until)
		if err != nil {
			writeInternalError(r.Context(), w, "loading analytics summary", err)
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

func parseAnalyticsTimeParam(w http.ResponseWriter, r *http.Request, name string, fallback time.Time) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true
	}
	writeError(w, http.StatusBadRequest, name+" must be RFC3339 or YYYY-MM-DD")
	return time.Time{}, false
}
