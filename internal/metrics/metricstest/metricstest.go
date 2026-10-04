package metricstest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/andrewcgraves/sparks-effect-api/internal/metrics"
)

// Reader collects what a test's Metrics recorded, in-process, the same data
// the OTLP exporter would have pushed.
type Reader struct {
	r *sdkmetric.ManualReader
}

func New(t *testing.T) (*metrics.Metrics, *Reader) {
	t.Helper()
	r := sdkmetric.NewManualReader()
	return metrics.New(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))), &Reader{r: r}
}

func (r *Reader) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func (r *Reader) find(t *testing.T, name string) (metricdata.Metrics, bool) {
	t.Helper()
	for _, sm := range r.collect(t).ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// Count is a counter's value, or a histogram's observation count, for the
// series whose attributes are exactly attrs. Zero when nothing was recorded.
func (r *Reader) Count(t *testing.T, name string, attrs ...attribute.KeyValue) int64 {
	t.Helper()
	m, ok := r.find(t, name)
	if !ok {
		return 0
	}
	want := attribute.NewSet(attrs...)
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, p := range d.DataPoints {
			if p.Attributes.Equals(&want) {
				return p.Value
			}
		}
	case metricdata.Histogram[float64]:
		for _, p := range d.DataPoints {
			if p.Attributes.Equals(&want) {
				return int64(p.Count)
			}
		}
	default:
		t.Fatalf("%s: Count does not read %T", name, m.Data)
	}
	return 0
}

// Gauge is a gauge's last value and whether it was ever set.
func (r *Reader) Gauge(t *testing.T, name string) (int64, bool) {
	t.Helper()
	m, ok := r.find(t, name)
	if !ok {
		return 0, false
	}
	d, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s: Gauge does not read %T", name, m.Data)
	}
	if len(d.DataPoints) == 0 {
		return 0, false
	}
	return d.DataPoints[0].Value, true
}

// Series is every attribute set name was recorded under, for asserting that
// a label stays inside a bounded set.
func (r *Reader) Series(t *testing.T, name string) []attribute.Set {
	t.Helper()
	m, ok := r.find(t, name)
	if !ok {
		return nil
	}
	var sets []attribute.Set
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		for _, p := range d.DataPoints {
			sets = append(sets, p.Attributes)
		}
	case metricdata.Histogram[float64]:
		for _, p := range d.DataPoints {
			sets = append(sets, p.Attributes)
		}
	}
	return sets
}
