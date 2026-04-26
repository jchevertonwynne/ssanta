#!/usr/bin/env bash
set -euo pipefail

# Run unit tests locally (no integration tests, no Docker required).
#
# Usage:
#   bash scripts/test.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

echo "==> go vet ./..."
go vet ./...

echo "==> golangci-lint run ./..."
golangci-lint run ./...

echo "==> go test -race ./..."
go test -race ./...

echo "==> unit tests passed"
