package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/jchevertonwynne/ssanta/internal/model"
	"github.com/jchevertonwynne/ssanta/internal/observability"
)

var errHijackNotSupported = errors.New("hijack not supported")

type contextKey int

const (
	ctxKeyUserID contextKey = iota
	ctxKeyLogger
	ctxKeyRequestID
	ctxKeyCSRFID
	ctxKeyCSRFToken
	ctxKeyScriptNonce
	ctxKeyWSSide
)

// Chain wraps h with the given middlewares, with the *first* middleware as the
// outermost layer. So Chain(h, A, B) is equivalent to A(B(h)).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func loggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithRequestLogger derives a request-scoped logger holding request_id, method
// and path. Handlers retrieve it via loggerFromContext.
func WithRequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get("X-Request-Id")
			if rid == "" {
				rid = newRequestID()
			}
			logger := base.With(
				"request_id", rid,
				"method", r.Method,
				"path", r.URL.Path,
			)
			ctx := context.WithValue(r.Context(), ctxKeyRequestID, rid)
			ctx = context.WithValue(ctx, ctxKeyLogger, logger)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RecoverPanic catches panics from downstream handlers and returns 500.
func RecoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		defer func() {
			if rec := recover(); rec != nil {
				loggerFromContext(ctx).Error("handler panic", "panic", rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

func newScriptNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

func scriptNonceFromContext(ctx context.Context) string {
	if nonce, ok := ctx.Value(ctxKeyScriptNonce).(string); ok {
		return nonce
	}
	return ""
}

// WithScriptNonce generates a per-request CSP script nonce and injects it into
// the request context so SecurityHeaders can reference it.
func WithScriptNonce(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newScriptNonce()
		ctx := context.WithValue(r.Context(), ctxKeyScriptNonce, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// pathInt64 parses a numeric path parameter and writes a 400 on failure.
func pathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// pathUserID parses a user ID from a path parameter.
func pathUserID(w http.ResponseWriter, r *http.Request, name string) (model.UserID, bool) {
	v, ok := pathInt64(w, r, name)
	return model.UserID(v), ok
}

// pathRoomID parses a room ID from a path parameter.
func pathRoomID(w http.ResponseWriter, r *http.Request, _ string) (model.RoomID, bool) {
	v, ok := pathInt64(w, r, "id")
	return model.RoomID(v), ok
}

// pathInviteID parses an invite ID from a path parameter.
func pathInviteID(w http.ResponseWriter, r *http.Request, name string) (model.InviteID, bool) {
	v, ok := pathInt64(w, r, name)
	return model.InviteID(v), ok
}

// responseWriter captures status code and response size.
type responseWriter struct {
	http.ResponseWriter

	statusCode int
	written    int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

// Hijack implements http.Hijacker for WebSocket upgrades.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errHijackNotSupported
	}
	return h.Hijack()
}

// TracingMiddleware extracts trace context from headers and creates a root span for each request.
func TracingMiddleware(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(serviceName)
	propagator := otel.GetTextMapPropagator()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from headers
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Create a new span for this request
			spanName := r.Method + " " + r.URL.Path
			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.target", r.URL.Path),
					attribute.String("http.scheme", r.URL.Scheme),
					attribute.String("http.host", r.Host),
					attribute.String("http.user_agent", r.UserAgent()),
				),
			)
			defer span.End()

			// Capture response status
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(rw, r.WithContext(ctx))

			// Record status code
			span.SetAttributes(attribute.Int("http.status_code", rw.statusCode))

			// Mark span as error if 5xx
			if rw.statusCode >= 500 {
				span.SetStatus(codes.Error, http.StatusText(rw.statusCode))
			}
		})
	}
}

// SecurityHeaders adds security-related HTTP headers to every response.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			nonce := scriptNonceFromContext(r.Context())
			scriptSrc := "script-src 'self' unpkg.com cdn.jsdelivr.net"
			if nonce != "" {
				scriptSrc += " 'nonce-" + nonce + "'"
			}
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; "+
					scriptSrc+"; "+
					"script-src-attr 'none'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"connect-src 'self' ws: wss:;")
			if secure {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxRequestBody caps the request body at 1 MB.
func MaxRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
		next.ServeHTTP(w, r)
	})
}

// MetricsMiddleware records HTTP metrics for each request.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := observability.GetMetrics()
		if metrics == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		statusCode := strconv.Itoa(rw.statusCode)

		// Common attributes
		attrs := attribute.NewSet(
			attribute.String("http.method", r.Method),
			attribute.String("http.route", r.URL.Path),
			attribute.String("http.status_code", statusCode),
		)

		// Record metrics
		metrics.HTTPRequestCount.Add(r.Context(), 1, metric.WithAttributeSet(attrs))
		metrics.HTTPRequestDuration.Record(r.Context(), duration, metric.WithAttributeSet(attrs))

		if r.ContentLength > 0 {
			metrics.HTTPRequestSize.Record(r.Context(), r.ContentLength, metric.WithAttributeSet(attrs))
		}

		if rw.written > 0 {
			metrics.HTTPResponseSize.Record(r.Context(), rw.written, metric.WithAttributeSet(attrs))
		}
	})
}
