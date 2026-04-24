# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
go build ./cmd/server
go build ./cmd/migrate

# Unit tests (no Docker needed)
go test ./...
go test ./internal/server -run TestName -v   # single test

# Integration tests (require Docker daemon)
SSANTA_INTEGRATION=1 go test ./internal/store -count=1
SSANTA_INTEGRATION=1 go test ./internal/service -count=1
SSANTA_INTEGRATION=1 go test ./internal/store ./internal/service -run TestName -count=1 -v

# Lint
go vet ./...
golangci-lint run ./...

# Regenerate mocks (after changing internal/server/interfaces.go)
go generate ./internal/server

# Run locally
go run ./cmd/migrate   # run migrations (required before first start)
go run ./cmd/server    # start web server
```

Required env vars for local development: `DATABASE_URL`, `SESSION_SECRET`. Set `SECURE_COOKIES=false` for local HTTP.

## Architecture

`ssanta` is a room-based chat app. The request flow is:

```
HTTP (HTMX) / WebSocket
        ↓
internal/server    — routing, HTTP handlers, templates, middleware, WebSocket routing
        ↓
internal/service   — business logic, composed view models
        ↓
internal/store     — pgx queries against Postgres
        ↓
Postgres (migrations/)
```

### Key packages

| Package | Role |
|---------|------|
| `cmd/server` | Entry point: loads config, initialises DB/service/sessions, starts HTTP + janitor |
| `cmd/migrate` | Standalone migration runner; supports separate admin vs runtime DB roles |
| `internal/config` | Env-based config via `env` struct tags; validates required fields |
| `internal/server` | Handlers, template rendering, CSRF, middleware chain, WebSocket routing |
| `internal/service` | View-model composition (ContentView, RoomDetailView), DM/PGP workflows |
| `internal/store` | All Postgres access via pgx/pgxpool |
| `internal/session` | HMAC-signed cookie auth with session version for force-logout |
| `internal/ws` | WebSocket hub: broadcasts room messages and HTMX content updates |
| `internal/pgp` | PGP key validation, normalisation, challenge encryption/verification |
| `internal/ratelimit` | Sliding-window per-IP rate limiter used for auth and search endpoints |
| `internal/observability` | OpenTelemetry setup (traces, metrics, OTLP gRPC export) |

### server package internals

`server.go` — `New()` wires the mux + middleware chain, plus template data types and render helpers.  
`interfaces.go` — ~30 small single-method capability interfaces composed into handler-facing and root interfaces (`ServerService`). `service.Service` satisfies `ServerService`. Mocks are generated at `mocks/mock_interfaces.go`.  
`middleware.go` — middleware stack + shared context helpers (`loggerFromContext`, `pathRoomID`, `pathUserID`, `pathInviteID`, `scriptNonceFromContext`).  
`csrf.go` — HMAC-SHA256 CSRF tokens stored in context and validated on state-changing requests.

### Rendering pattern

UI updates use HTMX partial renders. Handlers call `render*` helpers (e.g. `renderRoom`, `renderContentWithRoomFormError`) that fetch the required view data and execute a named template. The full-page shell is `index.html`; all subsequent navigation swaps partials via HTMX.

### Integration tests

Tests under `internal/store` and `internal/service` are integration tests gated by `SSANTA_INTEGRATION=1`. They use Testcontainers to spin up a temporary Postgres container, run all migrations from `migrations/`, then truncate tables between test cases. Each test gets its own Postgres schema for full isolation.

### WebSocket

`internal/ws` runs a single `ChatHub` goroutine that owns all connection state. Handlers in `server/handler_ws.go` upgrade HTTP to WebSocket then register with the hub. Per-connection inbound frames are rate-limited by a token bucket in `internal/ws`.

### Database migrations

Migrations live in `migrations/` and are managed by `golang-migrate`. `cmd/migrate` applies them; `cmd/server` does **not** run migrations on startup. When `DATABASE_SCHEMA` is set, migrations and `search_path` are scoped to that schema.
