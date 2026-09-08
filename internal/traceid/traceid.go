package traceid

import (
	"context"
	"net/http"

	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
)

const Header = "X-Trace-Id"

type contextKey struct{}

var traceKey contextKey

func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey, id)
}

func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(traceKey).(string)
	return id, ok
}

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
