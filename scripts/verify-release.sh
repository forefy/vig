#!/usr/bin/env bash
# Verify a deployment is serving the expected version. Polls the
# unauthenticated GET /version until it matches, and fails loudly if it never
# does, so a pointer bump that did not roll is caught in minutes.
#
#   scripts/verify-release.sh 0.1.7
#   VERSION=0.1.7 scripts/verify-release.sh
#   HOST=https://vig.forefy.com TIMEOUT=1200 scripts/verify-release.sh v0.1.7
set -euo pipefail

HOST="${HOST:-https://vig.forefy.com}"
WANT="${1:-${VERSION:-}}"
TIMEOUT="${TIMEOUT:-1200}"   # seconds; the ArgoCD root -> child -> OCI pull -> rollout chain can pass 10 min
INTERVAL="${INTERVAL:-15}"

if [ -z "${WANT}" ]; then
  echo "usage: $0 <version>   (or set VERSION=...)" >&2
  exit 2
fi
WANT="${WANT#v}"

echo ">> waiting for ${HOST}/version to report ${WANT} (timeout ${TIMEOUT}s)"
deadline=$(( $(date +%s) + TIMEOUT ))
last=""
while [ "$(date +%s)" -lt "${deadline}" ]; do
  # A timeout or 5xx mid-rollout is a retry, not a failure.
  got="$(curl -fsS --max-time 12 "${HOST}/version" 2>/dev/null \
        | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')" || true
  if [ "${got}" = "${WANT}" ]; then
    echo ">> OK: ${HOST} is serving ${got}"
    exit 0
  fi
  if [ "${got}" != "${last}" ]; then
    echo "   serving '${got:-<no answer>}', want '${WANT}' ..."
    last="${got}"
  fi
  sleep "${INTERVAL}"
done

echo "!! FAIL: ${HOST} never served ${WANT} within ${TIMEOUT}s (last: '${last:-<no answer>}')" >&2
echo "   check: argocd app get vig; kubectl -n vig get pods" >&2
exit 1
