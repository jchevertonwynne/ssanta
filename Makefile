.PHONY: all build test test-race test-integration test-all lint vet fmt generate run migrate clean help

all: vet lint test-race

## build: Build the server binary.
build:
	go build -o server ./cmd/server

## test: Run unit tests.
test:
	go test ./...

## test-race: Run unit tests with the race detector.
test-race:
	go test -race ./...

## test-integration: Run integration tests (requires Docker).
test-integration:
	SSANTA_INTEGRATION=1 go test -race ./internal/store ./internal/service -count=1 -v

## test-all: Run unit and integration tests.
test-all: test-race test-integration

## lint: Run golangci-lint.
lint:
	golangci-lint run ./...

## vet: Run go vet.
vet:
	go vet ./...

## fmt: Format all Go files.
fmt:
	gofmt -w .

## generate: Run go generate (mocks, etc.).
generate:
	go generate ./...

## start: Start the local development stack with docker compose.
start:
	docker compose up server --build -d

stop:
	docker compose down

## migrate: Run database migrations in docker compose.
migrate:
	docker compose up migrate --build -d

## clean: Remove build artifacts and docker volumes.
clean:
	rm -f server migrate loadgen
	docker compose down -v

## help: Show this help message.
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST)
