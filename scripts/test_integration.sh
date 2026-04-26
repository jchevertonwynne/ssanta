#!/usr/bin/env bash
set -euo pipefail

# Run integration tests locally.
# Requires Docker (Testcontainers spins up PostgreSQL containers).
#
# Usage:
#   bash scripts/test_integration.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

echo "==> go test -race ./internal/store ./internal/service -count=1 -v"
SSANTA_INTEGRATION=1 go test -race ./internal/store ./internal/service -count=1 -v

echo "==> integration tests passed"
