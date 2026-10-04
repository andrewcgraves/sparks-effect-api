package metrics

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	routeUnmatched = "unmatched"
	methodOther    = "other"
)

// Few buckets on purpose: every route that is ever hit costs one series per
// bucket against the free-tier active-series budget (SPA-296). The edges
// bracket a cached read (~5 ms) to the 60 s write timeout.
var requestBuckets = []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 60}

// Compiles run in-process and range from milliseconds for one service to
// tens of seconds for a large scenario.
var compileBuckets = []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true,
	http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	http.MethodOptions: true,
}

// Metrics is nil-safe: a nil *Metrics records nothing, so a caller that was
// not handed one (most tests) needs no stand-in.
type Metrics struct {
	requests        metric.Int64Counter
	requestDuration metric.Float64Histogram
	compiles        metric.Int64Counter
	compileDuration metric.Float64Histogram
	backlogInFlight metric.Int64Gauge
	backlogFull     metric.Int64Counter
	rateLimited     metric.Int64Counter
}

func New(provider metric.MeterProvider) *Metrics {
	meter := provider.Meter("github.com/andrewcgraves/sparks-effect-api")
	// Instrument names are written the way Grafana Cloud's OTLP ingest wants
	// them: no `_total` on counters and no unit on histograms, because the
	// Prometheus translation adds both. http_requests lands as
	// http_requests_total, http_request_duration (unit "s") as
	// http_request_duration_seconds — the names the alert rules query.
	// The API returns an error only for an invalid name or option, which is a
	// programming error caught by the tests, and a usable no-op instrument
	// alongside it, so the errors are deliberately dropped.
	m := &Metrics{}
	m.requests, _ = meter.Int64Counter("http_requests",
		metric.WithDescription("HTTP requests answered, by route pattern, method and status class."))
	m.requestDuration, _ = meter.Float64Histogram("http_request_duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time to answer an HTTP request, by route pattern."),
		metric.WithExplicitBucketBoundaries(requestBuckets...))
	m.compiles, _ = meter.Int64Counter("compile_jobs",
		metric.WithDescription("Compile jobs finished, by kind and outcome."))
	m.compileDuration, _ = meter.Float64Histogram("compile_duration",
		metric.WithUnit("s"),
		metric.WithDescription("Time to run a compile job, by kind."),
		metric.WithExplicitBucketBoundaries(compileBuckets...))
	m.backlogInFlight, _ = meter.Int64Gauge("isochrone_backlog_inflight",
		metric.WithDescription("In-flight routing jobs, sampled on each isochrone enqueue."))
	m.backlogFull, _ = meter.Int64Counter("backlog_full",
		metric.WithDescription("Isochrone requests refused because the routing backlog was full."))
	m.rateLimited, _ = meter.Int64Counter("rate_limited",
		metric.WithDescription("Requests refused by a rate limiter, by limiter."))
	return m
}

// Request takes the mux pattern rather than the URL path so the route label
// is bounded by the routes registered, not by what callers type.
func (m *Metrics) Request(ctx context.Context, pattern, method string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	route := routeOf(pattern)
	if !knownMethods[method] {
		method = methodOther
	}
	m.requests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("route", route),
		attribute.String("method", method),
		attribute.String("status_class", statusClass(status)),
	))
	m.requestDuration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attribute.String("route", route)))
}

func (m *Metrics) Compile(ctx context.Context, kind, outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.compiles.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("outcome", outcome),
	))
	m.compileDuration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attribute.String("kind", kind)))
}

func (m *Metrics) BacklogInFlight(ctx context.Context, n int) {
	if m == nil {
		return
	}
	m.backlogInFlight.Record(ctx, int64(n))
}

func (m *Metrics) BacklogFull(ctx context.Context) {
	if m == nil {
		return
	}
	m.backlogFull.Add(ctx, 1)
}

// RateLimited answers the refusal hook ratelimit.Limit takes, so the
// limiter package needs no knowledge of metrics.
func (m *Metrics) RateLimited(limiter string) func(context.Context) {
	if m == nil {
		return nil
	}
	attrs := metric.WithAttributes(attribute.String("limiter", limiter))
	return func(ctx context.Context) { m.rateLimited.Add(ctx, 1, attrs) }
}

// A Go 1.22 pattern may lead with a method and a host ("GET /api/x"); the
// method has a label of its own.
func routeOf(pattern string) string {
	if pattern == "" {
		return routeUnmatched
	}
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = strings.TrimLeft(pattern[i+1:], " ")
	}
	return pattern
}

func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}
