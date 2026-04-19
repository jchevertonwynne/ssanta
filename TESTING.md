# Testing

## Unit tests

Runs fast and does not require Docker:

- `go test ./...`

## Integration tests (Postgres via Testcontainers)

Integration tests are skipped unless you opt in:

- `SSANTA_INTEGRATION=1 go test ./internal/store -count=1`

Notes:
- Requires a working Docker daemon.
- Tests start a temporary Postgres container, run migrations from `migrations/`, then truncate tables between tests.

## Mocks (uber-go/mock)

Server-layer interfaces live in `internal/server/interfaces.go` and have a `go:generate` directive.

Regenerate mocks after changing interfaces:

- `go generate ./internal/server`

Generated output:
- `internal/server/mocks/mock_interfaces.go`
