// Package traceid propagates a per-request trace identifier across the API's
// own logs and into the messages it hands to the routing worker, so a single
// isochrone can be followed through both services' logs in Grafana.
package traceid

import (
	"context"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

// Header is the request header a caller may set to supply its own trace id,
// and the header the API echoes it back on. A caller that already has a
// trace (the website's own request id, say) can hand it in so its logs and
// the API's line up; one that doesn't gets the id the API minted, in the
// response, so it can still correlate after the fact.
const Header = "X-Trace-Id"

type contextKey struct{}

var traceKey contextKey

// WithContext returns a context carrying id as the request's trace id.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey, id)
}

// FromContext returns the trace id attached to ctx, and false if none was.
func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(traceKey).(string)
	return id, ok
}

// Middleware attaches a trace id to every request: the one the caller sent in
// Header, or a freshly minted one when it sent none. Either way the id lands
// on the request context for downstream handlers and logging, and is echoed
// back in the response header.
//
// It must run outermost, ahead of anything that logs the request or enqueues
// work on its behalf (see server.New) — everything downstream depends on the
// trace id already being on the context by the time it runs.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if id == "" {
			// crypto/rand failing here is effectively unrecoverable elsewhere in
			// this codebase too (see ids.NewUUID's other callers); a request
			// proceeding with no trace id degrades observability, not
			// correctness, so it is not worth failing the request over.
			if generated, err := ids.NewUUID(); err == nil {
				id = generated
			}
		}
		if id != "" {
			w.Header().Set(Header, id)
			r = r.WithContext(WithContext(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}
