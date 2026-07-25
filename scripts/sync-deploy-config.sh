#!/usr/bin/env bash
#
# Populate the v2 repo's GitHub Actions config from your existing v1 setup + GCP,
# so you don't copy-paste anything by hand.
#
#   - GCP_PROJECT / GCP_REGION  → READ from the v1 repo (they are variables, not
#                                 secrets, so their values are retrievable) and set
#                                 on the v2 repo.
#   - GCP_SA_KEY                → GitHub secrets are WRITE-ONLY, so it cannot be read
#                                 out of v1. Instead we mint a FRESH key for the same
#                                 deploy service account in GCP and push it to v2.
#                                 (A service account can hold multiple keys; v1's is
#                                 untouched.) Or pass KEY_FILE=path to reuse a JSON
#                                 key you already have on disk.
#
# Requires: gh (authenticated), and gcloud (authenticated) unless KEY_FILE is given.
#
# Usage:
#   DEPLOY_SA=gh-deployer@<project>.iam.gserviceaccount.com ./scripts/sync-deploy-config.sh
#
# Optional overrides:
#   SRC_REPO   default palkani/tamil-proofreading-platform
#   DST_REPO   default palkani/prooftamil
#   SRC_ENV    a GitHub Environment on v1 to also look in (e.g. Production)
#   GCP_PROJECT / GCP_REGION   set these to skip reading from v1
#   KEY_FILE   path to an existing SA JSON key (skips minting a new one)
set -euo pipefail

SRC_REPO="${SRC_REPO:-palkani/tamil-proofreading-platform}"
DST_REPO="${DST_REPO:-palkani/prooftamil}"

command -v gh >/dev/null || { echo "gh CLI not found"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "run 'gh auth login' first"; exit 1; }

# Read a variable from the v1 repo: try repo-level, then an environment if SRC_ENV set.
read_v1_var() {
  local name="$1" val=""
  val=$(gh api "/repos/$SRC_REPO/actions/variables/$name" --jq .value 2>/dev/null || true)
  if [ -z "$val" ] && [ -n "${SRC_ENV:-}" ]; then
    val=$(gh api "/repos/$SRC_REPO/environments/$SRC_ENV/variables/$name" --jq .value 2>/dev/null || true)
  fi
  printf '%s' "$val"
}

# 1. Variables ------------------------------------------------------------------
PROJECT_VAL="${GCP_PROJECT:-$(read_v1_var GCP_PROJECT)}"
REGION_VAL="${GCP_REGION:-$(read_v1_var GCP_REGION)}"
[ -n "$REGION_VAL" ] || REGION_VAL="asia-south1"

if [ -z "$PROJECT_VAL" ]; then
  echo "! Could not read GCP_PROJECT from $SRC_REPO."
  echo "  Pass it explicitly: GCP_PROJECT=my-proj ... (or SRC_ENV=Production if it lives in an environment)"
  exit 1
fi

gh variable set GCP_PROJECT --repo "$DST_REPO" --body "$PROJECT_VAL"
gh variable set GCP_REGION  --repo "$DST_REPO" --body "$REGION_VAL"
echo "  ✓ variable GCP_PROJECT = $PROJECT_VAL"
echo "  ✓ variable GCP_REGION  = $REGION_VAL"

# 2. GCP_SA_KEY secret ----------------------------------------------------------
if [ -n "${KEY_FILE:-}" ]; then
  [ -f "$KEY_FILE" ] || { echo "KEY_FILE '$KEY_FILE' not found"; exit 1; }
  gh secret set GCP_SA_KEY --repo "$DST_REPO" < "$KEY_FILE"
  echo "  ✓ secret GCP_SA_KEY set from $KEY_FILE"
else
  : "${DEPLOY_SA:?set DEPLOY_SA=<deployer sa email> or pass KEY_FILE=path}"
  command -v gcloud >/dev/null || { echo "gcloud not found (needed to mint a key)"; exit 1; }
  TMPKEY=$(mktemp)
  trap 'rm -f "$TMPKEY"' EXIT
  gcloud iam service-accounts keys create "$TMPKEY" --iam-account "$DEPLOY_SA" >/dev/null
  gh secret set GCP_SA_KEY --repo "$DST_REPO" < "$TMPKEY"
  echo "  ✓ secret GCP_SA_KEY set from a fresh key of $DEPLOY_SA (v1's key untouched)"
fi

echo "✓ done — $DST_REPO now has GCP_PROJECT, GCP_REGION, GCP_SA_KEY"
