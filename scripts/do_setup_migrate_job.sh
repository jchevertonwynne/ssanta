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

if [[ -z "$APP_ID" ]]; then
  # Try to auto-detect the app ID by name "ssanta".
  APP_ID="$(doctl apps list --format ID,Spec.Name --no-header 2>/dev/null | awk '$2=="ssanta"{print $1; exit}')"
fi
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

# Ensure the app is allowed to reach the DB.
# If it already exists, doctl will return an error; we ignore it.
doctl databases firewalls append "$DB_CLUSTER_ID" --rule "app:${APP_ID}" >/dev/null 2>&1 || true

# Admin connection (contains password). Keep it in memory only.
ADMIN_URI="$(doctl databases connection "$DB_CLUSTER_ID" --format URI --no-header)"
DB_HOST="$(doctl databases connection "$DB_CLUSTER_ID" --format Host --no-header)"
DB_PORT="$(doctl databases connection "$DB_CLUSTER_ID" --format Port --no-header)"
DB_NAME="$(doctl databases connection "$DB_CLUSTER_ID" --format Database --no-header)"

# Create/rotate the runtime user's password.
RUNTIME_DB_PASS="$(openssl rand -hex 24)"

# Create/rotate the runtime role and grant least-privilege rights.
# NOTE: DigitalOcean's doctl database users are *admin* on the cluster.
# We therefore create a Postgres role ourselves and lock it down.
psql "$ADMIN_URI" -v ON_ERROR_STOP=1 \
  -v app_user="$RUNTIME_DB_USER" \
  -v app_pass="$RUNTIME_DB_PASS" \
  -v db_name="$DB_NAME" \
  <<'SQL' >/dev/null
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'app_user') THEN
    EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', :'app_user', :'app_pass');
  ELSE
    EXECUTE format('ALTER ROLE %I WITH PASSWORD %L', :'app_user', :'app_pass');
  END IF;
END$$;

SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db_name', :'app_user') \gexec
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'app_user') \gexec
SELECT format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %I', :'app_user') \gexec
SELECT format('GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO %I', :'app_user') \gexec

-- Ensure future tables/sequences created by doadmin are usable by the app role.
SELECT format('ALTER DEFAULT PRIVILEGES FOR ROLE doadmin IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', :'app_user') \gexec
SELECT format('ALTER DEFAULT PRIVILEGES FOR ROLE doadmin IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO %I', :'app_user') \gexec

-- Explicitly deny DDL.
SELECT format('REVOKE CREATE ON SCHEMA public FROM %I', :'app_user') \gexec
SQL

# Runtime URL used by the public service (least privilege).
RUNTIME_URI="postgresql://${RUNTIME_DB_USER}:${RUNTIME_DB_PASS}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=require"
ingress:

# If SESSION_SECRET is missing on the service, generate one.
SESSION_SECRET_FALLBACK="$(openssl rand -hex 32)"

SPEC_FILE="$(mktemp -t ssanta-appspec.XXXXXX.json)"
cleanup() {
  rm -f "$SPEC_FILE" 2>/dev/null || true
}
trap cleanup EXIT

# Patch the existing app spec in JSON so we preserve any extra settings.
doctl apps get "$APP_ID" --output json | \
  RUNTIME_URI="$RUNTIME_URI" \
  ADMIN_URI="$ADMIN_URI" \
  SERVICE_NAME="$SERVICE_NAME" \
  JOB_NAME="$JOB_NAME" \
  SESSION_SECRET_FALLBACK="$SESSION_SECRET_FALLBACK" \
  python3 - <<'PY' >"$SPEC_FILE"
import json
import os
import sys

runtime_uri = os.environ["RUNTIME_URI"]
admin_uri = os.environ["ADMIN_URI"]
service_name = os.environ["SERVICE_NAME"]
job_name = os.environ["JOB_NAME"]
session_secret_fallback = os.environ["SESSION_SECRET_FALLBACK"]

apps = json.load(sys.stdin)
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

# Web service: preserve existing SESSION_SECRET if present, otherwise generate.
existing = None
for e in envs:
    if e.get("key") == "SESSION_SECRET":
        existing = e.get("value")
        break
if not existing:
    upsert_env(envs, "SESSION_SECRET", session_secret_fallback, env_type="SECRET")
else:
    upsert_env(envs, "SESSION_SECRET", existing, env_type="SECRET")

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
job["envs"] = job_envs

spec["jobs"] = jobs

json.dump(spec, sys.stdout, indent=2)
sys.stdout.write("\n")
PY

# Validate and apply the spec.
doctl apps spec validate "$SPEC_FILE" >/dev/null

doctl apps update "$APP_ID" --spec "$SPEC_FILE" --update-sources --wait --format ID,Updated

# Store runtime password locally (so you can recover it later if needed).
mkdir -p .secrets
chmod 700 .secrets
printf "%s" "$RUNTIME_DB_PASS" > .secrets/ssanta_app_password
chmod 600 .secrets/ssanta_app_password

# Minimal non-secret output.
echo "Done. Created/rotated DB role: ${RUNTIME_DB_USER}" >&2
echo "Runtime DB password saved to: .secrets/ssanta_app_password" >&2
echo "App updated: ${APP_ID} (job: ${JOB_NAME}, service: ${SERVICE_NAME})" >&2
