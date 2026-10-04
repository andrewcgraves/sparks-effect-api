package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

// A hung database or broker must not pin /readyz. A second is long enough
// for a local ping and short enough that a stuck check fails the probe.
var readyTimeout = time.Second

const (
	componentOK          = "ok"
	componentDisabled    = "disabled"
	componentUnavailable = "unavailable"
)

func Ready(db, broker Pinger, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		var pg, mq string
		var wg sync.WaitGroup
		wg.Go(func() { pg = checkComponent(ctx, log, "postgres", db) })
		wg.Go(func() { mq = checkComponent(ctx, log, "amqp", broker) })
		wg.Wait()

		status := http.StatusOK
		if pg == componentUnavailable || mq == componentUnavailable {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"postgres": pg, "amqp": mq})
	}
}

func checkComponent(ctx context.Context, log *slog.Logger, name string, p Pinger) string {
	// A nil Pinger is a component this deployment runs without — no
	// DATABASE_URL or no AMQP_URL. That is a supported mode (local dev runs
	// read-only from the embedded store), not an outage, so it must not fail
	// the check.
	if p == nil {
		return componentDisabled
	}
	if err := p.Ping(ctx); err != nil {
		log.WarnContext(ctx, "readyz: component unavailable", "component", name, "error", err)
		return componentUnavailable
	}
	return componentOK
}
