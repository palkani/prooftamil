#!/usr/bin/env bash
#
# Create the 5 app secrets the v2 api needs in GCP Secret Manager, and grant the
# Cloud Run runtime service account read access to each. Matches the names the
# deploy workflow references: gemini, db, redis, jwtpriv, jwtpub.
#
# The JWT RS256 keypair is generated INLINE and never written to disk. gemini/db/
# redis values come from the environment (never hard-coded, never logged).
#
# Idempotent: re-running adds a new secret version rather than failing.
#
# Usage:
#   PROJECT=my-gcp-project \
#   GEMINI_API_KEY='...' \
#   DATABASE_URL='postgres://user:pass@host:5432/db' \
#   REDIS_URL='redis://:pass@host:6379' \
#   ./scripts/create-secrets.sh
#
# Optional: RUNTIME_SA=<sa-email> to override the default Cloud Run runtime SA.
set -euo pipefail

: "${PROJECT:?set PROJECT=<gcp-project-id>}"
: "${GEMINI_API_KEY:?set GEMINI_API_KEY=<your Gemini key>}"
: "${DATABASE_URL:?set DATABASE_URL=<postgres url>}"
: "${REDIS_URL:?set REDIS_URL=<redis url>}"

echo "→ enabling Secret Manager API"
gcloud services enable secretmanager.googleapis.com --project "$PROJECT" >/dev/null

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')
# The identity a Cloud Run service runs AS (and therefore reads secrets as). Defaults
# to the project's Compute Engine default SA unless you deploy with a custom one.
RUNTIME_SA="${RUNTIME_SA:-${PROJECT_NUMBER}-compute@developer.gserviceaccount.com}"

# put_secret NAME VALUE — create or add a version, then let the runtime SA read it.
put_secret() {
  local name="$1" value="$2"
  if gcloud secrets describe "$name" --project "$PROJECT" >/dev/null 2>&1; then
    printf '%s' "$value" | gcloud secrets versions add "$name" \
      --project "$PROJECT" --data-file=- >/dev/null
  else
    printf '%s' "$value" | gcloud secrets create "$name" \
      --project "$PROJECT" --replication-policy=automatic --data-file=- >/dev/null
  fi
  gcloud secrets add-iam-policy-binding "$name" --project "$PROJECT" \
    --member "serviceAccount:${RUNTIME_SA}" \
    --role roles/secretmanager.secretAccessor >/dev/null 2>&1
  echo "  ✓ $name"
}

echo "→ creating secrets in $PROJECT (runtime SA: $RUNTIME_SA)"
put_secret gemini "$GEMINI_API_KEY"
put_secret db     "$DATABASE_URL"
put_secret redis  "$REDIS_URL"

# RS256 keypair for JWT signing — generated here, never persisted to disk.
JWT_PRIV=$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 2>/dev/null)
JWT_PUB=$(printf '%s' "$JWT_PRIV" | openssl pkey -pubout 2>/dev/null)
put_secret jwtpriv "$JWT_PRIV"
put_secret jwtpub  "$JWT_PUB"

echo "✓ done — 5 secrets ready: gemini, db, redis, jwtpriv, jwtpub"
echo "  the deploy workflow binds them by reference (…=<name>:latest)"
