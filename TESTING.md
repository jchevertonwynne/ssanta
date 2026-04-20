# Testing

## Unit tests

Runs fast and does not require Docker:

- `go test ./...`

## Integration tests (Postgres via Testcontainers)

Integration tests are skipped unless you opt in. Requires a working Docker daemon. Tests start a temporary Postgres container, run migrations from `migrations/`, then truncate tables between tests.

Store layer (room leave/join behaviour, invite cleanup):

- `SSANTA_INTEGRATION=1 go test ./internal/store -count=1`

Service layer (room detail views, content views, DM creation and auto-join):

- `SSANTA_INTEGRATION=1 go test ./internal/service -count=1`

## Mocks (uber-go/mock)

Server-layer interfaces live in `internal/server/interfaces.go` and have a `go:generate` directive.

Regenerate mocks after changing interfaces:

- `go generate ./internal/server`

Generated output:
- `internal/server/mocks/mock_interfaces.go`
