#!/usr/bin/env bash
# Create (or reuse) the local k3d cluster for the Tilt loop. The same chart prod runs is
# deployed here; this is the prod-parity check. Idempotent.
#
#   ./deploy/dev/k3d-up.sh   # then: tilt up
#
# Registry: vig-registry, reachable at localhost:5006 from the host (Tilt's docker push) and
# vig-registry:5000 from inside the cluster (pods pull). Host port 5006 avoids the sibling
# projects' registries on 5000 and 5005. Traefik's LB is mapped to host :7113 (vig.localhost).
set -euo pipefail
CLUSTER=vig-dev

if k3d cluster list 2>/dev/null | awk '{print $1}' | grep -qx "${CLUSTER}"; then
  echo "cluster ${CLUSTER} already exists - reusing"
else
  echo "creating k3d cluster ${CLUSTER} (+ registry on :5006, Traefik LB on :7113)"
  k3d cluster create "${CLUSTER}" \
    --registry-create vig-registry:0.0.0.0:5006 \
    -p "7113:80@loadbalancer" \
    --wait --timeout 120s
fi

kubectl config use-context "k3d-${CLUSTER}" >/dev/null
echo "context: k3d-${CLUSTER}  |  app at http://vig.localhost:7113 (and via 'tilt up' port-forward :7112)"
