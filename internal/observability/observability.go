package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Config holds observability configuration.
type Config struct {
	ServiceName string
	Environment string
}

// InitResult holds the results of initializing observability.
type InitResult struct {
	Shutdown       func(context.Context) error
	MetricsHandler http.Handler
}

// Init initializes OpenTelemetry with OTLP/HTTP exporters for traces, metrics, and logs,
// plus a Prometheus exporter for /metrics scraping. OTLP is enabled when the standard
// OTEL_EXPORTER_OTLP_ENDPOINT env var is set; the SDK reads endpoint, headers, and TLS
// config from the standard OTEL_EXPORTER_OTLP_* env vars automatically.
//
//nolint:cyclop,funlen
func Init(ctx context.Context, cfg Config) (*InitResult, error) {
	promExporter, err := otelprom.New()
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(promExporter),
		)
		otel.SetMeterProvider(meterProvider)
		return &InitResult{
			Shutdown:       meterProvider.Shutdown,
			MetricsHandler: promhttp.Handler(),
		}, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		res, _ = resource.Merge(res, resource.NewWithAttributes(
			"",
			semconv.ServiceVersion(bi.Main.Version),
		))
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		_ = metricExporter.Shutdown(ctx)
		return nil, fmt.Errorf("create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(loggerProvider)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		var errs []error
		if err := tracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := meterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := loggerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			return fmt.Errorf("shutdown errors: %w", errors.Join(errs...))
		}
		return nil
	}

	return &InitResult{
		Shutdown:       shutdown,
		MetricsHandler: promhttp.Handler(),
	}, nil
}

// NewSlogHandler creates a new slog handler that exports logs via OTLP.
func NewSlogHandler(serviceName string) *otelslog.Handler {
	return otelslog.NewHandler(serviceName)
}
