# vig

Solana program audit service sold as credits. Read SPEC.md before doing anything. It is the source of truth for product, data model, API, job states, and build order.

## Read in this order
1. SPEC.md - the whole product and engineering spec. Section 2 is the decisions log; treat every row as a hard constraint.
2. docs/identity-brief.md - visual identity constraints. Design the identity before building screens.
3. docs/decisions.md - what was decided and why; add a row when you settle something. todo/ is gitignored scratch.

## Non-negotiables
- No paid API keys or paid services anywhere. Free tiers and public endpoints only.
- No mocks, no fixtures pretending to be the API, no "temporary" shortcuts in the frontend. The backend exposes a minimal real API from day one and the frontend uses only the client generated from its OpenAPI spec.
- No help text or explanatory copy in the UI. Facts are shown as badges, meters, timelines. If a screen needs a sentence to be understood, redesign the screen.
- Nothing is copied from any adjacent or competing product. Logo, mascot, copy, layouts, naming: all original.
- Target chains are adapters. Core packages never import Solana types.
- The operator API is job-scoped. There is no global operator key. Ever.
- Concierge phase: audits are run by an operator through the operator CLI. The UI, pricing and pipeline are built to the final shape anyway.

## Stack
- frontend/: React 18, Vite, TypeScript strict, Tailwind, shadcn/ui, react-router (data routers), TanStack Query, Solana wallet adapter, react-helmet-async. Vitest + Testing Library, Playwright for the wizard and checkout.
- backend/: Go 1.23+, chi, huma v2 (OpenAPI 3.1 generated from Go types), Scalar at /docs, pgx, goose migrations, Postgres.
- cmd/vig-op: operator CLI, same module as the backend.
- deploy/: Helm chart, k3d+Tilt loop, ArgoCD application, GHCR release. secrets/: git-secret encrypted env files, Makefile targets to reveal and hide them.

## Conventions
- IDs are UUIDv7. Timestamps UTC. Money in USDC base units (6 decimals) as int64.
- API errors are RFC 9457 problem+json.
- Every audit state change is an audit_events row; the user timeline renders those rows and nothing else.
- Ledger is append-only. Balance is derived, never edited.
- Corrections to a delivered finding are never deleted: archiving takes a reason and keeps the finding visible, re-rating takes a reason, and both histories stay on the finding and surface as lessons on the next job for the same repository.
- The generated TypeScript client lives in frontend/src/api/generated and is committed; `make gen` regenerates it from backend/openapi.json and CI fails on drift. Do not hand-edit it.
- Layouts come from the five archetypes in SPEC.md §13. Add a sixth only with a written reason in docs/decisions.md.
- Semantic colors (verified, caution, mismatch) are separate tokens from the accent.
- Finding prose (`description`, `normal_use`, `expected`, `impact`, any agent or operator that writes a report): short paragraphs split by blank lines, `- ` bullets for consequences and alternatives, backticks around files, functions, errors and arguments, a ``` fence for a code block or an exact value. Validation rejects a paragraph over 600 characters or a code block over 2000. Three or four blocks per finding. SPEC §9 has the shape; `vig-op template` prints it.
- A method that ran leaves something behind: a finding whose `evidence` names a tool, or says `live`, `fuzz` or `onchain`, is refused without an attachment on the finding or a step. `role: proof` on an attachment keeps it as evidence for operators and admins and out of every report a reader gets (`vig-op attach --proof`).
- Every submitted finding answers its `exhaustion`: vig lists five to ten things the reproduction had to rule out (`core.ExhaustionItems`, the dry run prints them), each answered `done` true or false with a `note` when not. The page shows a row of dots and the items behind a click.
- Prose may link to another finding of the same report as `[[Title of that finding]]`, for one the reader has to read first. Validation drops a link whose title no finding in the document carries, or a self link, keeping the words; the dry run advises against links in general.
- A `fix` is stricter: every line is a `- ` bullet, one per change, and a ``` fence right after a bullet is the definite fix for it (the correct code, the exact header value, the configuration line), rendered nested under that bullet. A fence whose info line names a place (`rust src/lib.rs:41-45`) renders as a patch against those lines at the pinned commit; `path:line` alone inserts. Validation rejects a fix with a non-bullet line. A located finding without a placed fence passes but the dry run advises one; `fix_kind: operational` says there is nothing to patch. A bullet with no definite code stands alone.

## Working order
Follow SPEC.md §18. Phase 0 (contract: types, state machine, ledger rules, adapter interface, stubbed huma routes, migrations) is done before any screen. Then the identity brief. Then screens. GitHub App, Discord, USDC payments, AWS infra and the domain are deferred until the UI is complete (docs/decisions.md); build their UI states now against the real API shapes, wire the integrations later.

## Local dev
- `make setup` installs tooling; `make dev` runs backend on :7112 and frontend on :7111.
- Secrets: `make decrypt-env` after you have a GPG key added to .gitsecret. Env files live in secrets/ and are committed encrypted only.
- Postgres locally via docker compose (deploy/local/compose.yaml). No SQLite.

## Open decisions (ask, do not guess)
See SPEC.md §19. Where a default is stated in §2 "Assumed defaults", use it and note it in docs/decisions.md.
