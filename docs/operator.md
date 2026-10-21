# Running an audit as the operator

Phase 1 is concierge: a person runs the tooling, the product does not know the difference. Everything below happens inside the sandbox image (`deploy/operator/Dockerfile`), never on a workstation. `vig-op` refuses to start outside a container.

## One job, start to finish

1. In the admin console, Operator tokens, mint an unbound token. It can claim exactly one job and expires in 48 hours.
2. Start a sandbox and claim:

   ```bash
   docker run --rm -it -e VIG_API=https://<api> -e VIG_OP_TOKEN=vop_... vig-operator
   vig-op claim
   ```

   The claim binds the token to the job, writes `/work/job.json`, and checks the source out at the pinned commit under `/work/src`. Private repos arrive with a one-hour installation token that is used for the clone and then dropped from the remote.

3. Announce the stages as you reach them. Each becomes a timeline row the customer sees:

   ```bash
   vig-op event cloning
   solana-verify build                # or anchor build, in /work/src
   vig-op verify                      # hashes target/deploy/*.so, compares with the deployed bytecode, records the match
   vig-op event programs_verified
   vig-op event analysis
   ```

   `verify` accepts `--so <program id>=<path>` when the artifact is elsewhere and `--match <program id>=verified|self_built|mismatch|unverified` when you established the result another way.

4. Write findings. `vig-op template > findings.json` prints a document pre-filled with the pin; fill in the findings. The schema is `core.FindingsDocument` (SPEC §9); `exploit_path` is only ever shown to verified owners.

   Two fields record how you worked, and the report page shows them as chips, never as prose:
   - `report.methods`: `static`, `bytecode` (you profiled or disassembled the deployed image), `onchain` (you inspected live accounts or transactions), and `fuzzing` with the campaign's `tool`, `harnesses`, `runs`, `crashes`, `duration_s` and optional `coverage`. Drop `fuzzing` if you did not fuzz.
   - `findings[].exploitable_by`: who can reach it, least to most privileged. `anyone` (no account, no session), `any_user` (any signed-in account) or `any_signer` (any funded key), `account_owner` (a member of the affected account or position), `api_key` (an integration's credentials), `internal_network` (inside the network or on a VPN), `admin`, `upgrade_authority` (the platform operator or the upgrade key), `other_program` (only through another program or service), `none`. The page shows it in the target's own words: a site reads "any signed-in user" where a chain reads "any signer".
   - `findings[].evidence`: `{kind: static | fuzz | onchain | bytecode, artifact}` where the artifact is something a reader can check: a crash input hash, a transaction signature, a bytecode offset, a harness name.

   Code is attached for you: `vig-op report` captures the whole enclosing item (function, struct, impl) at every location from the checkout, and the report folds everything except the located lines. Two more fields shape how a finding reads:
   - `findings[].sites` with `sites_kind: flow`: ordered steps, each `{location, note}`; the note is the tab caption ("input enters", "reaches the CPI", "no check"). The report shows them as numbered tabs left to right.
   - `findings[].sites` with `sites_kind: occurrences`: the same mistake in several places, one tab per site.
   Put the primary location in `location` as usual; sites repeat it as the first entry when it belongs to the sequence.

   The report shows a finding's code as a chain the reader steps through: `normal_use` first (how the code is ordinarily called, by whom, with what), `expected` next (what it is supposed to guarantee there), then the code steps, then `impact` last (who loses what). Write all three in the same shape as `description`; a finding without them just has a shorter chain.

   `fix` is bullets only, one per change. When there is a definite fix, put it in a ``` fence right after its bullet and the report nests it under that bullet: the correct code, the exact header value, the configuration line. Name where the code goes on the fence line, `rust programs/x/src/lib.rs:41-45`, and the report shows it as a patch: those lines as removed, yours as added, linked to the repository at the pinned commit (`path:line` alone inserts before that line). A bullet with no definite code stands alone. A finding that names a file reads best with a placed fence, and the dry run advises one when it is missing; say `"fix_kind": "operational"` when there are no lines to change (rotate, revoke, configure) and the advice stops. The other prose fields take a fence too, for an exact value such as a returned header.

   Attachments ride on one step (`findings[].sites[].visuals`, one per step, shown under its code) or on the finding (`findings[].visuals`, shown on the impact item): a screenshot (png, jpeg, webp, gif) shows as a picture, a recording (mp4, webm) as a player, anything else (a PoC script, a transaction dump, a log, an archive) as a download under its file name. `vig-op attach 2.3 drain.mp4 "drain runs in one tx"` uploads for the third step of finding 2 and writes the reference into findings.json; `vig-op attach 2 poc.py "the script"` puts it on the finding, with the impact. Images up to 8MB, recordings 64MB, files 16MB. Upload before `vig-op report`: the server refuses a document that references an attachment it does not hold, a step that carries more than one, and a finding with more than one recording.

   The claim response carries each program's bytecode profile (`target.programs[].panel.profile`): size, syscalls and capabilities, framework, crates, source paths and the instruction names recovered from discriminators. For a program with no verified source this is the starting map for reverse engineering.

   ```bash
   vig-op attach 1.2 drain.mp4 "the drain, recorded"   # optional: one capture, recording or file per step; `attach 1 …` goes on the impact
   vig-op report findings.json        # validates locally, checks every file path exists in the checkout, submits
   vig-op event human_review
   vig-op event delivered             # revokes the token, deletes the checkout, notifies the customer
   ```

5. Leave the container. It is destroyed with everything in it.

## The surface map

The map is built before you claim: instructions and accounts from the IDL or the bytecode, first evidence sampled from the chain. It is yours to grow.

```
vig-op surface                            # every node with attention, observations and findings
vig-op review --note "checked math" ix:<program>:withdraw
vig-op replay --tag "#01" <signature>     # re-run a transaction: outcome, units, accounts before and after, decoded through the IDL
vig-op whatif --arg msol_amount=1 <sig>   # same, with one scalar argument rewritten in the wire bytes
vig-op whatif --set-account <pubkey>='{"lamports":0}' --time 36h <sig>   # overrides and time travel; needs a surfnet on VIG_RPC
vig-op har --host app.example.com capture.har   # web targets: routes (ids folded to {id}), parameters, requests and referer nav edges from a proxy or browser capture
```

A web job's `job.json` carries `target.site`: the origin, the hosts and path prefixes the customer authorised (`scope.files` on the audit is the same scope as URL prefixes) and whether ownership was proven. Nothing outside those prefixes is probed, whatever the capture shows.

Replay uses `simulateTransaction` with signatures skipped and the blockhash replaced, on `VIG_RPC` (the public mainnet endpoint by default). Point `VIG_RPC` at a surfpool surfnet to replay against curated state; `whatif` uses its `surfnet_setAccount` and `surfnet_timeTravel` cheatcodes and refuses overrides anywhere else.

## Lessons

`vig-op claim` prints `lesson` lines before anything else: findings on earlier reports of this repository that the owner or an admin archived as not relevant, or rated up or down, each with the reason in their words. Read them before the code. Do not raise what was archived unless the code changed; rate like the corrections rated. The same list is `job.lessons` in job.json and `GET /v1/admin/lessons` for every repository.

## Posting as an agent

An agent that writes the document itself, without the CLI, has three things to lean on:

- `GET /v1/findings/shape` (public): which finding fields are required (`ordinal`, `severity`, `title`, `location`, `description`, `fix`), which are recommended (`normal_use`, `expected`, `impact`, `exploitable_by`, `evidence`, a flow of `sites` for critical and high, a fenced patch in `fix`, attachments), which are optional, the rules validation applies, and a filled example to start from.
- Every field in the OpenAPI document at `/docs` opens with Required, Recommended or Optional.
- `POST /v1/op/jobs/{id}/report/check` with the document: `{valid, errors, advice}`. Errors are what the real post would refuse; advice is what the report reads better without, finding by finding. Iterate until `valid` and the advice is what you meant to leave out, then post the same body to `/report`. `vig-op report --check findings.json` does this from the sandbox.

The chain a finding renders as, in order: `normal_use` (how the code is ordinarily used), `expected` (what it must guarantee), the code steps (`location`, then `sites` for a flow, one attachment each), `impact` (who loses what, with the finding's own attachments). Upload attachments first; reference them by id.

## When something goes wrong

- Behind schedule: `vig-op delay 12h "queue over capacity"`. The ETA moves and the customer is notified. The job returns to the stage it left when you post the next event.
- Cannot deliver: `vig-op fail "repository does not build at the pinned commit"`. The credit or daily slot is restored and the reason is shown on the timeline.

## What the operator cannot do

- Read another customer's job: the token is bound to one audit at claim time.
- Write free text to the customer: only states, program matches and the findings document are accepted.
- Keep a credential: the token dies on delivery, the installation token dies after an hour, the clone dies with the container.
