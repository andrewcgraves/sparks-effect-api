package errorreporttest

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/andrewcgraves/sparks-effect-api/internal/errorreport"
)

// Report is one exported record, flattened to what a test asserts on.
type Report struct {
	Severity log.Severity
	Body     string
	Attrs    map[string]string
}

// Recorder holds what a test's Reporter exported, in-process, through the same
// SDK pipeline the OTLP exporter sits behind.
type Recorder struct {
	mu      sync.Mutex
	reports []Report
}

func New(t *testing.T) (*errorreport.Reporter, *Recorder) {
	t.Helper()
	rec := &Recorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(rec)))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return errorreport.New(provider), rec
}

func (r *Recorder) Reports() []Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Report(nil), r.reports...)
}

func (r *Recorder) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		attrs := map[string]string{}
		rec.WalkAttributes(func(kv attribute.KeyValue) bool {
			attrs[string(kv.Key)] = kv.Value.String()
			return true
		})
		r.reports = append(r.reports, Report{Severity: rec.Severity(), Body: rec.Body().String(), Attrs: attrs})
	}
	return nil
}

func (r *Recorder) Shutdown(context.Context) error   { return nil }
func (r *Recorder) ForceFlush(context.Context) error { return nil }
