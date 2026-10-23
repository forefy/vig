# Security Audits, Down to the Bytecode

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/hero/hero-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/hero/hero-light.svg">
    <img src="docs/hero/hero-dark.svg" alt="A repository and its deployed bytecode are pinned together, the program's surface lights up under live traffic, a report comes out with a severity ladder and a finding read as a chain, and the owner's corrections flow back as lessons for the next audit" width="880"/>
  </picture>
</p>

<p align="center">
<a href="https://github.com/forefy/vig/releases/"><img alt="vig releases" title="vig releases" src="https://img.shields.io/github/release/forefy/vig"></a>
<img alt="vig GitHub repo size" title="vig GitHub repo size" src="https://img.shields.io/github/repo-size/forefy/vig">
<img alt="GitHub Actions Workflow Status" src="https://img.shields.io/github/actions/workflow/status/forefy/vig/.github/workflows/ci.yml">
<img alt="vig GitHub commit activity" title="vig GitHub commit activity" src="https://img.shields.io/github/commit-activity/m/forefy/vig">
<img alt="GitHub last commit" title="GitHub last commit" src="https://img.shields.io/github/last-commit/forefy/vig">
<a href="https://github.com/forefy/vig/issues/new"><img alt="Issues" title="Issues" src="https://img.shields.io/github/issues-raw/forefy/vig"></a>
<img alt="License" title="License" src="https://img.shields.io/github/license/forefy/vig">
<a href="https://twitter.com/forefy"><img alt="Forefy Twitter" title="Forefy Twitter" src="https://img.shields.io/twitter/follow/forefy.svg?logo=twitter"></a>
</p>

<p align="center">
<a href="https://github.com/forefy/vig/discussions" title="vig Discussions">Discussions</a>
| <a href="https://github.com/forefy/vig/issues/new" title="vig Feature request">Feature request</a>
| <a href="SPEC.md" title="vig Spec">Spec</a>
| <a href="https://twitter.com/messages/compose?recipient_id=1469398978185809922" title="Forefy Twitter DM">Twitter DM</a>
</p>

## <img src="frontend/public/icon-512.png" alt="" width="22" align="top"/> What is vig

vig is a security audit service for on-chain programs and live web applications, sold as credits and delivered as a report the owner can act on.

<p align="center">A delivered report</p>
<p align="center">
<img src="docs/screenshots/report.png" alt="A delivered report: the pinned repository and verified build, the severity ladder, the changes log, and the first finding read as a chain with the exploitable line marked" title="A delivered report" width="900"/>
</p>

Point it at a repository (Solana or Ethereum) or a live site. The deployed code is pinned and verified against the source, an operator works the audit in a sandbox with a live map of the target, and every finding comes back as a chain the owner can follow: how the code is normally used, what it must guarantee, the steps that break it with captures and recordings, who loses what. Fixes are patches against the exact lines at the pinned commit. Nothing a person corrects on a report is ever lost, and the next audit of the same code starts from those corrections.

The report above is the seeded demo (`make seed`, then `/r/vault-signer-check`): an invented program with invented findings.

- [How an audit runs](#how-an-audit-runs)
- [How a report reads](#how-a-report-reads)
- [How to run it locally](#how-to-run-it-locally)
- [What is in the box](#what-is-in-the-box)
- [For agents and operators](#for-agents-and-operators)
- [What is missing](#what-is-missing)

## How an audit runs

1. **Pin.** The repository and commit are resolved, the deployed bytecode is fetched, and the two are compared: `verified`, `self-built`, `mismatch` or `unverified`, said on every report.
2. **Map.** The target's surface is drawn before anyone reads a line: programs, instructions and accounts for a chain, hosts, routes and parameters for a site, lit by live traffic and by what the operator has reviewed.
3. **Work.** An operator claims the job with a token bound to that one audit and works it in a throwaway sandbox through the operator CLI. There is no standing key that can read every customer's code.
4. **Deliver.** The findings document is validated for shape, the code at every location is captured from the checkout, and the report goes to the owner. Exploit details are shown only to proven owners.
5. **Correct.** The owner re-rates or archives findings with a reason; an admin can revise. Every move stays on the finding, and surfaces as a lesson on the next job for the same repository.

## How a report reads

- **The severity ladder**: five hues, one per severity, never borrowed from a state colour.
- **A chain per finding**: normal use, expected, the numbered code steps with their lines marked and one capture or recording each, then impact with the finding's own attachments.
- **Fixes as patches**: a bullet per change; code that names its place renders as a diff against the pinned commit, linked to the lines.
- **A query line**: fold the findings by severity, status, who can reach them, how they were established, file, function or route, with counts that narrow as you type.
- **History that stays**: re-ratings and archives with their reasons, on the finding and in a folded changes log; the summary follows the current ratings.
- **One map for every kind of target**: the same picture for a program and a website, with the words swapped.

## How to run it locally

Needs Go 1.23+, Node 20+ and Docker (Postgres via compose).

```bash
make setup          # tooling
make db-up          # Postgres on :7132
make migrate        # schema
make seed           # demo users, targets and reports
make dev            # backend on :7112, frontend on :7111
```

Open http://localhost:7111 and use the `admin` dev login on the sign-in page; other users are made from the admin's Users page. The API document is at http://localhost:7112/docs.

Env files: copy `secrets/.env.backend.example` to `secrets/.env.backend.local` and the frontend one alongside, and fill what you need; everything optional stays empty. The `*.secret` files are the maintainers' own, encrypted with git-secret, and are not needed to run the project.

## What is in the box

| Path | What |
|---|---|
| `backend/` | Go API: chi + huma (OpenAPI 3.1 from Go types, Scalar at `/docs`), pgx, goose migrations. `cmd/vig` is the server, `cmd/vig-op` the operator CLI |
| `frontend/` | React 18 + Vite + Tailwind + shadcn/ui. Public routes are prerendered; the app uses only the client generated from the OpenAPI document |
| `deploy/` | Helm chart, k3d + Tilt loop, ArgoCD application, operator sandbox image |
| `secrets/` | git-secret encrypted env files; the `*.example` files are the templates |
| `docs/` | identity, decisions log, operator guide, deploy and OAuth notes |
| `SPEC.md` | the product and engineering spec; the decisions in its section 2 are hard constraints |

## For agents and operators

The findings document is the one exchange format: the operator CLI posts it, an agent emits it, the report page renders it, the JSON export is it. Everything an author needs is in the API:

- `GET /v1/findings/shape` lists the required, recommended and optional fields, the rules validation applies, and a filled example.
- Every field's description in the OpenAPI document opens with `Required`, `Recommended` or `Optional`.
- `POST /v1/op/jobs/{id}/report/check` is a dry run: what would be refused, and what the report reads better with.
- A claimed job carries the lessons from earlier reports on the same repository: what was archived as not relevant and what was re-rated, with the reasons.

`vig-op template`, `vig-op attach`, `vig-op report --check` do the same from the sandbox. The operator guide is at [docs/operator.md](docs/operator.md).

Working on the code: `make gen` regenerates the TypeScript client and CI fails on drift; `make test` and `make lint` run the suites. `CLAUDE.md` is the working contract for anyone, human or agent, changing this repo.

## What is missing

Deferred until the product surface is complete, with their UI states already built against the real API shapes: the GitHub App for private repositories, Discord notifications, USDC checkout, production infrastructure and the domain. The decisions log in [docs/decisions.md](docs/decisions.md) says why.

## License

MIT. See [LICENSE](LICENSE).
