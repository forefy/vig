# ArgoCD

vig is deployed by forefy's existing ArgoCD installation, the app-of-apps root described in
the root deploy repository (root Application, `applications/` directory, bootstrap script,
git and OCI credentials). Nothing from that bootstrap is duplicated here. The only thing vig adds
is [`applications/vig.yaml`](applications/vig.yaml): copy it into the root repository's
`applications/` directory and the root syncs it.

## Roll a release

1. Tag: `git tag v0.1.1 && git push --tags`. The `release` workflow pushes
   `ghcr.io/forefy/vig:0.1.1` and the chart `ghcr.io/forefy/charts/vig:0.1.1`, digest pinned.
2. Bump `targetRevision` in `applications/vig.yaml` to `0.1.1`, commit to main.
3. Confirm: `scripts/verify-release.sh 0.1.1` (or the `verify-deploy` Action). It polls
   `GET /version` and fails if the rollout never lands.

## Secrets

`secrets.mode: existing` with `existingSecret: vig-secrets` in namespace `vig`. Keys are listed
in `deploy/chart/values.yaml`; only `session-secret`, `database-url` (and `postgres-password`
when `postgres.internal`) are required. Because the internal Postgres reads `postgres-password`
from the same Secret, the Secret has to exist before the first sync:

```bash
kubectl -n vig create secret generic vig-secrets \
  --from-literal=session-secret="$(openssl rand -hex 32)" \
  --from-literal=postgres-password="$(openssl rand -hex 16)" \
  --from-literal=database-url="postgres://vig:<postgres-password>@vig-postgres:5432/vig?sslmode=disable"
```

The image pull secret `ghcr-pull` is the same read-only GHCR token the root already seeds.

## Hard requirements inherited from the root

- The `default` AppProject must exist (core mode does not create it).
- `ignoreDifferences` on StatefulSet `volumeClaimTemplates`, or the Postgres StatefulSet reads
  perpetually OutOfSync.
- No helm `lookup` under GitOps: the repo-server renders without cluster access, which is why
  prod uses `secrets.mode: existing`.
- The migration Job hook runs post-install and pre-upgrade (ArgoCD PostSync / PreSync). See
  `migrations.hook` in the chart values for why pre-install cannot work with the internal
  Postgres.

AWS infrastructure (terraform) is deferred by decision; nothing here assumes a cloud provider.
