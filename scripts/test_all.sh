#!/usr/bin/env bash
set -euo pipefail

# Run all tests locally: unit tests + integration tests.
# Requires Docker (for integration Testcontainers).
#
# Usage:
#   bash scripts/test_all.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

bash "$SCRIPT_DIR/test.sh"
echo ""
bash "$SCRIPT_DIR/test_integration.sh"

echo ""
echo "==> all tests passed"
