# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache ca-certificates

# Copy the module files first to allow better caching in the non-vendored path.
COPY go.mod go.sum ./

# Copy the rest of the repo. If vendor/ is present, we'll use it.
COPY . ./

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    if [ -d vendor ]; then \
        CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /out/server ./cmd/server; \
        CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w" -o /out/migrate ./cmd/migrate; \
    else \
        go mod download \
        && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server \
        && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate; \
    fi

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/migrate /app/migrate
COPY migrations /app/migrations

ENV MIGRATIONS_DIR=/app/migrations
ENV HTTP_ADDR=:8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
