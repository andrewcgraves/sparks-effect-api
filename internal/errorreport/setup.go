package errorreport

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

const serviceName = "sparks-effect-api"

// Setup sends reports as OTLP log records to the same Grafana Cloud gateway,
// with the same OTEL_EXPORTER_OTLP_* credentials, as internal/metrics. They
// land in Loki under service_name="sparks-effect-api", where an alert rule
// counts them; Grafana is the one place alerts come from (SPA-380).
//
// release is the commit the binary was built from. It rides on the resource as
// service.version, so every report says which build raised it.
//
// Disabled, it answers a nil *Reporter, which reports nothing.
func Setup(ctx context.Context, enabled bool, release string, lg *slog.Logger) (*Reporter, func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !enabled {
		lg.Info("OTEL_EXPORTER_OTLP_ENDPOINT not set; internal errors will not be reported")
		return nil, noop, nil
	}

	exporter, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, noop, fmt.Errorf("errorreport: building the OTLP exporter: %w", err)
	}
	// Same precedence as metrics.Setup: OTEL_RESOURCE_ATTRIBUTES' service.namespace
	// is what tells staging's reports from production's.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", release),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, noop, fmt.Errorf("errorreport: building the resource: %w", err)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)
	lg.Info("reporting internal errors over OTLP", "release", release)
	return New(provider), provider.Shutdown, nil
}
