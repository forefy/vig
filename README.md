# vig

Security audits of on-chain programs and web applications, sold as credits and delivered as a report the owner can act on.

A customer points vig at a repository (Solana or Ethereum) or a live site. The deployed code is pinned and verified against the source, an operator works the audit in a sandbox, and the result is a report where every finding reads as a chain: how the code is normally used, what it must guarantee, the steps that break it with captures and recordings, who loses what. Fixes are bullets with patches against the exact lines. Severities can be re-rated with a reason, and the history stays.

## What is in the box

| Path | What |
|---|---|
| `backend/` | Go API: chi + huma (OpenAPI 3.1 from Go types, Scalar at `/docs`), pgx, goose migrations. `cmd/vig` is the server, `cmd/vig-op` the operator CLI |
| `frontend/` | React 18 + Vite + Tailwind + shadcn/ui. Public routes are prerendered; the app uses only the client generated from the OpenAPI document |
| `deploy/` | Helm chart, k3d + Tilt loop, ArgoCD application, operator sandbox image |
| `secrets/` | git-secret encrypted env files; the `*.example` files are the templates |
| `docs/` | identity, decisions log, operator guide, deploy and OAuth notes |
| `SPEC.md` | the product and engineering spec; the decisions in its section 2 are hard constraints |

## Run it locally

Needs Go 1.23+, Node 20+ and Docker (Postgres via compose).

```bash
make setup          # tooling
make db-up          # Postgres on :7132
make migrate        # schema
make seed           # demo users, targets and reports
make dev            # backend on :7112, frontend on :7111
```

Open http://localhost:7111. In dev the sign-in page offers seeded accounts (`admin`, `pro`, `free`); the API document is at http://localhost:7112/docs.

Env files: copy `secrets/.env.backend.example` to `secrets/.env.backend.local` and the frontend one alongside, and fill what you need; everything optional stays empty. The `*.secret` files are the maintainers' own, encrypted with git-secret, and are not needed to run the project.

## Working on it

- `make gen` regenerates the TypeScript client from `backend/openapi.json`. CI fails on drift. Never hand-edit `frontend/src/api/generated`.
- `make test` runs the Go and frontend suites; `make lint` the linters.
- Target chains are adapters (`backend/internal/adapter`, `chain`, `evm`); core packages never import a chain SDK.
- The operator API is job-scoped: a token claims one audit and is bound to it until delivery.
- Finding prose has a shape the server enforces; `vig-op template` prints it and `GET /v1/findings/shape` describes it.

`CLAUDE.md` is the working contract for anyone, human or agent, changing this repo. Read it with `SPEC.md` before a first change.

## License

MIT. See `LICENSE`.
