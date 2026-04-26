#!/usr/bin/env bash
set -euo pipefail

# DigitalOcean App Platform: split migrations from the public web service.
#
# What this script does:
# 1) Ensures the app can reach the DB (DB firewall rule for app).
# 2) Generates a runtime DB password and injects secrets into the App Platform spec.
# 3) Updates the App Platform spec to:
#    - run a PRE_DEPLOY Job that executes /app/migrate using MIGRATE_DATABASE_URL (DDL-capable)
#    - run the web service using DATABASE_URL (least-privileged)
#
# Role creation and privilege grants are handled by the migrate binary during PRE_DEPLOY.
#
# Secrets handling:
# - This script does NOT print any DB passwords.
# - It writes the generated runtime DB password to .secrets/ssanta_app_password (chmod 600).
# - Do NOT run with `bash -x`.

umask 077

APP_ID="${APP_ID:-${1:-}}"
DB_CLUSTER_ID="${DB_CLUSTER_ID:-${2:-}}"
SERVICE_NAME="${SERVICE_NAME:-ssanta}"
JOB_NAME="${JOB_NAME:-migrate}"
RUNTIME_DB_USER="${RUNTIME_DB_USER:-ssanta_app}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

require doctl
require openssl
require python3

log() {
  # Log to stderr so stdout stays clean.
  printf '%s %s\n' "[do-setup]" "$*" >&2
}

if [[ -z "$APP_ID" ]]; then
  # Try to auto-detect the app ID by name "ssanta".
  APP_ID="$(doctl apps list --format ID,Spec.Name --no-header 2>/dev/null | awk '$2=="ssanta"{print $1; exit}')"
fi

log "app_id=$APP_ID"
if [[ -z "$APP_ID" ]]; then
  echo "APP_ID is required (env APP_ID or first arg)." >&2
  exit 1
fi

if [[ -z "$DB_CLUSTER_ID" ]]; then
  # Try to auto-detect a managed DB cluster named "ssanta-pg".
  DB_CLUSTER_ID="$(doctl databases list --format ID,Name --no-header 2>/dev/null | awk '$2=="ssanta-pg"{print $1; exit}')"
fi
if [[ -z "$DB_CLUSTER_ID" ]]; then
  echo "DB_CLUSTER_ID is required (env DB_CLUSTER_ID or second arg)." >&2
  echo "Hint: doctl databases list" >&2
  exit 1
fi

log "db_cluster_id=$DB_CLUSTER_ID"

# Ensure the app is allowed to reach the DB.
# If it already exists, doctl will return an error; we ignore it.
log "ensuring db firewall allows app" 
doctl databases firewalls append "$DB_CLUSTER_ID" --rule "app:${APP_ID}" >/dev/null 2>&1 || true

log "fetching database connection details"
# Admin connection (contains password). Keep it in memory only.
ADMIN_URI="$(doctl databases connection "$DB_CLUSTER_ID" --format URI --no-header)"
DB_HOST="$(doctl databases connection "$DB_CLUSTER_ID" --format Host --no-header)"
DB_PORT="$(doctl databases connection "$DB_CLUSTER_ID" --format Port --no-header)"
DB_NAME="$(doctl databases connection "$DB_CLUSTER_ID" --format Database --no-header)"

log "db_host=$DB_HOST db_port=$DB_PORT db_name=$DB_NAME"

log "current db firewall rules:"
doctl databases firewalls list "$DB_CLUSTER_ID" 2>/dev/null | sed 's/^/[do-setup]   /' >&2 || true

# Generate the runtime user's password. Role creation and grants are handled
# by the migrate binary during the PRE_DEPLOY job.
RUNTIME_DB_PASS="$(openssl rand -hex 24)"

# Runtime URL used by the public service (least privilege).
RUNTIME_URI="postgresql://${RUNTIME_DB_USER}:${RUNTIME_DB_PASS}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=require"

# Load or generate SESSION_SECRET. Stored in .secrets/ so it survives redeploys
# (rotating it would invalidate all active sessions).
mkdir -p .secrets
chmod 700 .secrets
SESSION_SECRET_FILE=".secrets/ssanta_session_secret"
if [[ -s "$SESSION_SECRET_FILE" ]]; then
  SESSION_SECRET_VAL="$(cat "$SESSION_SECRET_FILE")"
  log "loaded SESSION_SECRET from $SESSION_SECRET_FILE"
else
  SESSION_SECRET_VAL="$(openssl rand -hex 32)"
  printf "%s" "$SESSION_SECRET_VAL" > "$SESSION_SECRET_FILE"
  chmod 600 "$SESSION_SECRET_FILE"
  log "generated new SESSION_SECRET, saved to $SESSION_SECRET_FILE"
fi

METRICS_SECRET_FILE=".secrets/ssanta_metrics_secret"
if [[ -s "$METRICS_SECRET_FILE" ]]; then
  METRICS_SECRET_VAL="$(cat "$METRICS_SECRET_FILE")"
  log "loaded METRICS_SECRET from $METRICS_SECRET_FILE"
else
  METRICS_SECRET_VAL="$(openssl rand -hex 32)"
  printf "%s" "$METRICS_SECRET_VAL" > "$METRICS_SECRET_FILE"
  chmod 600 "$METRICS_SECRET_FILE"
  log "generated new METRICS_SECRET, saved to $METRICS_SECRET_FILE"
fi

SPEC_FILE="$(mktemp -t ssanta-appspec.XXXXXX.json)"
APP_GET_JSON_FILE="$(mktemp -t ssanta-appget.XXXXXX.json)"
APP_GET_ERR_FILE="$(mktemp -t ssanta-appget.XXXXXX.err)"
cleanup() {
  rm -f "$SPEC_FILE" 2>/dev/null || true
  rm -f "$APP_GET_JSON_FILE" "$APP_GET_ERR_FILE" 2>/dev/null || true
}
trap cleanup EXIT

# Patch the existing app spec in JSON so we preserve any extra settings.
log "patching app spec (service=$SERVICE_NAME job=$JOB_NAME)"

# Fetch the app spec JSON with a couple retries to avoid transient API issues.
attempt=1
while true; do
  : >"$APP_GET_ERR_FILE"
  if doctl apps get "$APP_ID" --output json >"$APP_GET_JSON_FILE" 2>"$APP_GET_ERR_FILE"; then
    break
  fi
  if [[ $attempt -ge 3 ]]; then
    log "doctl apps get failed after $attempt attempts"
    sed 's/^/[do-setup]   /' "$APP_GET_ERR_FILE" >&2 || true
    exit 2
  fi
  log "doctl apps get failed (attempt $attempt); retrying..."
  sed 's/^/[do-setup]   /' "$APP_GET_ERR_FILE" >&2 || true
  attempt=$((attempt+1))
  sleep 2
done

if [[ ! -s "$APP_GET_JSON_FILE" ]]; then
  log "doctl apps get returned empty JSON"
  sed 's/^/[do-setup]   /' "$APP_GET_ERR_FILE" >&2 || true
  exit 2
fi

RUNTIME_URI="$RUNTIME_URI" \
ADMIN_URI="$ADMIN_URI" \
SERVICE_NAME="$SERVICE_NAME" \
JOB_NAME="$JOB_NAME" \
SESSION_SECRET_VAL="$SESSION_SECRET_VAL" \
RUNTIME_DB_PASS="$RUNTIME_DB_PASS" \
METRICS_SECRET_VAL="$METRICS_SECRET_VAL" \
python3 - "$APP_GET_JSON_FILE" <<'PY' >"$SPEC_FILE"
import json
import os
import sys

runtime_uri = os.environ["RUNTIME_URI"]
admin_uri = os.environ["ADMIN_URI"]
service_name = os.environ["SERVICE_NAME"]
job_name = os.environ["JOB_NAME"]
session_secret_val = os.environ["SESSION_SECRET_VAL"]
runtime_db_pass = os.environ["RUNTIME_DB_PASS"]
metrics_secret_val = os.environ["METRICS_SECRET_VAL"]

with open(sys.argv[1], "r", encoding="utf-8") as f:
  apps = json.load(f)
spec = apps[0].get("spec")
if not isinstance(spec, dict):
    raise SystemExit("Unable to read app spec")

services = spec.get("services") or []
service = None
for s in services:
    if s.get("name") == service_name:
        service = s
        break
if service is None:
    raise SystemExit(f"Service not found: {service_name}")

envs = service.get("envs") or []

def upsert_env(env_list, key, value, scope="RUN_TIME", env_type=None):
    for e in env_list:
        if e.get("key") == key:
            e["value"] = value
            e["scope"] = scope
            if env_type is not None:
                e["type"] = env_type
            return
    e = {"key": key, "value": value, "scope": scope}
    if env_type is not None:
        e["type"] = env_type
    env_list.append(e)

def remove_env(env_list, key):
    env_list[:] = [e for e in env_list if e.get("key") != key]

# Web service: least-privileged DB URL.
upsert_env(envs, "DATABASE_URL", runtime_uri, env_type="SECRET")
remove_env(envs, "MIGRATE_DATABASE_URL")

# Web service: always set SESSION_SECRET from the locally-persisted value so we
# submit a plaintext value (DO rejects re-submitting its own EV[…] ciphertext,
# and omitting the value clears the secret).
upsert_env(envs, "SESSION_SECRET", session_secret_val, env_type="SECRET")
upsert_env(envs, "METRICS_SECRET", metrics_secret_val, env_type="SECRET")

# Strip EV[…]-encrypted values from any other SECRET envs we haven't explicitly
# set — DO rejects re-submitting its own ciphertext. Omitting value for
# non-managed secrets is the least-bad option.
explicitly_set = {"DATABASE_URL", "SESSION_SECRET", "METRICS_SECRET"}
for e in envs:
    if e.get("type") == "SECRET" and e.get("key") not in explicitly_set:
        v = e.get("value", "")
        if v.startswith("EV["):
            del e["value"]

service["envs"] = envs

# Job: pre-deploy migrations.
jobs = spec.get("jobs") or []
job = None
for j in jobs:
    if j.get("name") == job_name:
        job = j
        break
if job is None:
    job = {"name": job_name}
    jobs.append(job)

job["kind"] = "PRE_DEPLOY"
job["run_command"] = "/app/migrate"

# Mirror the service's build/source config for Dockerfile builds.
for k in ("dockerfile_path", "source_dir", "github", "git", "gitlab", "image"):
    if k in service:
        job[k] = service[k]

job.setdefault("instance_count", 1)
if "instance_size_slug" in service:
    job["instance_size_slug"] = service["instance_size_slug"]

job_envs = job.get("envs") or []
upsert_env(job_envs, "MIGRATE_DATABASE_URL", admin_uri, env_type="SECRET")
upsert_env(job_envs, "DATABASE_URL", runtime_uri, env_type="SECRET")
upsert_env(job_envs, "RUNTIME_DB_PASS", runtime_db_pass, env_type="SECRET")
upsert_env(job_envs, "MIGRATIONS_DIR", "/app/migrations", env_type="GENERAL")
upsert_env(job_envs, "RUNTIME_DB_USER", "ssanta_app", env_type="GENERAL")
job["envs"] = job_envs

spec["jobs"] = jobs

json.dump(spec, sys.stdout, indent=2)
sys.stdout.write("\n")
PY

# Validate and apply the spec.
log "validating spec"
doctl apps spec validate "$SPEC_FILE" >/dev/null

log "updating app (this triggers a redeploy)"
doctl apps update "$APP_ID" --spec "$SPEC_FILE" --update-sources --wait --format ID,Updated

# Store runtime password locally (so you can recover it later if needed).
log "saving runtime db password to .secrets/ssanta_app_password"
mkdir -p .secrets
chmod 700 .secrets
printf "%s" "$RUNTIME_DB_PASS" > .secrets/ssanta_app_password
chmod 600 .secrets/ssanta_app_password

# Minimal non-secret output.
echo "Done. Created/rotated DB role: ${RUNTIME_DB_USER}" >&2
echo "Runtime DB password saved to: .secrets/ssanta_app_password" >&2
echo "App updated: ${APP_ID} (job: ${JOB_NAME}, service: ${SERVICE_NAME})" >&2
