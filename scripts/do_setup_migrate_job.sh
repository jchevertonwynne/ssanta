#!/usr/bin/env bash
set -euo pipefail

# DigitalOcean App Platform: split migrations from the public web service.
#
# What this script does:
# 1) Ensures the app can reach the DB (DB firewall rule for app).
# 2) Creates/rotates a least-privileged DB role for the web service.
# 3) Updates the App Platform spec to:
#    - run a PRE_DEPLOY Job that executes /app/migrate using MIGRATE_DATABASE_URL (DDL-capable)
#    - run the web service using DATABASE_URL (least-privileged)
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
ALLOW_LOCAL_IP="${ALLOW_LOCAL_IP:-0}"
LOCAL_IP_RULE="${LOCAL_IP_RULE:-}"
PGCONNECT_TIMEOUT="${PGCONNECT_TIMEOUT:-10}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

require doctl
require openssl
require psql
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

if [[ "$ALLOW_LOCAL_IP" == "1" ]]; then
  # Allow the machine running this script to connect to the DB (useful for psql grants).
  # You can also provide LOCAL_IP_RULE explicitly, e.g. LOCAL_IP_RULE=203.0.113.10
  if [[ -z "$LOCAL_IP_RULE" ]]; then
    if command -v curl >/dev/null 2>&1; then
      LOCAL_IP_RULE="$(curl -fsSL https://api.ipify.org || true)"
    fi
  fi
  if [[ -z "$LOCAL_IP_RULE" ]]; then
    log "ALLOW_LOCAL_IP=1 but couldn't determine public IP (set LOCAL_IP_RULE=<your.ip.addr>)"
    exit 2
  fi
  log "ensuring db firewall allows local ip_addr:$LOCAL_IP_RULE"
  doctl databases firewalls append "$DB_CLUSTER_ID" --rule "ip_addr:${LOCAL_IP_RULE}" >/dev/null 2>&1 || true
  # Give firewall propagation a moment.
  sleep 3
fi

log "fetching database connection details"
# Admin connection (contains password). Keep it in memory only.
ADMIN_URI="$(doctl databases connection "$DB_CLUSTER_ID" --format URI --no-header)"
DB_HOST="$(doctl databases connection "$DB_CLUSTER_ID" --format Host --no-header)"
DB_PORT="$(doctl databases connection "$DB_CLUSTER_ID" --format Port --no-header)"
DB_NAME="$(doctl databases connection "$DB_CLUSTER_ID" --format Database --no-header)"

log "db_host=$DB_HOST db_port=$DB_PORT db_name=$DB_NAME"

if command -v nc >/dev/null 2>&1; then
  log "checking TCP connectivity to database host"
  if nc -z -w 5 "$DB_HOST" "$DB_PORT" >/dev/null 2>&1; then
    log "tcp check ok"
  else
    log "tcp check failed (this often means outbound port is blocked or firewall rule hasn't propagated yet)"
  fi
else
  log "nc not found; skipping TCP check"
fi

log "current db firewall rules:"
doctl databases firewalls list "$DB_CLUSTER_ID" 2>/dev/null | sed 's/^/[do-setup]   /' >&2 || true

# Create/rotate the runtime user's password.
RUNTIME_DB_PASS="$(openssl rand -hex 24)"

log "creating/rotating runtime db role and grants (user=$RUNTIME_DB_USER db=$DB_NAME host=$DB_HOST port=$DB_PORT)"

# Create/rotate the runtime role and grant least-privilege rights.
# NOTE: DigitalOcean's doctl database users are *admin* on the cluster.
# We therefore create a Postgres role ourselves and lock it down.

if ! PGCONNECT_TIMEOUT="$PGCONNECT_TIMEOUT" psql "$ADMIN_URI" -v ON_ERROR_STOP=1 \
  <<SQL >/dev/null
DO \$\$
BEGIN
  CREATE ROLE "${RUNTIME_DB_USER}" LOGIN PASSWORD '${RUNTIME_DB_PASS}';
EXCEPTION WHEN duplicate_object THEN
  ALTER ROLE "${RUNTIME_DB_USER}" WITH PASSWORD '${RUNTIME_DB_PASS}';
END\$\$;

GRANT CONNECT ON DATABASE "${DB_NAME}" TO "${RUNTIME_DB_USER}";
GRANT USAGE ON SCHEMA public TO "${RUNTIME_DB_USER}";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "${RUNTIME_DB_USER}";
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO "${RUNTIME_DB_USER}";
ALTER DEFAULT PRIVILEGES FOR ROLE doadmin IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "${RUNTIME_DB_USER}";
ALTER DEFAULT PRIVILEGES FOR ROLE doadmin IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO "${RUNTIME_DB_USER}";
REVOKE CREATE ON SCHEMA public FROM "${RUNTIME_DB_USER}";
SQL
then
  log "psql failed to connect (timeout or network block)."
  log "Common causes:"
  log "- Your network blocks outbound port ${DB_PORT}"
  log "- DB firewall/trusted sources hasn't propagated or is missing your IP"
  log "Fixes:"
  log "- Wait ~30s and rerun"
  log "- Ensure firewall has ip_addr:<your_public_ip> (script can do this with ALLOW_LOCAL_IP=1)"
  log "- Try from another network (phone hotspot)"
  exit 2
fi

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
python3 - "$APP_GET_JSON_FILE" <<'PY' >"$SPEC_FILE"
import json
import os
import sys

runtime_uri = os.environ["RUNTIME_URI"]
admin_uri = os.environ["ADMIN_URI"]
service_name = os.environ["SERVICE_NAME"]
job_name = os.environ["JOB_NAME"]
session_secret_val = os.environ["SESSION_SECRET_VAL"]

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

# Strip EV[…]-encrypted values from any other SECRET envs we haven't explicitly
# set — DO rejects re-submitting its own ciphertext. Omitting value for
# non-managed secrets is the least-bad option.
explicitly_set = {"DATABASE_URL", "SESSION_SECRET"}
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
