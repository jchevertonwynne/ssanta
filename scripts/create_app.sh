#!/usr/bin/env bash
set -euo pipefail

# Creates the DigitalOcean managed PostgreSQL cluster and App Platform app,
# then runs do_setup_migrate_job.sh to wire the DB, create the least-privilege
# role, and inject all secrets.
#
# Prerequisites: doctl authenticated, repo pushed to GitHub main.
#
# Usage:
#   bash scripts/create_app.sh
#   ALLOW_LOCAL_IP=1 bash scripts/create_app.sh  # if your machine needs DB access for psql grants

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

DB_CLUSTER_NAME="${DB_CLUSTER_NAME:-ssanta-pg}"
APP_SPEC="${APP_SPEC:-$REPO_ROOT/.do/app.yaml}"
# Default to the doctl-managed user path to avoid psql SSL issues behind
# corporate TLS proxies (e.g. Zscaler). The DO-managed user is admin-level
# rather than least-privilege, but is fine for a toy deployment.
# Set FALLBACK_TO_DOCTL_USER=0 to attempt least-privilege grants via psql instead.
FALLBACK_TO_DOCTL_USER="${FALLBACK_TO_DOCTL_USER:-1}"
ALLOW_LOCAL_IP="${ALLOW_LOCAL_IP:-0}"

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "Missing required command: $1" >&2; exit 1; }
}

require doctl

log() { printf '[create-app] %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# 1. Create managed PostgreSQL cluster (skip if already exists).
# ---------------------------------------------------------------------------
log "checking for existing db cluster: $DB_CLUSTER_NAME"
DB_CLUSTER_ID="$(doctl databases list --format ID,Name --no-header 2>/dev/null \
  | awk -v name="$DB_CLUSTER_NAME" '$2==name{print $1; exit}')"

if [[ -n "$DB_CLUSTER_ID" ]]; then
  log "db cluster already exists: $DB_CLUSTER_ID"
else
  log "creating db cluster (this takes a few minutes)..."
  doctl databases create "$DB_CLUSTER_NAME" \
    --engine pg \
    --version 17 \
    --size db-s-1vcpu-1gb \
    --num-nodes 1 \
    --region lon1 \
    --wait
  DB_CLUSTER_ID="$(doctl databases list --format ID,Name --no-header 2>/dev/null \
    | awk -v name="$DB_CLUSTER_NAME" '$2==name{print $1; exit}')"
  log "created db cluster: $DB_CLUSTER_ID"
fi

# ---------------------------------------------------------------------------
# 2. Create the App Platform app (skip if already exists).
# ---------------------------------------------------------------------------
log "checking for existing app: ssanta"
APP_ID="$(doctl apps list --format ID,Spec.Name --no-header 2>/dev/null \
  | awk '$2=="ssanta"{print $1; exit}')"

if [[ -n "$APP_ID" ]]; then
  log "app already exists: $APP_ID"
else
  log "creating app from $APP_SPEC..."
  APP_ID="$(doctl apps create --spec "$APP_SPEC" --format ID --no-header --wait)"
  log "created app: $APP_ID"
fi

# ---------------------------------------------------------------------------
# 3. Wire DB to app, create least-privilege role, inject secrets.
# ---------------------------------------------------------------------------
log "running do_setup_migrate_job.sh..."
APP_ID="$APP_ID" \
DB_CLUSTER_ID="$DB_CLUSTER_ID" \
ALLOW_LOCAL_IP="$ALLOW_LOCAL_IP" \
  bash "$SCRIPT_DIR/do_setup_migrate_job.sh"

log "done — app $APP_ID is deploying"
