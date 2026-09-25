package handler

import (
	"context"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

type PublishedServiceStore interface {
	ListPublishedServiceSummaries(ctx context.Context) ([]transit.PublishedServiceSummary, error)
}

func PublishedServices(store PublishedServiceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// No identity is read. The index answers the same for every caller,
		// owner included, so an owner's unpublished drafts are no more listed
		// for them than for anyone else (ADR-0005).
		items, err := store.ListPublishedServiceSummaries(r.Context())
		if err != nil {
			writeInternalError(r.Context(), w, "listing published services", err)
			return
		}
		if items == nil {
			items = []transit.PublishedServiceSummary{}
		}
		writeJSON(w, http.StatusOK, items)
	}
}
