package metrics

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const serviceName = "sparks-effect-api"

// Setup pushes to Grafana Cloud's OTLP gateway rather than being scraped: the
// API runs on Railway, outside the cluster Alloy scrapes. The exporter reads
// the standard OTEL_EXPORTER_OTLP_* variables itself (endpoint, headers,
// timeout), so the credentials Grafana Cloud's OpenTelemetry card hands out
// are pasted in as they come.
//
// Disabled, it answers a nil *Metrics, which records nothing.
func Setup(ctx context.Context, enabled bool, lg *slog.Logger) (*Metrics, func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !enabled {
		lg.Info("OTEL_EXPORTER_OTLP_ENDPOINT not set; metrics will not be exported")
		return nil, noop, nil
	}

	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, noop, fmt.Errorf("metrics: building the OTLP exporter: %w", err)
	}
	// Later options win, so OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
	// override the default name. Staging and production are told apart by
	// service.namespace, which Grafana folds into the job label.
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", serviceName)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, noop, fmt.Errorf("metrics: building the resource: %w", err)
	}

	// The default 60 s interval matches the cluster's scrape interval, and
	// pushing more often would buy resolution the alerts do not use.
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	lg.Info("exporting metrics over OTLP")
	return New(provider), provider.Shutdown, nil
}
