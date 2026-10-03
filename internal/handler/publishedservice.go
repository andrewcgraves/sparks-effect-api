package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type PublishedServiceStore interface {
	ListPublishedServiceSummaries(ctx context.Context, after *transit.PublishedIndexKey, limit int) (transit.PublishedIndexPage, error)
}

const (
	publishedIndexDefaultLimit = 50
	publishedIndexMaxLimit     = 100
)

type publishedIndexPage struct {
	Items      []transit.PublishedServiceSummary `json:"items"`
	NextCursor *string                           `json:"next_cursor"`
}

func PublishedServices(store PublishedServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// No identity is read. The index answers the same for every caller,
		// owner included, so an owner's unpublished drafts are no more listed
		// for them than for anyone else (ADR-0005). The cursor is built from
		// the index's own order alone, so it is no more personal than the
		// page it continues.
		q := r.URL.Query()

		// Neither parameter: the bare array of everything (a limit of zero),
		// which a website built before SPA-434 still reads. Drop it once the
		// website's production tag reads pages.
		paged := q.Has("limit") || q.Has("cursor")
		var after *transit.PublishedIndexKey
		limit := 0
		if paged {
			limit = publishedIndexDefaultLimit
			if q.Has("limit") {
				n, err := strconv.Atoi(q.Get("limit"))
				if err != nil || n < 1 {
					writeError(w, http.StatusBadRequest, "limit must be a positive integer")
					return
				}
				limit = min(n, publishedIndexMaxLimit)
			}
			// An empty cursor is the first page, so a client can always send one.
			if c := q.Get("cursor"); c != "" {
				key, err := decodePublishedIndexCursor(c)
				if err != nil {
					writeError(w, http.StatusBadRequest, "malformed cursor")
					return
				}
				after = &key
			}
		}

		page, err := store.ListPublishedServiceSummaries(r.Context(), after, limit)
		if err != nil {
			writeInternalError(r.Context(), w, "listing published services", err)
			return
		}
		items := page.Items
		if items == nil {
			items = []transit.PublishedServiceSummary{}
		}
		if !paged {
			writeJSON(w, http.StatusOK, items)
			return
		}
		out := publishedIndexPage{Items: items}
		if page.Next != nil {
			c := encodePublishedIndexCursor(*page.Next)
			out.NextCursor = &c
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// The cursor is opaque to clients but not secret: URL-safe base64 of the last
// item's first-publish time and slug. The time goes first because it cannot
// contain the separator, so a slug that does still decodes.
func encodePublishedIndexCursor(k transit.PublishedIndexKey) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(k.FirstPublishedAt.UTC().Format(time.RFC3339Nano) + "|" + k.Slug))
}

func decodePublishedIndexCursor(c string) (transit.PublishedIndexKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return transit.PublishedIndexKey{}, err
	}
	at, slug, ok := strings.Cut(string(raw), "|")
	if !ok {
		return transit.PublishedIndexKey{}, errors.New("cursor has no separator")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return transit.PublishedIndexKey{}, err
	}
	return transit.PublishedIndexKey{FirstPublishedAt: t, Slug: slug}, nil
}
