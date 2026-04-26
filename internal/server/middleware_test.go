package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jchevertonwynne/ssanta/internal/observability"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestRecoverPanic_RecoversAndWrites500(t *testing.T) {
	t.Parallel()
	handler := RecoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "internal error") {
		t.Fatalf("expected internal error body, got %q", body)
	}
}

func TestRecoverPanic_NoPanic_PassesThrough(t *testing.T) {
	t.Parallel()
	handler := RecoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSecurityHeaders_SetsExpectedHeaders(t *testing.T) {
	t.Parallel()
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	headers := []struct {
		key  string
		want string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, h := range headers {
		got := rec.Header().Get(h.key)
		if got == "" {
			t.Fatalf("missing header %s", h.key)
		}
		if !strings.Contains(got, h.want) {
			t.Fatalf("header %s = %q, want to contain %q", h.key, got, h.want)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("missing Content-Security-Policy header")
	}
}

func TestMaxRequestBody_AllowsSmallBody(t *testing.T) {
	t.Parallel()
	handler := MaxRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("small body"))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMaxRequestBody_RejectsLargeBody(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			// MaxBytesReader returns an error that surfaces as a request entity too large.
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	wrapped := MaxRequestBody(handler)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(make([]byte, 2<<20)))
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestChain_ComposesMiddlewareInOrder(t *testing.T) {
	t.Parallel()
	var order []string

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1-before")
			next.ServeHTTP(w, r)
			order = append(order, "m1-after")
		})
	}
	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2-before")
			next.ServeHTTP(w, r)
			order = append(order, "m2-after")
		})
	}

	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), m1, m2)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	want := []string{"m1-before", "m2-before", "handler", "m2-after", "m1-after"}
	if len(order) != len(want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected order %v, got %v", want, order)
		}
	}
}

func TestBearerAuthMiddleware_ValidToken_CallsNext(t *testing.T) {
	t.Parallel()
	token := "valid-token"
	handler := bearerAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), token)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestBearerAuthMiddleware_InvalidToken_Returns403(t *testing.T) {
	t.Parallel()
	handler := bearerAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}), "valid-token")

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestBearerAuthMiddleware_MissingHeader_Returns401(t *testing.T) {
	t.Parallel()
	handler := bearerAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}), "valid-token")

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

//nolint:paralleltest // manipulates global otel state
func TestTracingMiddleware_CreatesSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	orig := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(orig) })

	handler := TracingMiddleware("test-service")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test-path", nil)
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /test-path" {
		t.Fatalf("expected span name %q, got %q", "GET /test-path", span.Name)
	}
	if span.SpanKind != trace.SpanKindServer {
		t.Fatalf("expected server span kind, got %v", span.SpanKind)
	}
	if span.Status.Code != codes.Unset {
		t.Fatalf("expected unset status for 200, got %v", span.Status.Code)
	}
	wantAttr := map[attribute.Key]string{
		"http.method": "GET",
		"http.target": "/test-path",
	}
	gotAttr := make(map[string]string, len(span.Attributes))
	for _, a := range span.Attributes {
		gotAttr[string(a.Key)] = a.Value.AsString()
	}
	for k, v := range wantAttr {
		if gotAttr[string(k)] != v {
			t.Fatalf("expected attribute %q=%q, got %q", k, v, gotAttr[string(k)])
		}
	}
}

//nolint:paralleltest // manipulates global otel state
func TestTracingMiddleware_UsesPatternForName(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	orig := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(orig) })

	handler := TracingMiddleware("test-service")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/123", nil)
	const pattern = "GET /rooms/{id}"
	req.Pattern = pattern
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != pattern {
		t.Fatalf("expected span name %q, got %q", pattern, spans[0].Name)
	}
}

//nolint:paralleltest // manipulates global otel state
func TestTracingMiddleware_5xxMarksError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	orig := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(orig) })

	handler := TracingMiddleware("test-service")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("expected error status for 500, got %v", spans[0].Status.Code)
	}
}

//nolint:paralleltest // manipulates global otel state
func TestMetricsMiddleware_RecordsMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	origMP := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(origMP) })

	origMetrics := observability.GetMetrics()
	t.Cleanup(func() { observability.SetGlobalMetrics(origMetrics) })

	m, err := observability.InitMetrics(t.Context(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	observability.SetGlobalMetrics(m)

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test", strings.NewReader("body"))
	req.ContentLength = 4
	handler.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	assertMetricRecorded(t, rm)
}

//nolint:paralleltest // manipulates global otel state
func TestMetricsMiddleware_NilMetrics_PassesThrough(t *testing.T) {
	orig := observability.GetMetrics()
	observability.SetGlobalMetrics(nil)
	t.Cleanup(func() { observability.SetGlobalMetrics(orig) })

	called := false
	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

//nolint:paralleltest // manipulates global otel state
func TestMetricsMiddleware_UsesPatternForRoute(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	origMP := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(origMP) })

	origMetrics := observability.GetMetrics()
	t.Cleanup(func() { observability.SetGlobalMetrics(origMetrics) })

	m, err := observability.InitMetrics(t.Context(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	observability.SetGlobalMetrics(m)

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rooms/123", nil)
	const pattern = "GET /rooms/{id}"
	req.Pattern = pattern
	handler.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name == "http.server.request.count" {
				if d, ok := mv.Data.(metricdata.Sum[int64]); ok && len(d.DataPoints) > 0 {
					assertAttr(t, d.DataPoints[0].Attributes, "http.route", pattern)
					return
				}
			}
		}
	}
	t.Fatal("expected http.server.request.count metric with route attribute")
}

//nolint:paralleltest // manipulates global otel state
func TestMetricsMiddleware_OmitsSizeWhenZero(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	origMP := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(origMP) })

	origMetrics := observability.GetMetrics()
	t.Cleanup(func() { observability.SetGlobalMetrics(origMetrics) })

	m, err := observability.InitMetrics(t.Context(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	observability.SetGlobalMetrics(m)

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write nothing
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.ContentLength = 0
	handler.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name == "http.server.request.size" || mv.Name == "http.server.response.size" {
				t.Fatalf("expected %s to be omitted when zero", mv.Name)
			}
		}
	}
}

func assertMetricRecorded(t *testing.T, rm metricdata.ResourceMetrics) {
	t.Helper()
	assertCounter(t, rm, "http.server.request.count", 1, func(attrs attribute.Set) {
		assertAttr(t, attrs, "http.method", "POST")
		assertAttr(t, attrs, "http.status_code", "201")
	})
	assertHistogram[float64](t, rm, "http.server.request.duration")
	assertMetricExists(t, rm, "http.server.request.size")
	assertHistogram[int64](t, rm, "http.server.response.size", func(attrs attribute.Set) {
		assertAttr(t, attrs, "http.route", "/test")
	})
}

func assertCounter(t *testing.T, rm metricdata.ResourceMetrics, name string, want int64, attrAssert ...func(attribute.Set)) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name != name {
				continue
			}
			d, ok := mv.Data.(metricdata.Sum[int64])
			if !ok || len(d.DataPoints) != 1 || d.DataPoints[0].Value != want {
				t.Fatalf("expected %s = %d, got %+v", name, want, d.DataPoints)
			}
			for _, fn := range attrAssert {
				fn(d.DataPoints[0].Attributes)
			}
			return
		}
	}
	t.Fatalf("expected metric %s", name)
}

func assertHistogram[N int64 | float64](t *testing.T, rm metricdata.ResourceMetrics, name string, attrAssert ...func(attribute.Set)) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name != name {
				continue
			}
			d, ok := mv.Data.(metricdata.Histogram[N])
			if !ok || len(d.DataPoints) != 1 {
				t.Fatalf("expected %s with 1 datapoint, got %+v", name, d.DataPoints)
			}
			for _, fn := range attrAssert {
				fn(d.DataPoints[0].Attributes)
			}
			return
		}
	}
	t.Fatalf("expected metric %s", name)
}

func assertMetricExists(t *testing.T, rm metricdata.ResourceMetrics, name string) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, mv := range sm.Metrics {
			if mv.Name == name {
				return
			}
		}
	}
	t.Fatalf("expected metric %s", name)
}

func assertAttr(t *testing.T, attrs attribute.Set, key, want string) {
	t.Helper()
	val, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Fatalf("missing attribute %q", key)
	}
	if val.AsString() != want {
		t.Fatalf("attribute %q = %q, want %q", key, val.AsString(), want)
	}
}
