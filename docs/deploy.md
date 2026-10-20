# Deploy runbook

One image (`ghcr.io/forefy/vig`), one chart (`ghcr.io/forefy/charts/vig`), one ArgoCD Application. The same chart runs locally on k3d (with hot reload through Tilt) and in prod; only values differ. No staging: the k3d loop is the pre-prod check. AWS infrastructure and the domain are deferred (docs/decisions.md); nothing below assumes a cloud provider beyond a Kubernetes cluster with Traefik and cert-manager.

## What ships

| Artifact | Built by | Contains |
|---|---|---|
| `ghcr.io/forefy/vig:<version>` | `release.yml` on a `v*` tag | static `vig` and `vig-op`, prerendered `frontend/dist` at `/srv/web`, distroless nonroot, provenance + SBOM, linux/amd64 + arm64 |
| `ghcr.io/forefy/charts/vig:<version>` | same run | `deploy/chart` with `image.digest` pinned to the image built in that run |

Inside the image: `STATIC_DIR=/srv/web`, `DATA_DIR=/data`, `PORT=7112`, `VIG_ENV=prod`, `LOG_FORMAT=json`. `/data` is the only writable path; the chart mounts an emptyDir or a PVC there and an emptyDir on `/tmp`.

## Before the first prod sync

1. Namespace and Secret (the chart runs with `secrets.mode: existing`; helm `lookup` is unavailable under ArgoCD):
   ```bash
   kubectl create ns vig
   PG=$(openssl rand -hex 16)
   kubectl -n vig create secret generic vig-secrets \
     --from-literal=session-secret="$(openssl rand -hex 32)" \
     --from-literal=postgres-password="$PG" \
     --from-literal=database-url="postgres://vig:${PG}@vig-postgres:5432/vig?sslmode=disable"
   ```
   Add provider keys as they are set up (`google-client-id`, `github-oauth-client-secret`, `github-app-private-key`, ...; full list in `deploy/chart/values.yaml`). Every key except the three above is optional.
2. Image pull secret `ghcr-pull` in namespace `vig` (read-only `read:packages` token), the same one forefy's root seeds.
3. Copy `deploy/argocd/applications/vig.yaml` into the root repository's `applications/` directory. The `default` AppProject must exist. Set `ingress.host`, `app.treasuryPubkey`, `app.githubAppSlug`, `app.discordGuildId` in its `valuesObject`.
4. DNS for `ingress.host` at the cluster's Traefik LB; cert-manager issues the certificate through `letsencrypt-prod`.

## Release

```bash
git tag v0.1.1 && git push --tags        # release.yml: image + chart to GHCR, digest pinned
```

Then bump `targetRevision: 0.1.1` in `applications/vig.yaml` (root repo) and push to main. The root Application syncs the child; ArgoCD runs the migration Job (PreSync on upgrades) and rolls the Deployment.

Verify:

```bash
scripts/verify-release.sh 0.1.1          # polls GET /version until it matches (20 min default)
curl -fsS https://vig.forefy.com/readyz  # {"ok":true} once the DB answers
```

Or run the `verify-deploy` workflow with the version. A red check means the pointer bump did not roll: `argocd app get vig`, `kubectl -n vig get pods`, `kubectl -n vig logs job/vig-migrate`.

## Rollback

Bump `targetRevision` back to the previous version and push. Migrations are forward-only in the Job; if a release added a migration the previous binary cannot read, run `vig migrate down` from a pod of the new image first:

```bash
kubectl -n vig run migrate-down --rm -it --restart=Never \
  --image=ghcr.io/forefy/vig:<new> --overrides='{"spec":{"containers":[{"name":"m","image":"ghcr.io/forefy/vig:<new>","args":["migrate","down"],"envFrom":[],"env":[{"name":"DATABASE_URL","valueFrom":{"secretKeyRef":{"name":"vig-secrets","key":"database-url"}}}]}]}}'
```

## Database

- Default: internal Postgres StatefulSet (`postgres:17-alpine`, one replica, PVC). Password from `postgres-password` in the Secret; the app's `database-url` must embed the same password.
- Managed instance: `postgres.internal: false`, `database-url` points at it, and set `migrations.hook: pre-install,pre-upgrade` (the default `post-install,pre-upgrade` exists only because the internal Postgres does not exist during pre-install).
- Backups are the operator's job; `pg_dump` through `kubectl exec` on the StatefulSet pod is enough at this scale.

## Values that matter

| Value | Why |
|---|---|
| `image.digest` | Set by CI; wins over `tag`. Never set a tag by hand in prod |
| `secrets.mode: existing` | Required under GitOps |
| `app.workers` | `false` to run a second replica that only serves HTTP (workers on exactly one pod) |
| `data.persistence.enabled` | PVC on `/data` keeps tarball and advisory caches warm across restarts |
| `ingress.adminAllowlist` | Source CIDRs allowed to reach `/v1/admin` at the edge, on top of TOTP-gated admin auth |
| `optional.*` | ServiceMonitor, PodDisruptionBudget, NetworkPolicy, all off by default |

## Observability

- `/healthz` liveness (no dependencies), `/readyz` readiness (pings Postgres, 2s timeout), `/version` build version and commit.
- `/metrics` Prometheus: `vig_http_requests_total` and `vig_http_request_duration_seconds` by route pattern, method and status; `vig_rate_limited_total` by scope; Go and process collectors. `optional.serviceMonitor: true` scrapes it.
- Logs: one JSON line per request with the route pattern (never the raw path), status, duration, request id and client IP. Every response carries `X-Request-Id`; error bodies carry it as `instance`.
- Rate limits: 120/min per anonymous IP (10/min on `POST /v1/scans` and `POST /v1/auth/siws/nonce`), 600/min per user, 240/min per operator token; a 429 is problem+json with `Retry-After`.

## Local prod parity

```bash
make k3d-up      # cluster vig-dev, registry on :5006, Traefik LB on :7113
make tilt        # builds the binary and dist on the host, deploys deploy/chart with values.dev.yaml
```

App at `http://localhost:7112` (port-forward) or `http://vig.localhost:7113`. Editing a Go file recompiles and live-syncs the binary into the pod. Details in `deploy/dev/README.md`. Teardown: `make k3d-down`.

## Troubleshooting

| Symptom | Look at |
|---|---|
| Pods `ImagePullBackOff` | `ghcr-pull` secret in the namespace; `imagePullSecrets` in the Application values |
| `vig-migrate` Job failing | `kubectl -n vig logs job/vig-migrate`; `database-url` password matches `postgres-password` |
| `/readyz` 503, `/healthz` 200 | Postgres pod not ready, or DSN host wrong (`vig-postgres` is the headless Service) |
| ArgoCD shows the StatefulSet OutOfSync forever | `ignoreDifferences` on `volumeClaimTemplates` is in the Application |
| Sign-in redirects to the wrong host | `ingress.host` (drives `FRONTEND_URL`, `BACKEND_URL`, `SITE_URL`) or `app.siteUrl` when a CDN fronts another host |
| 429s from a shared office IP | Users should sign in; anonymous budgets are per IP by design |
