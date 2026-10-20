# Local dev on k3d + Tilt (prod parity)

Runs the same Helm chart production runs (`deploy/chart` + `values.dev.yaml`) on a local k3d
cluster with hot reload. This is the pre-prod check; there is no staging.

```bash
./deploy/dev/k3d-up.sh     # create the vig-dev cluster and registry (once)
tilt up                    # build, deploy, watch
```

- App: `http://localhost:7112` (Tilt port-forward) or `http://vig.localhost:7113` (Traefik).
- Edit any `.go` file: the binary recompiles on the host and live_update syncs it into the
  running pod and restarts the process, no image rebuild or helm upgrade.
- Frontend: the fast loop is Vite HMR (`make dev-frontend`). Tilt rebuilds `dist` and syncs it,
  which also exercises the prerender and the server-side head injection on `/r/:slug`.

## Two loops

| Loop | Use for | How |
|---|---|---|
| Native | tight iteration | `make dev` (Go on :7112, Vite on :7111) |
| Prod parity (this) | does it work as it will in prod | k3d + Tilt on the real chart |

## Dev vs prod

`values.dev.yaml` plus Tilt overrides: local image (`vig:dev`), TLS off, `vig.localhost`, a stable
`postgres.auth.password`, `LOG_FORMAT=text`, and a writable root pod so live_update can sync.
Prod keeps distroless-nonroot with a read-only root filesystem. `Dockerfile.dev` is dev only.

## Teardown

```bash
k3d cluster delete vig-dev
```
