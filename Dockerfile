# syntax=docker/dockerfile:1.7

# --platform=$BUILDPLATFORM pins this stage to the runner's own architecture
# and leaves the cross-compilation to Go, via GOARCH below. The shared image
# workflow sets up buildx deliberately without QEMU, so an arm64 build stage
# would have no interpreter to run it — this line is what makes the Pi image
# build at all, and it is much quicker than emulating a whole toolchain.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src

# Module files first so the download layer caches across source edits. The
# previous version copied the whole repo before downloading, which meant every
# source change re-fetched the entire dependency tree.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 is what allows the distroless static base below. It costs
# nothing here: pgx is pure Go, unlike a SQLite driver such as
# mattn/go-sqlite3, which would drag in a C toolchain and a libc base image.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
        go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
        go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# distroless/static rather than scratch: pgx may verify TLS against a managed
# Postgres, and that needs CA certificates. Everything else the binaries want
# is inside them.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/migrate /app/migrate

# Templates are embedded (//go:embed in internal/server), so they are already
# in the binary. Migrations are not: golang-migrate opens them off disk at
# MIGRATIONS_DIR, so they have to travel as files.
COPY migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
ENV HTTP_ADDR=:8080
EXPOSE 8080

# Two entrypoints in one image on purpose. The deployment runs /app/migrate as
# an initContainer and /app/server as the container, so migrations never run
# in the publicly reachable process.
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
