#!/usr/bin/env bash
# Smoke test — the gate between "deployed" and "receives traffic" (§10).
#
# Exits non-zero if the target is not serving, which is what arms the CD
# pipeline's auto-rollback. Safe to run against localhost or a Cloud Run URL.
#
# Usage: smoke-test.sh [API_BASE] [ML_BASE]
set -euo pipefail

API="${1:-${API_BASE:-http://localhost:8080}}"
ML="${2:-${ML_BASE:-http://localhost:8081}}"
RETRIES="${RETRIES:-10}"
DELAY="${DELAY:-2}"

pass=0
fail=0

check() {
  local name="$1" url="$2" expect="${3:-200}"
  local code
  for i in $(seq 1 "$RETRIES"); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$url" || echo 000)"
    if [[ "$code" == "$expect" ]]; then
      echo "  PASS  $name ($url -> $code)"
      pass=$((pass + 1))
      return 0
    fi
    [[ $i -lt $RETRIES ]] && sleep "$DELAY"
  done
  echo "  FAIL  $name ($url -> $code, expected $expect)"
  fail=$((fail + 1))
  return 0
}

echo "smoke test"
echo "  api = $API"
echo "  ml  = $ML"
echo

check "api liveness"   "$API/internal/health"
check "api readiness"  "$API/internal/ready"
check "ml  liveness"   "$ML/health"

echo
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]] || exit 1
