# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src

# Dependency layer — cached until go.mod/go.sum/vendor change.
COPY go.mod go.sum ./
COPY vendor ./vendor

# Source layer — invalidates on code changes, but vendor layer is reused.
COPY cmd ./cmd
COPY internal ./internal

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
ENV HTTP_ADDR=:8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
