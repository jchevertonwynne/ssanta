package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Metrics holds all metric instruments for the application
type Metrics struct {
	// HTTP Metrics
	HTTPRequestCount    metric.Int64Counter
	HTTPRequestDuration metric.Float64Histogram
	HTTPRequestSize     metric.Int64Histogram
	HTTPResponseSize    metric.Int64Histogram

	// WebSocket Metrics
	WSActiveConnections metric.Int64UpDownCounter
	WSMessagesSent      metric.Int64Counter
	WSMessagesReceived  metric.Int64Counter

	// Business Metrics - Rooms
	RoomsCreated metric.Int64Counter
	RoomsDeleted metric.Int64Counter

	// Business Metrics - Users
	UsersRegistered metric.Int64Counter
	UsersLoggedIn   metric.Int64Counter

	// Business Metrics - Invites
	InvitesSent     metric.Int64Counter
	InvitesAccepted metric.Int64Counter
	InvitesRejected metric.Int64Counter

	// Business Metrics - PGP
	PGPKeysUploaded metric.Int64Counter
	PGPKeysDeleted metric.Int64Counter
}

// InitMetrics initializes all metric instruments
func InitMetrics(ctx context.Context, serviceName string) (*Metrics, error) {
	meter := otel.Meter(serviceName)

	m := &Metrics{}
	var err error

	// HTTP Metrics
	m.HTTPRequestCount, err = meter.Int64Counter(
		"http.server.request.count",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request count: %w", err)
	}

	m.HTTPRequestDuration, err = meter.Float64Histogram(
		"http.server.request.duration",
		metric.WithDescription("HTTP request duration"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request duration: %w", err)
	}

	m.HTTPRequestSize, err = meter.Int64Histogram(
		"http.server.request.size",
		metric.WithDescription("HTTP request body size"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(100, 1000, 10000, 100000, 1000000),
	)
	if err != nil {
		return nil, fmt.Errorf("create http request size: %w", err)
	}

	m.HTTPResponseSize, err = meter.Int64Histogram(
		"http.server.response.size",
		metric.WithDescription("HTTP response body size"),
		metric.WithUnit("By"),
		metric.WithExplicitBucketBoundaries(100, 1000, 10000, 100000, 1000000),
	)
	if err != nil {
		return nil, fmt.Errorf("create http response size: %w", err)
	}

	// WebSocket Metrics
	m.WSActiveConnections, err = meter.Int64UpDownCounter(
		"websocket.connections.active",
		metric.WithDescription("Number of active WebSocket connections"),
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create ws active connections: %w", err)
	}

	m.WSMessagesSent, err = meter.Int64Counter(
		"websocket.messages.sent",
		metric.WithDescription("Total number of WebSocket messages sent"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create ws messages sent: %w", err)
	}

	m.WSMessagesReceived, err = meter.Int64Counter(
		"websocket.messages.received",
		metric.WithDescription("Total number of WebSocket messages received"),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create ws messages received: %w", err)
	}

	// Business Metrics - Rooms
	m.RoomsCreated, err = meter.Int64Counter(
		"business.rooms.created",
		metric.WithDescription("Total number of rooms created"),
		metric.WithUnit("{room}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create rooms created: %w", err)
	}

	m.RoomsDeleted, err = meter.Int64Counter(
		"business.rooms.deleted",
		metric.WithDescription("Total number of rooms deleted"),
		metric.WithUnit("{room}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create rooms deleted: %w", err)
	}

	// Business Metrics - Users
	m.UsersRegistered, err = meter.Int64Counter(
		"business.users.registered",
		metric.WithDescription("Total number of users registered"),
		metric.WithUnit("{user}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create users registered: %w", err)
	}

	m.UsersLoggedIn, err = meter.Int64Counter(
		"business.users.logged_in",
		metric.WithDescription("Total number of user logins"),
		metric.WithUnit("{login}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create users logged in: %w", err)
	}

	// Business Metrics - Invites
	m.InvitesSent, err = meter.Int64Counter(
		"business.invites.sent",
		metric.WithDescription("Total number of invites sent"),
		metric.WithUnit("{invite}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create invites sent: %w", err)
	}

	m.InvitesAccepted, err = meter.Int64Counter(
		"business.invites.accepted",
		metric.WithDescription("Total number of invites accepted"),
		metric.WithUnit("{invite}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create invites accepted: %w", err)
	}

	m.InvitesRejected, err = meter.Int64Counter(
		"business.invites.rejected",
		metric.WithDescription("Total number of invites rejected"),
		metric.WithUnit("{invite}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create invites rejected: %w", err)
	}

	// Business Metrics - PGP
	m.PGPKeysUploaded, err = meter.Int64Counter(
		"business.pgp_keys.uploaded",
		metric.WithDescription("Total number of PGP keys uploaded"),
		metric.WithUnit("{key}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create pgp keys uploaded: %w", err)
	}

	m.PGPKeysDeleted, err = meter.Int64Counter(
		"business.pgp_keys.deleted",
		metric.WithDescription("Total number of PGP keys deleted"),
		metric.WithUnit("{key}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create pgp keys deleted: %w", err)
	}

	return m, nil
}

var globalMetrics *Metrics

// SetGlobalMetrics sets the global metrics instance
func SetGlobalMetrics(m *Metrics) {
	globalMetrics = m
}

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	return globalMetrics
}
