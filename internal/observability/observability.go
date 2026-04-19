package observability

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Config holds observability configuration
type Config struct {
	OTLPEndpoint string
	OTLPInsecure bool
	ServiceName  string
	Environment  string
}

// InitResult holds the results of initializing observability.
type InitResult struct {
	Shutdown       func(context.Context) error
	MetricsHandler http.Handler
}

// Init initializes OpenTelemetry with OTLP exporters for traces, metrics, and logs,
// plus a Prometheus exporter for /metrics scraping.
func Init(ctx context.Context, cfg Config) (*InitResult, error) {
	// Always set up Prometheus exporter (works without OTLP)
	promExporter, err := otelprom.New()
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	if cfg.OTLPEndpoint == "" {
		// No OTLP endpoint; set up meter provider with Prometheus only
		meterProvider := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(promExporter),
		)
		otel.SetMeterProvider(meterProvider)
		return &InitResult{
			Shutdown:       meterProvider.Shutdown,
			MetricsHandler: promhttp.Handler(),
		}, nil
	}

	// Build resource with service info
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

	// Add build info if available
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		res, _ = resource.Merge(res, resource.NewWithAttributes(
			"",
			semconv.ServiceVersion(bi.Main.Version),
		))
	}

	// Initialize trace exporter
	var traceExporter sdktrace.SpanExporter
	if cfg.OTLPInsecure {
		traceExporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
	} else {
		traceExporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)

	// Initialize metric exporter
	var metricExporter sdkmetric.Exporter
	if cfg.OTLPInsecure {
		metricExporter, err = otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlpmetricgrpc.WithInsecure(),
		)
	} else {
		metricExporter, err = otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		)
	}
	if err != nil {
		_ = traceExporter.Shutdown(ctx) //nolint:errcheck
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	// Initialize log exporter
	var logExporter sdklog.Exporter
	if cfg.OTLPInsecure {
		logExporter, err = otlploggrpc.New(ctx,
			otlploggrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlploggrpc.WithInsecure(),
		)
	} else {
		logExporter, err = otlploggrpc.New(ctx,
			otlploggrpc.WithEndpoint(cfg.OTLPEndpoint),
		)
	}
	if err != nil {
		_ = traceExporter.Shutdown(ctx)  //nolint:errcheck
		_ = metricExporter.Shutdown(ctx) //nolint:errcheck
		return nil, fmt.Errorf("create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(loggerProvider)

	// Set up text map propagation (W3C trace context)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Return shutdown function that closes all exporters
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
			return fmt.Errorf("shutdown errors: %v", errs)
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
