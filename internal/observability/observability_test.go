package observability

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestSetGlobalMetrics_RoundTrip(t *testing.T) {
	t.Parallel()

	orig := GetMetrics()
	t.Cleanup(func() { SetGlobalMetrics(orig) })

	m := &Metrics{}
	SetGlobalMetrics(m)
	if got := GetMetrics(); got != m {
		t.Fatal("expected GetMetrics to return the same instance")
	}
}

func TestInitMetrics_WithManualReader(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	orig := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(orig) })

	m, err := InitMetrics(t.Context(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Metrics")
	}
	if m.HTTPRequestCount == nil {
		t.Fatal("expected HTTPRequestCount to be initialized")
	}
	if m.HTTPRequestDuration == nil {
		t.Fatal("expected HTTPRequestDuration to be initialized")
	}

	// Verify a recording actually reaches the reader.
	m.HTTPRequestCount.Add(t.Context(), 1)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name == "http.server.request.count" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected metric to be collected")
	}
}

//nolint:paralleltest // manipulates global env and otel state
func TestInit_NoOTLPEndpoint(t *testing.T) {
	origEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() { _ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", origEndpoint) }() //nolint:usetesting // need to restore original value

	origMP := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(origMP) })

	res, err := Init(t.Context(), Config{ServiceName: "test", Environment: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil InitResult")
	}
	if res.Shutdown == nil {
		t.Fatal("expected non-nil Shutdown")
	}
	if res.MetricsHandler == nil {
		t.Fatal("expected non-nil MetricsHandler")
	}

	// Verify the metrics handler serves something.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	res.MetricsHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	_ = res.Shutdown(t.Context())
}

func TestNewSlogHandler_NonNil(t *testing.T) {
	t.Parallel()
	h := NewSlogHandler("test-service")
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}
