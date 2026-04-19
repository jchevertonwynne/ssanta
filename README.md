# ssanta

`ssanta` is a small Go web app for lightweight, room-based chat.

It supports:
- Users (simple username-based accounts)
- Rooms (creator-owned)
- Room membership
- Room invites
- Live chat via WebSockets
- Per-room PGP public keys with an ownership challenge (decrypt-to-verify)

## Quick start (Docker)

The repository includes a `docker-compose.yml` that runs Postgres + the server:

```bash
docker compose up --build
```

Then open:
- http://localhost:8080/

## Run locally (without Docker)

You’ll need a Postgres database and these required environment variables:

- `DATABASE_URL` (example: `postgres://ssanta:ssanta@localhost:5432/ssanta?sslmode=disable`)
- `SESSION_SECRET`

Optional:
- `HTTP_ADDR` (default `:8080`)
- `MIGRATIONS_DIR` (default `migrations`)
- `DATABASE_SCHEMA` (default empty; when set, server will `CREATE SCHEMA IF NOT EXISTS` and set `search_path` so all tables + `schema_migrations` live in that schema)
- `MIGRATE_DATABASE_URL` (default empty; when set, migrations/schema bootstrap use this URL while the app uses `DATABASE_URL` for normal operation)
- `INVITE_MAX_AGE` (default `24h`)
- `JANITOR_INTERVAL` (default `1m`)

Config is loaded via `env` struct tags in `internal/config` (format `env:"NAME"` for required, or `env:"NAME,default"` for optional).

Run:

```bash
go run ./cmd/server
```

## DigitalOcean App Platform / Managed Postgres note

Some managed Postgres setups restrict writes to the `public` schema. If you see an error like:

`pq: permission denied for schema public ... CREATE TABLE ... schema_migrations`

Set `DATABASE_SCHEMA` to a schema name your DB user can own/write (for example `ssanta`). The server will create the schema (if needed) and run migrations with `search_path` set to it.

If your app’s DB user cannot create schemas (error like `permission denied for database ...`), either:

- Pre-create the schema once using an admin role, or
- Set `MIGRATE_DATABASE_URL` to a more privileged connection string (admin role) so the app can create the schema + run migrations at startup.

## Architecture

High-level request flow:

```
HTTP (HTMX) / WebSocket
        │
        ▼
internal/server   (routing, handlers, templates, websocket hub)
        │
        ▼
internal/service  (business logic, view composition)
        │
        ▼
internal/store    (Postgres queries via pgx)
        │
        ▼
Postgres (migrations/)
```

### `cmd/server`

The entrypoint is `cmd/server/main.go`. It:
- Loads config from environment (`internal/config`)
- Connects to Postgres (`internal/db`)
- Runs migrations (`internal/db.Migrate`)
- Constructs the service (`internal/service`) and session manager (`internal/session`)
- Starts the HTTP server and WebSocket hub (`internal/server`)
- Starts a janitor goroutine to clean up old data periodically

### `internal/server` (HTTP + templates + WebSockets)

- Uses `http.ServeMux` route patterns.
- Renders HTML templates from `internal/server/templates/`.
- Uses HTMX for “partial page” updates (handlers frequently return fragments like the room sidebar/dynamic area).
- Uses a WebSocket hub to broadcast chat messages and “refresh” events to clients in a room.

Server-facing interfaces live in `internal/server/interfaces.go` and are mocked via `go.uber.org/mock`.

### `internal/service` (business logic)

The service layer composes view models used by templates:
- `ContentView` (home page fragments)
- `RoomDetailView` (room page fragments)

It also owns higher-level workflows that span multiple store calls, such as issuing and verifying PGP challenges.

### `internal/store` (persistence)

The store layer wraps a `pgxpool.Pool` and provides concrete operations for:
- Users
- Rooms + membership
- Invites

All schema changes live in `migrations/` and are applied on startup.

### `internal/session`

Authentication is cookie-based.

The session manager signs a cookie using an HMAC secret (`SESSION_SECRET`) and resolves the user ID from it.

## Per-room PGP key verification

Each room member can upload a PGP public key for that room.

Storage:
- PGP fields live on the `room_users` join table (per-room, per-user)

Ownership challenge:
1. User uploads an armored public key.
2. Server normalizes/validates it (must be a *public* key and usable for encryption).
3. Server generates a random challenge string.
4. Server encrypts the challenge to the uploaded public key using `github.com/ProtonMail/gopenpgp/v3`.
5. Server stores only a SHA-256 hash of the plaintext challenge plus an expiry timestamp.
6. User decrypts the ciphertext locally and submits the plaintext.
7. Server compares hashes (constant-time) and, if valid and unexpired, marks the key as verified.

Challenge expiry:
- Room PGP challenges expire after **10 minutes**.

UI:
- The encrypted challenge includes a “Copy” button for convenience.
- Members can view each other’s public keys + fingerprints within a room.

## Background cleanup (“janitor”)

A background goroutine periodically:
- Deletes invites older than `INVITE_MAX_AGE` (default `24h`)
- Clears expired room PGP challenge fields once `pgp_challenge_expires_at` has passed

This keeps the database tidy even if users never click “verify”.

## Testing

See `TESTING.md` for the authoritative commands.

Summary:
- Unit tests: `go test ./...`
- Integration tests (Testcontainers + Postgres): `SSANTA_INTEGRATION=1 go test ./internal/store -count=1`

## Development notes

### Mocks

After changing server interfaces:

```bash
go generate ./internal/server
```

### Vendoring

This repo vendors dependencies (see `vendor/`) and Docker builds use `-mod=vendor`.

After adding/changing dependencies:

```bash
go mod tidy
go mod vendor
```
