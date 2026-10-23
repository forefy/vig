# vig - Product & Engineering Spec

Status: v1 draft, 2026-09-09. vig is a reinvention: no logo, mascot, copy, layout or concept is carried over from them or from any other product in the space.

---

## 1. What it is

A Solana program audit service sold as credits. Users point it at a repo and a mainnet program ID, the intake verifies every input against the chain and the source, an audit runs, and a pinned report comes back within 24 hours.

Phase 1 is concierge: the "agent" is an operator running agentic tooling through a scoped operator API. The UI, pricing, consumption, and job pipeline are built to the final shape so the autonomous agent later replaces the operator without touching the product.

### Principles

- Visuals over text. No help copy. Verification badges, meters, and timelines do the explaining.
- One design language, several layout archetypes. No two pages identical, all obviously siblings.
- Minimal real API from day one. No mocks, no shortcuts. Generated client from the OpenAPI spec so frontend and backend cannot drift.
- Zero paid API keys. Free tiers and public endpoints only.
- Target is an adapter. Solana is adapter one. The core never imports Solana types.
- Original identity. Logo, palette, type, layouts and naming are designed for vig from scratch. Nothing is borrowed from the reference screenshots or from adjacent products.

---

## 2. Decisions log

Settled in discussion. Each is a constraint on the build.

| Area | Decision |
|---|---|
| Pricing | Free: 1 audit via tweet. Mini audit: $25, 1 run. Pro: $540 paid once, 1 run/day for 12 months, expires, no cancellation, renew is a new purchase. Team / Enterprise: talk to us. |
| Rails | USDC on Solana, one transfer per purchase. No recurring layer, no card processor. Invoice for Team/Enterprise. |
| Price display | Plan card says $45 / month. Pay button says the total. One line under it: billed once, no refunds. |
| Refunds | None. USDC is final. Stated before payment. |
| Free audit gate | User posts about vig on X including a per-user nonce, pastes the link. Admin approves by eye. Credit granted on approval. No X API, no automation. |
| Anonymous scans | Public repos, up to 5 per day per IP+fingerprint, no account. Scan is the automated pre-flight only (see §7). Results at an unguessable, noindex, expiring URL. |
| Sources | Public GitHub URL, or GitHub App for private repos. No zip upload. No other hosts for now. |
| Programs | Fetch from chain by program ID. Upload .so removed. "Build it for me" folded into the standard verified-build check. |
| Chain data | Public mainnet RPC as fallback, one free-tier keyed RPC as primary, all calls server-side, cached by program ID + slot. OtterSec verified-build registry for source-to-bytecode matching. |
| Ownership | Program ownership proven by signing a nonce with the upgrade authority key. Repo ownership via the GitHub App installation. Non-owners get findings without exploit paths; critical findings on live third-party programs go to the owner. |
| Auth | Google, GitHub, Sign-In-With-Solana (wallet adapter: Phantom, Solflare, Backpack, MetaMask Solana). One account, many linked identities. |
| Notifications | Discord bot DM, linked via Discord OAuth in settings, requires joining the vig server (which is also support). In-app is the source of truth. Email optional when the identity provides one. |
| Concierge disclosure | "Audits run by our agents with human review." On the queue screen and in the terms. Operators see the code. |
| Retention | Clone deleted after report is stored. Retention window visible in settings. |
| Dedup | Same repo + commit + program set returns the existing report, no credit burned. |
| Quota | Pro: one run per rolling 24h, no rollover, timer visible. |
| Capacity | "Delayed" state with new ETA when the queue exceeds capacity, pushed through notifications. |
| Sidenav | Dashboard, Audits, Settings. |
| Frontend | React + Vite + TypeScript, shadcn/ui, Tailwind, react-query, react-router, generated API client. |
| Backend | Go, chi, huma for OpenAPI generation, Scalar for docs. Postgres. |
| Infra | git-secret at /secrets, argocd + helm chart, terraform, GHCR release workflow. |

### Assumed defaults (change if wrong)

| Open item | Default taken in this spec |
|---|---|
| Mini audit cap | 1 program, up to 10,000 lines of Rust in scope. Shown as a meter on the scope step. Over cap: needs Pro or Team. |
| Report visibility | Private by default. Owner-verified users can publish a report page. Free-tier reports are not forced public. |
| Pro cap per run | Up to 3 programs, up to 40,000 lines in scope. |
| Anonymous scan definition | Automated pre-flight only: clone, tree, chain facts, declared-ID match, verified-build check, cargo-audit advisories. No AI findings. |
| Database | Postgres from day one (ledger, concurrent operator + users, argocd deploy). |

---

## 3. Users and tiers

| Tier | Gets | Requires |
|---|---|---|
| Anonymous | 5 pre-flight scans / day on public repos | nothing |
| Signed in | Save scans, request free audit, buy credits | any identity |
| Free audit | 1 full audit credit | approved tweet claim |
| Mini | 1 full audit credit per $25 | USDC purchase |
| Pro | 1 full audit / rolling 24h for 365 days | $540 USDC once |
| Team / Enterprise | custom quota, invoice, Slack support later | contact |

Consumption model: every full audit consumes one **credit** or one **daily slot**. Credits have an expiry (12 months). Pro is a plan row with start/end and a last-run timestamp; it does not create credits. The ledger records every movement: grant, purchase, consume, revoke, expire.

---

## 4. Flows

### 4.1 Anonymous scan
Landing page has one input: GitHub URL. Optional program ID appears after the URL validates. Submit → scan job → result page at `/s/{token}`. Result page shows the pre-flight panel (§7) and one call to action: sign in to save and request an audit. Meter shows scans left today.

### 4.2 Sign in
`/login` shows three buttons: Google, GitHub, Wallet. Wallet opens the Solana wallet adapter modal and runs SIWS. First sign-in creates the account. Any later provider from settings links to the same account.

### 4.3 Connect GitHub
One action that installs the GitHub App and, if the user is not signed in with GitHub, links the GitHub identity. Installation pending org approval shows the pending state with who must approve. Installation list appears in settings and in the source step.

### 4.4 New audit wizard (Solana adapter)
Five steps, sticky order summary on the right. Steps come from the adapter (§8).

1. **Target** - cards: Solana program (adapter one), EVM contract (adapter two), Web app (adapter three), others greyed as "soon". Coverage: Full or Diff, when the adapter offers it.
2. **Source** - cards: Public GitHub, Connect GitHub (or installation picker if connected). URL field with branch/tag/commit selector. On validation: repo card with default branch, resolved commit, author, date. Diff coverage adds base and head.
3. **Programs** - program ID input, Fetch. Each fetched program renders a verification panel (§7). Declared ID in source matched to the entered ID. Verified-build badge. Ownership badge with "Prove" button (sign nonce with upgrade authority). Multiple programs allowed within the tier cap.
4. **Scope** - file tree, program files auto-selected, lockfiles/tests/config unselected. A meter shows lines in scope against the tier cap. Clear / Select all.
5. **Review** - pinning line: repo, commit, programs with bytecode hashes and slots, files, lines, cost (1 credit or today's slot). Button: Queue audit. If no credit: Buy credits, which returns here after purchase.

### 4.5 Queue and timeline
Audit detail page shows the timeline. Stages are the job states (§6). ETA shown as a countdown. Disclosure line under the timeline: audits run by our agents with human review. Delayed state replaces the ETA with a new one.

### 4.6 Report
Header: pinning line plus match status (verified / self-built / mismatch). Findings list grouped by severity, each with location (file:line and program + instruction), description, exploit path (owner only), fix, status control (open / fixed / acknowledged / disputed). JSON export. Publish toggle for owners → public page at `/r/{slug}` with OG image.

### 4.7 Re-audit
From a report: "Audit new commit" opens the wizard prefilled with Diff coverage, base = audited commit. Findings carry over and statuses update.

### 4.8 Billing
Balance card: credits, Pro plan with expiry and next slot timer. Plan cards: Mini, Pro, Team. Checkout: amount, promo code, Select Wallet, Pay $X. Under the button: billed once, no refunds. Ledger table below.

USDC flow: backend creates a purchase with a memo nonce and expected amount → frontend builds a transaction: memo instruction + USDC transfer to the treasury → user signs → frontend posts the signature → backend confirms on chain (memo matches, amount matches, recipient matches, finalized) → credits or plan granted → ledger row.

### 4.9 Free audit via tweet
Dashboard card when the user has no credits: "Post about vig, get an audit." Shows the nonce and a prefilled post with a share button. Field to paste the link. States: submitted, approved, rejected, revoked. Admin approves in the admin dash.

### 4.10 Settings
Profile (name, avatar from identity). Identities (Google, GitHub, wallet, Discord; link / unlink). GitHub installations. Notifications (Discord connected state, email toggle). API keys (create, revoke, last used). Data (retention window, delete account).

### 4.11 Admin
Separate route group, `admin` role, TOTP required. Queue (claim, reassign, delay with ETA). Audits: every audit across users with filters (state, adapter, user, hidden, repo), and on any audit: put it in a state (any pipeline state from any state, delivered only with a report; leaving failed or cancelled pays for the audit again, leaving delivered takes a public report private; failed and cancelled by their own rules: fail from a running state only, cancel from any state with the credit or slot coming back and a public report going private), hide (cancel and hide while the cost is held; hide alone once it is back), unhide, delete (the row and everything under it; a held cost comes back first, the ledger keeps its rows, the action log keeps the receipt), and revise a delivered report by posting its findings document back (a new report version; statuses carry over by finding id or by title and file; the owner sees `rev N`). Tweet claims (approve / reject with the link and nonce visible). Credits (grant / revoke with reason). Users (identities, plans, ledger). Operator tokens. Every action writes an operator event that the affected user sees on their timeline. A hidden audit is gone from its owner's list and 404s for them. A deleted audit takes its events with it; the ledger is never deleted from.

---

## 5. Domain model

Postgres. IDs are UUIDv7. Timestamps UTC.

```
users            id, display_name, avatar_url, role (user|admin), created_at
identities       id, user_id, provider (google|github|solana|discord), provider_id, email?, handle?, created_at
sessions         id, user_id, expires_at, ip, ua
api_keys         id, user_id, prefix, hash, name, last_used_at, revoked_at

github_installations  id, user_id, installation_id, account_login, account_type, status (active|pending|removed)

targets          id, adapter (solana), owner_user_id?, source_kind (public|installation), repo_full_name,
                 installation_id?, created_at
target_programs  id, target_id, program_id, loader, upgrade_authority?, last_deploy_slot, bytecode_sha256,
                 idl_present, anchor_version?, verified_build (verified|self_built|mismatch|unverified),
                 verified_commit?, ownership_proven_at?, ownership_signature?

scans            id, token, target_id?, repo_full_name, commit_sha, program_id?, ip_hash, fingerprint_hash,
                 state (queued|running|done|failed), result jsonb, expires_at, created_at

audits           id, user_id, target_id, coverage (full|diff), base_sha?, head_sha, programs jsonb (ids),
                 scope jsonb (files, lines), state (see §6), eta_at, delayed_reason?, cost_kind (credit|slot),
                 ledger_entry_id?, report_id?, created_at, updated_at
audit_events     id, audit_id, kind, actor (user|operator|system), actor_id?, payload jsonb, created_at

reports          id, audit_id, pin jsonb (repo, commit, programs[{id, slot, hash}], match_status),
                 summary jsonb (counts by severity), published_slug?, published_at?, created_at
findings         id, report_id, ordinal, severity (critical|high|medium|low|info), title,
                 location jsonb (file, line_start, line_end, program_id?, instruction?), description,
                 exploit_path? (owner-only), fix, status (open|fixed|acknowledged|disputed),
                 carried_from_finding_id?, created_at, updated_at

plans            id, user_id, kind (pro|team), starts_at, ends_at, run_cap_per_day, last_run_at, purchase_id
purchases        id, user_id, sku (mini|pro|credits_n), amount_usdc, memo_nonce, treasury, state
                 (pending|confirmed|failed|expired), tx_signature?, confirmed_slot?, created_at
ledger           id, user_id, kind (grant|purchase|consume|revoke|expire), delta, balance_after,
                 ref_kind, ref_id, reason?, actor_id?, created_at

tweet_claims     id, user_id, nonce, url, state (submitted|approved|rejected|revoked), reviewed_by?,
                 reviewed_at?, ledger_entry_id?, created_at

notifications    id, user_id, kind, payload jsonb, channels jsonb, read_at?, delivered jsonb, created_at
operator_tokens  id, audit_id, hash, scope jsonb, expires_at, used_at?
admin_actions    id, admin_id, kind, subject_kind, subject_id, payload jsonb, created_at
```

---

## 6. Audit job state machine

```
queued ──► claimed ──► cloning ──► programs_verified ──► analysis ──► human_review ──► delivered
   │          │            │              │                 │              │
   └──────────┴────────────┴──────────────┴─────────────────┴──────────────┴──► failed (reason, credit refunded)
                                                                 any ──► delayed (eta) ──► back to previous
                                              any, delivered included ──► cancelled (admin; reason, credit refunded)
```

- `queued`: credit or slot consumed, ETA = now + 24h.
- `claimed`: operator claimed, scoped token minted.
- `cloning`: repo cloned in a throwaway container at head_sha.
- `programs_verified`: bytecode hash re-fetched, self-build compared, match status recorded.
- `analysis`: agentic tooling runs. Phase 1 operator, phase 2 autonomous.
- `human_review`: findings reviewed before delivery.
- `delivered`: report stored, clone deleted, notification sent.
- `delayed`: ETA replaced, notification sent. Returns to the stage it left.
- `failed`: reason visible to user, credit or slot restored.
- `cancelled`: an admin's decision from any state, delivered included; reason visible to the user, credit or slot restored unless it already was, operator tokens revoked, a public report goes private. Terminal.

Every transition is an `audit_events` row. The user timeline renders these rows; the operator adds no free text.

---

## 7. Solana adapter intake - the verification panel

The panel replaces help text. Each row is a fact fetched by the backend, rendered as a badge.

| Row | Source | States |
|---|---|---|
| Program exists | getAccountInfo | found / not found |
| Loader | account owner | upgradeable / non-upgradeable / unknown |
| Upgrade authority | program data header | pubkey / none (immutable) |
| Last deployed | program data slot | slot + approx date |
| Bytecode hash | sha256 of program data | hash, copy button |
| IDL on chain | Anchor IDL PDA | present (anchor version) / absent |
| Declared ID matches source | grep declare_id! / Anchor.toml in clone | match / mismatch / not found |
| Verified build | OtterSec registry | verified (commit) / unverified |
| Source matches chain | solana-verify build of head_sha vs bytecode hash | verified / self-built match / mismatch / pending |
| IDL matches source | instruction names in the on-chain IDL vs handlers found in the clone | match / mismatch (+n idl, +n source) / no IDL / not found / pending |
| Ownership | signature over nonce by upgrade authority | proven / not proven / immutable |

Rules:
- Unverified is neutral (grey). Mismatch is red. Proven and match are green.
- "Source matches chain" runs at intake when a build cache exists, otherwise during `programs_verified`. The panel shows pending, never blocks.
- All reads via backend, cached by (program_id, slot). Public RPC fallback. No browser RPC calls.

Pre-flight scan (anonymous and first stage of every audit) = this panel + file tree + lines in scope + cargo-audit advisories on Cargo.lock + the source summary (programs, entrypoints, accounts, raw accounts, CPIs, signed CPIs, PDAs, token ops, unsafe blocks, code lines). Deterministic, no AI, seconds to a minute.

Lines are counted twice: raw lines, and code lines with blanks and comments removed. The tier cap and the scope meter use code lines.

### Ethereum adapter (adapter two) - the contract panel

Same five steps; contracts and a chain where programs were. Chains served: Ethereum, Base, Sepolia, each through a public RPC endpoint; a program row keeps its chain id. The store keeps a contract address in the adapter's canonical spelling (lowercase), which is what its map nodes and evidence use, whatever was typed.

| Row | Source | States |
|---|---|---|
| Contract | eth_getCode | found (bytes) / not found |
| Chain | the intake | name + chain id |
| Bytecode | keccak of the code | hash, copy button |
| Verified source | Sourcify (no key) | verified / unverified |
| Proxy | EIP-1967 implementation slot | proxy (implementation) / no proxy |
| Upgrade admin | EIP-1967 admin slot | address / none / immutable |
| Compiler | Sourcify's compilation record | version · via_ir or legacy codegen · evm target / unknown |
| Interface | the verified ABI of the contract, or of its implementation | n functions, n events / unknown |
| Holds | Blockscout token balances of the address | n tokens (symbol and amount each) / nothing |
| Interface matches source | exposed selectors of the best deployable contract in the clone vs the verified ABI; without one, vs the selectors the bytecode pushes (PUSH1..PUSH4 constants, the implementation's for a proxy) | match / mismatch (+n chain, +n source) / selectors in bytecode (n/n) / partial (n/m) / not in bytecode / not found / pending |

The Solidity scan is pattern-based like the Rust one: contracts with their inheritance, every external or public function with the selector its canonical signature implies (structs as tuples, enums as uint8, contracts and unknown types as address, user-defined value types as what they wrap), events with topics, and the sites a reviewer looks for first (calls, delegatecalls, transfers, assembly, unchecked, selfdestruct). Tests, scripts and vendored libraries are skipped. Selectors that nobody verified are named through the 4byte directory.

### Web adapter (adapter three) - the site panel

Four steps: target, site, scope, review. A site is a live origin where a repository was; an optional public repository and revision stay as the snapshot the operator reads. The scope step is the consent §12 and §20 require: the hosts the review may probe (the origin's host always, the rest only its subdomains) and the path prefixes it may walk on each one; the target keeps the scope and the audit is pinned to it as URL prefixes, no programs, no lines, full coverage only.

| Row | Source | States |
|---|---|---|
| Site | one GET of the origin | status (2xx, 3xx ok; 4xx, 5xx check) / unreachable |
| Scheme | the origin | https / http (check) |
| Redirects to | where the GET landed | none / another origin (check) |
| Server | the Server header | its value / unnamed |
| HSTS | Strict-Transport-Security | present / absent (check) |
| Ownership | nonce found at `_vig.<host>` TXT or in `/.well-known/vig-verify.txt` | proven (dns, well-known) / not proven, with the prove action |

The nonce is issued per target and lives a day; the backend reads both places with plain lookups and the first hit wins. Nothing is crawled at intake: the one GET of the origin is the whole read until the audit is queued inside the scope.

---

## 8. Target adapter interface

```go
type Adapter interface {
    Key() string                                   // "solana"
    IntakeSteps() []StepSpec                       // ordered steps with field specs, rendered by the wizard engine
    Validate(ctx, IntakeDraft) ([]Issue, error)    // per-step validation
    Verify(ctx, IntakeDraft) (VerificationPanel, error) // the §7 facts
    Preflight(ctx, Clone, IntakeDraft) (Preflight, error)
    LocationKinds() []LocationKind                 // file:line, program+instruction
    OwnershipProof() OwnershipProofSpec            // how to prove ownership (sign with upgrade authority)
}
```

The wizard engine renders `StepSpec` (cards, inputs, tree, meter, list) and calls Validate/Verify. The target cards are every adapter's target options, read from `/v1/adapters`; a `site` step marks a live target, a `source` step a repository, and the engine never names an adapter. Adding a chain or a web-app target is a new package and a new card. The core, ledger, jobs, reports, and findings never change.

---

## 9. Findings schema (JSON, stable across phases)

```json
{
  "report": {
    "audit_id": "…",
    "pin": {
      "repo": "vig-demo/vault-programs",
      "commit": "24555d04…",
      "programs": [{"id": "…", "slot": 1234, "bytecode_sha256": "…", "match": "self_built"}],
      "excluded": ["audits/", "frontend/", "contracts/test/"]
    },
    "summary": {"critical": 1, "high": 2, "medium": 0, "low": 3, "info": 1},
    "known": [{"title": "No order expiration", "severity": "low", "source": "Cyfrin 2026-03", "status": "acknowledged", "reference": "audits/2026-03-04-cyfrin.pdf"}]
  },
  "findings": [{
    "ordinal": 1,
    "severity": "critical",
    "title": "Missing signer check on withdraw",
    "location": {"file": "programs/vault/src/lib.rs", "line_start": 41, "line_end": 58,
                 "program_id": "…", "instruction": "withdraw"},
    "description": "…",
    "exploit_path": "…",
    "fix": "…",
    "references": ["…"],
    "status": "open",
    "normal_use": "…",
    "expected": "…",
    "sites_kind": "flow",
    "sites": [{"location": {"file": "…", "line_start": 12}, "note": "input enters",
               "visuals": [{"id": "…", "kind": "video", "caption": "the drain, recorded", "mime": "video/mp4", "size": 1834002}]}],
    "impact": "…",
    "visuals": [{"id": "…", "kind": "file", "caption": "the script that ran it", "mime": "text/x-python", "name": "poc.py", "size": 2210}]
  }]
}
```

The operator CLI posts exactly this. The future agent emits exactly this. The report page renders exactly this. `exploit_path` is stripped for non-owners.

A finding can be archived after delivery: judged not relevant, always with a short reason, by the owner on the report page or by an admin. An archived finding leaves the severity groups and the summary but never the report: it sits in an archived strip at the end with its reason, who and when, and the same control brings it back. Every status move is a row in `status_changes` (from, to, reason, by, at), append-only, carried across revisions like re-ratings. The report ends with a changes log: every re-rating and status move on any of its findings, newest first.

Corrections are lessons. `GET /v1/admin/lessons` lists every archive and re-rating on delivered reports with its reason, one repository or all (moves made while a report is still in review are the operator iterating, not a lesson; a revision's own "revision N" rows are left out), and the admin page has a Lessons tab over it. A claimed job carries `lessons` for its repository, so the operator or agent reads what the last reports got wrong before reading the code; `vig-op claim` and `vig-op status` print them first.

A finding's `severity` can be re-rated after delivery, up or down, by the owner on the report page or by an admin, always with a short reason. Every re-rating is a row in `severity_changes` (from, to, reason, by, at), append-only, shown under the finding's title oldest first; the report's summary follows the current rating. A revision that rates a kept finding differently adds its own row, "revision N", so the trail has no silent step, and the history follows the finding into each new version.

A writer learns the shape from the API, not from this file: `GET /v1/findings/shape` lists the required, recommended and optional fields, the rules validation applies and a filled example; every field's description in the OpenAPI document opens with Required, Recommended or Optional; and `POST …/report/check` is a dry run that returns what would refuse the document and what it leaves out, so an agent can iterate before it posts. `vig-op report --check` does the same from the sandbox.

`pin.excluded` lists the folders the review left out, shown as a row of the scope block with nothing but the paths. `known` lists what earlier reports raised on the same code, with each item's state at the pinned commit; the page shows them as a strip above the findings, they are not counted in the summary, and a finding one of them already covers is not raised again.

A finding's code renders as a reproduction chain the reader steps through, never a stack of blocks. The chain opens on `normal_use`, how the code is ordinarily called and by whom, then `expected`, what it is supposed to guarantee at the location; runs through the code steps (the location alone, or `sites` in order), and closes on `impact`, who loses what when it happens. All three are prose in the same shape as `description`; all are optional and older reports simply have shorter chains. A step shows one attachment at most, under its code; the finding's own `visuals` show on the impact item; a finding carries one recording in all, on a step or on itself, so the script that ran it and the recording of it sit where the damage is described and nowhere else.

`visuals` are attachments, on a finding or on one of its flow steps (one per step): the bytes are uploaded first (`POST …/visuals`, base64, by the operator before the report or by an admin for a revision) and the document carries only the reference. `kind` follows the type: png, jpeg, webp and gif are pictures shown inline; mp4 and webm are recordings played inline with seeking; any other type is a file the reader downloads under `name`, served as opaque bytes. Images up to 8MB, recordings 64MB, files 16MB. The server rebinds mime, name and size from the stored bytes and refuses a document that references an attachment it does not hold for that audit.

`description`, `normal_use`, `expected` and `impact` are plain text with one light structure, written for a reader in a hurry: blank lines separate short paragraphs, lines starting with `- ` are bullets, backticks mark files, functions and errors, and a ``` fence (closed by a line that is only ```) holds a code block or an exact value such as a returned header. The page renders that structure (spaced paragraphs, bullet lists, code spans, code blocks, and any source path set as code). Validation rejects a paragraph over 600 characters or a code block over 2000, so a wall of text cannot be posted. Three or four blocks per finding is the norm.

`fix` is the same text in a stricter shape: every line is a `- ` bullet, one per change, and a fence right after a bullet is the definite fix for that bullet, rendered nested under it: the correct code, the exact header value, the configuration line, whatever closes the finding outright. A fence that names where it applies on its info line, as `rust programs/x/src/lib.rs:41-45`, renders as a patch: the original lines from the pinned commit as `-`, the fence as `+`, line numbers in the gutter, the location linking to those lines in the repository; `path:line` alone inserts before that line. The server captures those lines into `fix_sites` on submission, like snippets. A bullet with no definite code stands alone. A finding that names a file should carry a placed fence; when it does not, the dry run says so, unless the finding declares `fix_kind: operational` (rotate, revoke, configure: nothing to patch). Validation rejects a fix with a non-bullet line, no bullet at all, or a fence place that is not `path:line` or `path:start-end`.

---

## 10. API surface

Go, chi router, huma for typed handlers and OpenAPI 3.1 generation, Scalar served at `/docs`. TypeScript client generated from the spec in CI; the frontend imports only the generated client.

### Public
```
POST /v1/scans                       anonymous, rate limited
GET  /v1/scans/{token}
GET  /v1/reports/{slug}              published only
GET  /v1/plans                       pricing table (source of truth for the UI)
GET  /v1/findings/shape              required, recommended and optional finding fields, the rules, a filled example (§9)
```

### Auth
```
GET  /v1/auth/{provider}/start       google | github | discord
GET  /v1/auth/{provider}/callback
POST /v1/auth/siws/nonce
POST /v1/auth/siws/verify
POST /v1/auth/logout
GET  /v1/me
```

### User
```
GET/PATCH /v1/me
GET/DELETE /v1/me/identities/{id}
GET/POST/DELETE /v1/me/api-keys
GET  /v1/me/github/installations
GET  /v1/me/github/install-url
GET  /v1/me/notifications            PATCH …/{id}/read
GET  /v1/me/ledger
GET  /v1/me/plan

POST /v1/targets                     adapter, source, revision → resolved target
GET  /v1/targets/{id}
POST /v1/targets/{id}/programs       program_id → verification panel
POST /v1/targets/{id}/programs/{pid}/ownership/nonce
POST /v1/targets/{id}/programs/{pid}/ownership/verify
GET  /v1/targets/{id}/tree           file tree + auto selection + lines

POST /v1/audits                      target, coverage, programs, scope → queued (consumes credit/slot)
GET  /v1/audits                      list
GET  /v1/audits/{id}                 with events
GET  /v1/audits/{id}/report
PATCH /v1/audits/{id}/report/findings/{fid}   status
POST /v1/audits/{id}/report/publish  DELETE to unpublish
PATCH /v1/audits/{id}/report/findings/{fid}            status, reason (required for archived) → finding; the move joins its history
PATCH /v1/audits/{id}/report/findings/{fid}/severity   severity, reason → finding; the change joins its history, the summary follows
GET  /v1/audits/{id}/report.json

POST /v1/purchases                   sku → memo nonce, amount, treasury
POST /v1/purchases/{id}/confirm      tx signature
GET  /v1/purchases/{id}

POST /v1/tweet-claims                url → submitted
GET  /v1/tweet-claims
```

### Operator (bearer operator token, job-scoped)
```
POST /v1/op/jobs/claim               → audit + scoped token + clone credentials (short-lived installation token)
POST /v1/op/jobs/{id}/events         state transitions
POST /v1/op/jobs/{id}/programs       verification results (hashes, match)
POST /v1/op/jobs/{id}/visuals        attachment bytes (base64) for a finding → the reference §9 carries
POST /v1/op/jobs/{id}/report/check   findings JSON → {valid, errors, advice}; nothing stored
POST /v1/op/jobs/{id}/report         findings JSON (§9)
POST /v1/op/jobs/{id}/delay          eta, reason
POST /v1/op/jobs/{id}/fail           reason
```

### Admin (session + admin role + TOTP)
```
GET  /v1/admin/queue
GET  /v1/admin/audits                state, user_id, adapter, hidden, q, limit
POST /v1/admin/audits/{id}/reassign
POST /v1/admin/audits/{id}/fail      reason
POST /v1/admin/audits/{id}/cancel    reason, hide
POST /v1/admin/audits/{id}/hide      hidden, reason
POST /v1/admin/audits/{id}/state     state, reason   (any pipeline state, or failed / cancelled)
DELETE /v1/admin/audits/{id}         reason optional → receipt (events, reports, refunded)
POST /v1/admin/audits/delete         ids[], reason optional → {deleted[], failed[]}; one click on the list for any number of ticked rows
GET  /v1/admin/audits/{id}/report.json
POST /v1/admin/audits/{id}/visuals   attachment bytes (base64) for a revision → the reference §9 carries
POST /v1/admin/audits/{id}/report/check  findings document → {valid, errors, advice}; nothing stored
POST /v1/admin/audits/{id}/report    findings document → new revision
PATCH /v1/admin/audits/{id}/report/findings/{fid}            status, reason → finding, as admin
PATCH /v1/admin/audits/{id}/report/findings/{fid}/severity   severity, reason → finding, as admin
GET  /v1/admin/lessons               repo, limit → every archive and re-rating with its reason, newest first
GET  /v1/admin/tweet-claims          POST …/{id}/approve | reject | revoke
POST /v1/admin/credits/grant         user, delta, reason
POST /v1/admin/credits/revoke
GET  /v1/admin/users  GET /v1/admin/users/{id}
POST /v1/admin/users                 display_name, credits, plan → user made by hand (no identity; dev login by id, or an identity linked later)
POST /v1/admin/operator-tokens
GET  /v1/admin/actions
```

Errors: RFC 9457 problem+json. Rate limits per IP for public, per user for authed, per token for operator.

---

## 11. Auth details

- **Google, GitHub**: OAuth code flow, callback on the backend, session cookie (HttpOnly, SameSite=Lax). Console setup notes in `docs/oauth-providers.md`.
- **GitHub App**: separate from OAuth. Installation webhook stores installation rows. Installation tokens minted per operator job, valid one hour, scoped to the target repo. The user's OAuth token never leaves the backend.
- **SIWS**: nonce endpoint, wallet signs the standard Sign-In-With-Solana message, backend verifies ed25519, identity = pubkey. Wallet adapter modal covers Phantom, Solflare, Backpack, MetaMask.
- **Discord**: OAuth for linking only, scope `identify`. Bot DMs require the user in the vig guild; settings shows joined state and an invite.
- **Sessions**: 30 days sliding. API keys: `vig_` prefix, sha256 stored, shown once.
- **Admin**: `role=admin` plus TOTP enrolled. Admin routes refuse sessions without a recent TOTP.

---

## 12. Operator surface (the crucial part)

- `vig-op` CLI in Go, same repo. `vig-op claim` → prints job, writes a scoped token to a temp file, clones into a throwaway container (Docker) at head_sha using the installation token. `vig-op verify` runs solana-verify and posts hashes. `vig-op event <state>`. `vig-op report findings.json` validates against §9 and posts. `vig-op delay 12h "reason"`. `vig-op fail "reason"`.
- Scoped token: one audit, expires in 48h, revoked on delivery. No standing key that can read every customer's repo.
- Everything the operator posts becomes a visible timeline event. Operators never write free text to users.
- Phase 2: the same CLI becomes the agent's toolset inside a Bedrock agent sandbox. Contract unchanged. The agent's instructions carry the §9 prose shape verbatim, and `vig-op report` rejects what does not fit it, so the rule holds whoever writes.

---

## 13. Frontend

### Stack
React 18, Vite, TypeScript strict, Tailwind, shadcn/ui, react-router (data routers), TanStack Query, generated OpenAPI client, Solana wallet adapter, react-helmet-async. Vitest + Testing Library. Playwright for the wizard and checkout.

### Structure
```
frontend/
  src/
    app/            router, providers, layout archetypes
    api/            generated client + thin hooks
    adapters/       solana/ (intake step components, verification panel)
    features/       scan, audit-wizard, audit-detail, report, billing, tweet-claim, settings, admin
    components/     ui (shadcn), primitives (Badge, Meter, Timeline, Pin, TreeSelect, StatCard)
    styles/         tokens.css, globals
```

### Layout archetypes (the "same language, different pages" rule)
1. **Wizard** - stepper, main card, sticky summary rail.
2. **Board** - dashboard grid of stat tiles and lists.
3. **Ledger** - detail page with a side summary and a table (billing, audit detail).
4. **Document** - report: pinning header, findings stream, side index.
5. **Landing** - single input hero, public.

### Design tokens
Dark, spacious, minimal. Exact palette, type pairing, logo and mark are designed fresh for vig before the first screen is built. Monospace for numbers, IDs and hashes. Semantic colors for verified, caution and mismatch are separate from the accent. No mascot.

### Pages
`/` landing + scan input · `/s/:token` scan result · `/login` · `/app` dashboard · `/app/audits` · `/app/audits/new` · `/app/audits/:id` · `/app/audits/:id/report` · `/app/billing` · `/app/settings/*` · `/r/:slug` public report · `/admin/*` · `/terms` `/privacy`.

### SEO and sharing
Public routes (`/`, `/r/:slug`, `/terms`, `/privacy`) are prerendered at build time with full meta and OpenGraph. OG images generated server-side per public report (pinning line + severity counts). Anonymous scan pages are `noindex`. App routes are behind auth and not indexed. Sitemap and robots generated at build.

---

## 14. Backend

- Go 1.23+, chi, huma v2, pgx, sqlc or hand-written queries, goose migrations. Zero paid dependencies.
- Packages: `api` (handlers per group), `auth`, `adapters/solana`, `jobs`, `ledger`, `purchases` (Solana confirmation), `github` (App, installations, tokens), `chain` (RPC client with cache and fallback), `notify` (discord bot, email via SMTP), `admin`, `op`.
- Background workers: scan runner, purchase confirmer, quota expiry, notification dispatcher. Single binary, workers gated by flag.
- Observability: structured logs, request IDs, `/healthz`, `/metrics`.

---

## 15. Infra

- `/secrets` with git-secret: `.env.backend.local|prod`, `.env.frontend.local|prod`. Makefile targets `encrypt-env`, `decrypt-env`, `setup-secrets` copied over.
- `deploy/aws/{terraform,argocd,chart,docker}` copied and renamed. Chart adds a Postgres dependency or points at a managed instance. Treasury pubkey and RPC endpoints are chart values, not secrets.
- `.github/workflows/release.yml` adapted: Go cross-build instead of cargo-zigbuild, Vite dist, single image to GHCR, ArgoCD pins the tag.
- Env: `GOOGLE_*`, `GITHUB_OAUTH_*`, `GITHUB_APP_ID|PRIVATE_KEY|WEBHOOK_SECRET`, `DISCORD_CLIENT_*`, `DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `RPC_PRIMARY_URL`, `RPC_FALLBACK_URL`, `USDC_MINT`, `TREASURY_PUBKEY`, `SESSION_SECRET`, `DATABASE_URL`, `SMTP_*`.

---

## 16. Security controls

| Threat | Control |
|---|---|
| Operator key leaks | Job-scoped tokens, 48h, revoked on delivery; no global operator key |
| Private repo exposure | Installation tokens per job, one hour, never stored; clone deleted after delivery |
| Untrusted code execution | Operator clones and builds in a throwaway container only; CLI refuses to run outside one |
| Exploit handed to non-owners | Ownership proof; exploit_path stripped for non-owners; disclosure policy for third-party live programs |
| Fake purchases | On-chain confirmation: memo nonce, amount, mint, recipient, finalized commitment |
| Tweet fraud | Per-user nonce in the post, manual approval, revoke action |
| Scan abuse | IP + fingerprint limit, expiring results, noindex |
| Admin compromise | Separate role, TOTP, every action logged and user-visible |
| Session theft | HttpOnly SameSite cookies, rotation on privilege change |
| RPC exhaustion | Server-side only, cached, fallback endpoint |

---

## 17. Terms (one page, linked from checkout and every report)

- Findings are point-in-time against a specific commit, slot, and bytecode hash. No guarantee of completeness.
- Audits are run by vig's agents with human review. Operators access the code for the duration of the audit.
- Code is deleted after delivery; reports are retained.
- Payments are one-time, in USDC, final. Pro is a fixed 12-month term with no cancellation.
- Exploit details are provided only to verified owners. Critical findings on live programs not owned by the customer are disclosed to the program owner.
- Free audit credits granted for posts may be revoked if the post is removed.

---

## 18. Build order

Phase 0 - Contract (week 1)
1. Findings schema, job state machine, ledger rules, adapter interface as Go types.
2. huma routes stubbed with real types, OpenAPI generated, Scalar up, TS client generated in CI.
3. Postgres schema + migrations.

Phase 1 - Product (weeks 2–5)
4. Auth: Google, GitHub, SIWS. Sessions, settings identities.
5. Chain client with cache + fallback; Solana adapter Verify; verified-build lookup.
6. Targets, tree, scope meter; anonymous scan runner and result page.
7. Audit wizard end to end, queue, timeline, dedup, quota.
8. Ledger, USDC purchases with on-chain confirmation, billing page.
9. Tweet claims, admin dash (queue, claims, credits, users), TOTP.
10. Operator CLI: claim, verify, event, report, delay, fail. Container guard.
11. Report page, findings statuses, publish + OG images, re-audit diff flow.
12. Discord bot + OAuth link, notification dispatcher, email fallback.
13. Landing, terms, prerender, sitemap.
14. Infra: secrets, chart, argocd, release workflow. First deploy.

Phase 2 - Agent (later)
15. Agent sandbox uses the operator CLI. `human_review` stays. Nothing else changes.

Definition of done for phase 1: a stranger scans a public repo, signs in with a wallet, posts a tweet, gets a credit approved, queues an audit against a real mainnet program with a proven upgrade authority, the operator delivers a report through the CLI, the user gets a Discord DM, publishes the report, and the OG card renders on X.

---

## 20. Surface map

Every audit carries a map of what the target exposes, built by the system the moment the audit is queued and enriched by the operator through the job API. Nobody asks for it.

- **Shape.** Core owns `Surface`: nodes (`program`, `instruction`, `account` on chain; `host`, `route`, `param` on the web), edges (`cpi`, `nav`), evidence (`tx`, `request`, `review`). Adapters build it through `SurfaceBuilder`; core never learns a chain.
- **Solana.** Instructions and accounts from the on-chain IDL (legacy and 0.30 shapes), else from Anchor discriminators in the verified bytecode, else from traffic alone. Evidence is sampled from the public RPC within a budget: 100 signatures listed and 40 transactions read per program, paced, two minutes per audit. CPI targets become external nodes. Replay is `simulateTransaction` with signatures skipped and the blockhash replaced: no key, no validator.
- **Ethereum.** The contract and its functions from the verified ABI (Sourcify), else from the Solidity the checkout declares (the matched contract's own and inherited exposed functions), else from what traffic names through the 4byte directory. Evidence is every transaction sent to the contract in the newest blocks, then the events walked back in windows the endpoint accepts (halving on a refusal, stopping at its archive horizon) to their transactions when the minute was quiet, then, within the transaction budget, the newest transactions Blockscout lists for the address, read from the endpoint by hash; the sample records the oldest block and moment it could see, and an empty sample says how far it looked. The sender is the signer, a calling contract the caller. Overloaded functions are named by signature. What a function reaches is drawn both ways: the Solidity scan follows each exposed function through the contract's own helpers to the outside contracts its body calls (an interface it casts to, a constant bound to an address such as Permit2, the ERC-20 surface, an ETH recipient) and those become `static` edges to external nodes; every receipt read turns the contracts that emitted in that transaction into `traffic` edges from the function that was called, named by their ERC-20 symbol and counted, and the row carries them as `cpi` so the dock lights the route. The contract's box carries the build (compiler, pipeline, EVM target, exact or partial match) and what it holds per token; the watch keeps holdings live through a Transfer-topic subscription on the contract's address, re-reading the balance on each move and recording it as a write on the contract. Every reached function gets one replay at mapping time: `eth_call` against the latest state with the sampled sender, calldata and value (the public endpoint serves no historical state and no tracer), the gas it would take, what it returns, and a revert decoded as `Error(string)`, `Panic(code)` or the ABI's custom error, else named through the 4byte directory. The watch shares one websocket per chain: new heads are read in full for the transactions sent to each contract, a logs subscription per contract delivers the events it emitted (which is how calls through another contract are seen at all), receipts and the transactions behind events are read within the budget, the code hash and proxy slots are re-read every hundred blocks so an upgrade is a row the moment it lands, and coverage is counted in blocks; cursors are per address on the chain the program row names. The anonymous scan reads the adapter from the program id's shape, else from the checkout (Solidity without a Cargo manifest), and the default scope takes `.sol` sources and manifests, never tests, scripts or vendored libraries. The map speaks the chain's words: functions, senders, blocks, and transaction links go to that chain's explorer.
- **Web.** The map stands on the report before any capture: the target's host and a route for every endpoint a finding names are `static` nodes, and a finding whose location is a URL lights the route it folds to. The operator captures a HAR in the sandbox and posts it with `vig-op har`: a route is one host and one path with ids folded to `{id}` (the same fold the report's route facet uses), its methods a fact, parameters hang under routes by where they travel (query, body), every request is evidence with its status and duration, and the page that referred a request is a `nav` edge into it. Hosts stand where programs do, routes where instructions, parameters where accounts; the dock holds requests. Crawling a customer's live surface is authorized testing and needs explicit scope in the wizard before a web adapter ships.
- **Attention** is derived on every read and never stored: `unseen`, `observed` (evidence), `reviewed` (an operator looked, or replayed), `finding` (a report finding names the node). Parents carry the strongest state beneath them.
- **Replay.** Every reached instruction gets one replay at mapping time: `simulateTransaction` with signatures skipped and the blockhash replaced, post-state of the call's accounts, inner instructions. The IDL decodes arguments, account layouts (scalar fields, stopping at the first unsized one), and custom error codes. Stored as `replay` evidence with logs capped at 60 lines. The operator adds replays with `vig-op replay`, and what-ifs with `vig-op whatif`: a scalar argument patched in the wire bytes anywhere, account overrides and time travel only when `VIG_RPC` is a surfnet (`surfnet_setAccount`, `surfnet_timeTravel`).
- **Analysis.** Derived on every read, never stored: per instruction the outcome distribution (ok versus each decoded error name), the compute spread (min, median, max units of its calls), the distinct signers and first sighting, and the token volume per mint; per map the coverage (instructions observed and replayed, CPI edges observed against edges the source declares, signers) and, for a diff audit, how many handlers changed or were added. Recorded at mapping time as node facts: the audited program's upgrade history (loader, immutability, upgrade count, first deploy, last upgrade, current bytecode hash, compared with the intake hash), the trust of every external program the target calls (loader, authority, IDL, verified build), each handler's diff state against the base commit (fingerprint of its reachable code), and whether a seeded account carries a bump constraint. Replays carry token moves, a PDA check of every account the IDL seeds, and the patch, overrides and time travel of a what-if; two replays of one signature render as drift.
- **Rendering.** The Surface tab has two fixed regions. *The map is the place.* Structure nests, so one breadcrumb walks it and nothing else navigates: the system, one program, one instruction. At system level callers enter on the left (signers, programs that call in), each audited program is a box whose header carries its identity and life (build match, IDL, authority, upgrades, loader, deploy slot and dates, whether its init instruction is in the sample) and whose rows are its instructions, grouped by source module and foldable, each with a traffic bar, a four-part depth mark (declared, observed, replayed, reviewed), a raw-accounts marker, finding tags and a changed or added mark; what is reached sits on the right, the dynamic call target drawn as a gap. Wires are calls: width is traffic, dashed exists only in source, amber is signed. At program level that program opens fully and the others fold to their headers, still wired. At instruction level the instruction is laid out in execution order (enters, loads, validates, runs, calls, commits): callers (the ten busiest, the rest folded into one node that expands), the handler with its source counts and traffic stats, callees, and underneath the accounts it loads with how each is validated (raw reads unchecked). *The dock is what happened there.* It sits in one place and holds the sampled transactions and the accounts of wherever the map stands. Picking a wire or an account filters the dock instead of opening anything. Picking a transaction opens it in place (then, what the chain recorded, and now, the replay against current state, as separate marks; how far each got through the six stages; the numbered route; arguments, token moves, and the replay with every account's change) and lights and numbers that route on the map, where it stays lit across levels. Same component for the customer and the admin; the public report never.
- **Watch.** After mapping, the surface stays live. The adapter's `Watcher` keeps one websocket session per audit: a logs subscription per audited program delivers every confirmed transaction that mentions it, and the call tree in its logs (invoke depth, the Anchor instruction name, units, failure and the error code) is enough to place a row on the instruction it hit, with its callers and callees, without another read; an account subscription per watched address delivers every write with the new data, decoded with the IDL. Watched addresses are chosen from the sample once and stored: the program-data account (an upgrade or an authority change becomes a `write` row on the program node and rewrites its facts the moment it lands), the IDL account, and every address the sample passed repeatedly for a writable, unsigned declared account, which is what shared state looks like from outside. A bounded number of transactions per minute are also read in full (failures, calls on nodes a finding names, the first few calls of every instruction, calls with no log name), and a full read replaces the log-derived row rather than adding one. Live rows are the same evidence rows the sampler writes, flagged `live`, kept seven days; per-minute rollups per node keep the count forever and feed attention. Every session starts by walking the signature cursor back to where the last one stopped (or where the sample ended, on the first), reading what the budget allows and recording the rest as a gap; coverage is a fact per slot range per subject: `live`, `backfill` or `gap`, and no count is shown without it. A watch runs for the plan's life on Pro and Team and for thirty days after delivery on Free, live for everyone. The feed is `GET /v1/audits/{id}/surface/live` with a row cursor (`after`), or with `before` for the newest rows at a moment in the past; the map view folds the rollups into observed counts and says where the watch stands. *Time on the tab.* A timeline above the map is the time of the surface, as a session replay: the picture is a function of the playhead. The whole watch, or a window of an hour, ten minutes or a minute, on one linear axis: calls per bucket as peaks with failures stacked in red, one coverage lane per audited program under them (live, backfilled, missed), the sample window marked, every upgrade and state write a tick, the transactions in view as marks, round time labels beneath. Every call has the moment it happened; at the playhead's moment a dot is exactly as far along its path as the time since its call says, so dragging forward advances every dot, dragging back runs them in reverse, and live is the playhead advancing at one second per second a few seconds behind the chain. A seek pauses; play moves the playhead at 1×, 10× or 60× (a call's crossing scales with the speed), reading rows ahead in ranges, until it reaches now and is live again; space toggles, the arrows step through the transactions in view with each lit on the map, `now` returns, and the page opens on a short replay that catches up with now. A dot enters from whoever signed or called, crosses the instruction it hit and goes on to what it called, red and stopping short when it failed; at instruction level it also drops to each writable account; a call the map can place only on the program lands on its box; at most forty-eight dots are in flight. The dock lists what had happened up to the playhead; a transaction opened there shows its route, its arguments as the call reads in source (`name(arg: value, …)`, values typed by shape), every account with the address that stood in for it and whether it was written or signed, and its token moves, for the transactions the watch read in full, which a `with inputs` filter narrows the log to; a row placed from logs alone says so. Rows show calls since the watch began beside the sample count. Motion is off under reduced motion.
- **Drift.** A write on a program node that changes what was audited (an upgrade, a new authority or implementation, a new IDL) is a row in the dock above the transactions, a `surface_drift` event on the timeline, and an in-app notification to the owner; Discord when wired. The mapper also re-reads every program against the checkout once it is here and writes the panel back, so an intake's pending rows resolve without a second visit.
- **Limits.** One mapper goroutine, three attempts per audit, 5 000 nodes and 20 000 evidence rows per audit, the newest 300 returned. One watch session per watchable audit, 64 at once, 32 subscribed addresses per audit, 30 full reads a minute, 1 000 signatures listed and 40 read after a disconnect. Nothing paid, nothing browser-side.

## 19. Remaining decisions

- Mini and Pro scope caps (defaults in §2).
- Report visibility default (default: private).
- Domain.
- Visual identity brief: logo, palette, type. Designed before the frontend starts.
- Treasury wallet and which free-tier RPC to register.
- Whether Team gets a listed price before Enterprise.
