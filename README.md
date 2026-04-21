# ssanta

`ssanta` is a small Go web app for lightweight, room-based chat.

It supports:
- Users (simple username-based accounts)
- Rooms (creator-owned)
- Room membership
- Room invites
- Live chat via WebSockets
- Direct Messages (DMs) — private 1:1 conversations between users
- Whisper messaging — private in-room messages visible only to sender and recipient
- Per-room PGP public keys with an ownership challenge (decrypt-to-verify)

## Quick start (Docker)

The repository includes a `docker-compose.yml` that runs Postgres + the server:

```bash
docker compose up --build
```

Then open:
- http://localhost:8080/

## Run locally (without Docker)

You'll need a Postgres database and these required environment variables:

- `DATABASE_URL` (example: `postgres://ssanta:ssanta@localhost:5432/ssanta?sslmode=disable`)
- `SESSION_SECRET`

Optional:
- `HTTP_ADDR` (default `:8080`)
- `MIGRATIONS_DIR` (default `migrations`)
- `DATABASE_SCHEMA` (default empty; when set, server will `CREATE SCHEMA IF NOT EXISTS` and set `search_path` so all tables + `schema_migrations` live in that schema)
- `MIGRATE_DATABASE_URL` (default empty; when set, migrations/schema bootstrap use this URL while the app uses `DATABASE_URL` for normal operation)
- `INVITE_MAX_AGE` (default `24h`)
- `JANITOR_INTERVAL` (default `1m`)
- `ROOM_PGP_CHALLENGE_TTL` (default `10m`)
- `SESSION_TTL` (default `168h`)
- `SECURE_COOKIES` (default `true`; set to `false` for local HTTP dev)
- `ARGON2_*` — password hashing parameters (time, memory, threads, key length, salt length); sensible defaults are built in
- `OTLP_ENDPOINT`, `OTLP_INSECURE` — OpenTelemetry trace/metric export
- `OTEL_SERVICE_NAME`, `DEPLOYMENT_ENVIRONMENT` — service identity for OTEL

Config is loaded via `env` struct tags in `internal/config` (format `env:"NAME"` for required, or `env:"NAME,default"` for optional).

Run:

```bash
go run ./cmd/migrate
go run ./cmd/server
```

`cmd/migrate` runs database migrations and exits. `cmd/server` is the publicly accessible web process and **does not** apply migrations on startup.

## DigitalOcean App Platform / Managed Postgres note

Some managed Postgres setups restrict writes to the `public` schema. If you see an error like:

`pq: permission denied for schema public ... CREATE TABLE ... schema_migrations`

Set `DATABASE_SCHEMA` to a schema name your DB user can own/write (for example `ssanta`). The server will create the schema (if needed) and run migrations with `search_path` set to it.

If your app's DB user cannot create schemas (error like `permission denied for database ...`), either:

- Pre-create the schema once using an admin role, or
- Set `MIGRATE_DATABASE_URL` to a more privileged connection string (admin role) so the app can create the schema + run migrations at startup.

### DigitalOcean Dev Database

Dev Databases don't support creating additional databases/schemas. For this setup:

- Leave `DATABASE_SCHEMA` unset (or set it to `public`).
- Don't set `MIGRATE_DATABASE_URL`.

If you still see `permission denied for schema public`, the Dev Database role likely doesn't have sufficient privileges to run migrations; in that case you'll need to switch to a managed Postgres cluster where you can grant privileges or use an admin role for migrations.

### Recommended: run migrations as an App Platform Job

For better security, don't run migrations in the publicly accessible web process. Instead:

- Keep the web service (`cmd/server`) running with a least-privileged `DATABASE_URL` (no DDL permissions).
- Add an App Platform **Job** with `kind: PRE_DEPLOY` that runs `/app/migrate` using `MIGRATE_DATABASE_URL` (DDL-capable).

This repo includes a `/app/migrate` binary in the Docker image for that purpose.

If you also set `RUNTIME_DB_USER` on the job (for example `ssanta_app`), the migrate job will grant that role the required table/sequence privileges after applying migrations.

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
- Uses HTMX for "partial page" updates (handlers frequently return fragments like the room sidebar/dynamic area).
- Uses a WebSocket hub to broadcast chat messages and "refresh" events to clients in a room.

Server-facing interfaces live in `internal/server/interfaces.go` and are mocked via `go.uber.org/mock`.

### `internal/service` (business logic)

The service layer composes view models used by templates:
- `ContentView` (home page fragments)
- `RoomDetailView` (room page fragments)

It also owns higher-level workflows that span multiple store calls, such as issuing and verifying PGP challenges and creating/retrieving DM rooms.

### `internal/store` (persistence)

The store layer wraps a `pgxpool.Pool` and provides concrete operations for:
- Users
- Rooms + membership (including DM rooms)
- Invites

All schema changes live in `migrations/` and are applied on startup.

### `internal/session`

Authentication is cookie-based.

The session manager signs a cookie using an HMAC secret (`SESSION_SECRET`) and resolves the user ID from it.

## Direct Messages

DMs are private 1:1 rooms between two users.

- Started from the home page via the "Direct Messages" section — no invite required.
- Both participants are auto-joined when the DM is created.
- DM room names use the reserved format `dm:user1:user2` (usernames sorted alphabetically).
- A DM room is automatically deleted once both participants have left.

The following operations are blocked on DMs (they return `ErrOperationNotAllowedOnDM`):
- Deleting the room
- Inviting additional users
- Toggling `members_can_invite`
- Removing members

PGP can still be toggled on a DM room, but either participant may do it (not just the creator).

## Whisper messaging

In non-DM rooms a user can target a specific member with a private message.

- The whisper target is selected from a dropdown in the chat UI (defaults to "Everyone").
- The server delivers the message only to the sender and the target — nobody else in the room sees it.
- In PGP-required rooms the server encrypts the message to both users' keys before delivery.
- Whispered messages are displayed in purple italic with a "(whisper)" label.

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
- Room PGP challenges expire after `ROOM_PGP_CHALLENGE_TTL` (default **10 minutes**).

UI:
- The encrypted challenge includes a "Copy" button for convenience.
- Members can view each other's public keys + fingerprints within a room.

## Background cleanup ("janitor")

A background goroutine periodically:
- Deletes invites older than `INVITE_MAX_AGE` (default `24h`)
- Clears expired room PGP challenge fields once `pgp_challenge_expires_at` has passed
- Deletes DM rooms where both participants have left

This keeps the database tidy even if users never click "verify" or explicitly leave a DM.

## Testing

See `TESTING.md` for the authoritative commands.

Summary:
- Unit tests: `go test ./...`
- Integration tests (Testcontainers + Postgres): `SSANTA_INTEGRATION=1 go test ./internal/store ./internal/service -count=1`

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

## Development Setup (Local)

These steps get a local development environment running quickly.

1. Install Postgres (for example, via Homebrew on macOS):

```bash
brew install postgresql
brew services start postgresql
```

2. Create a database and user (example):

```bash
psql -c "CREATE USER ssanta WITH PASSWORD 'ssanta';"
psql -c "CREATE DATABASE ssanta OWNER ssanta;"
```

3. Export environment variables:

```bash
export DATABASE_URL="postgres://ssanta:ssanta@localhost:5432/ssanta?sslmode=disable"
export SESSION_SECRET="$(openssl rand -hex 32)"
```

4. Run migrations:

```bash
go run ./cmd/migrate
```

5. Run the server:

```bash
go run ./cmd/server
```

Alternatively use Docker Compose:

```bash
docker compose up --build
```

## Running Tests

Unit tests:

```bash
go test ./... -v
```

Integration tests (require Postgres / Testcontainers):

```bash
SSANTA_INTEGRATION=1 go test ./internal/store ./internal/service -count=1 -v
```

Local CI checks (match what GitHub Actions runs):

```bash
# formatting
gofmt -l .

# vet
go vet ./...

# lint (install first)
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.58.0
golangci-lint run ./...
```

## Continuous Integration (GitHub Actions)

A lightweight CI workflow is provided at `.github/workflows/ci.yml` and runs on pushes and pull requests to `main`. It checks formatting, runs `go vet`, runs `golangci-lint`, and executes `go test ./...`.
