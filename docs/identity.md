# Identity

Concept: vigilance. The mark is a monogram: two heavy strokes meeting at the base, and the accent held as a point in the notch. It reads as a V and as a watch mark at once. No wordmark. The product name never appears as text in the UI.

## Mark
`frontend/public/mark.svg` and `frontend/src/components/primitives/Mark.tsx`. Two strokes at 4.2/32 with round caps from (6,7) and (26,7) to (16,27), and a 2.4/32 point at (16,12) in the accent. Reads at 16px (favicon), 24px (rail), 56px (landing). When something is in progress the point pulses; the strokes never move.

## Palette
Dark first, in the zinc-and-mint register the user asked for. Light is the same roles on a paper ground. All values in `frontend/src/styles/tokens.css`.

| Role | Dark | Light | Use |
|---|---|---|---|
| background | #0f0f12 | #fafafa | page ground |
| card | #18181b | #ffffff | surfaces |
| secondary / accent surface | #27272a | #f4f4f5 | hover, chips |
| border | #27272a (input #35353b) | #e4e4e7 | hairlines |
| foreground | #fafafa | #0f0f12 | text |
| muted-foreground | #a1a1aa | #52525b | secondary text |
| faint | #52525b | #a1a1aa | tertiary text, disabled marks |
| primary (accent) | #86efac mint, text on it #052e16 | #16a34a | the one action colour, selection, the mark's centre |
| ok | #82e28d | #16a34a | verified, delivered, match |
| warn | #facc15 | #ca8a04 | delayed, near cap, disputed |
| bad | #f87171 | #dc2626 | mismatch, failed, critical |
| pending | #71717a | #71717a | not yet known |
| wallet | #c084fc | #7e22ce | wallet actions only |

Mint is both the accent and, in its sprout shade, the ok state. They are kept distinguishable by role: the accent sits on buttons and selections, ok only ever appears inside a StateBadge.

## Type
- UI: Instrument Sans, 14.5px base, weights 400 to 600. Headings track -0.015em.
- Data: Geist Mono for every number, id, hash, path and label. Tabular numerals always.
- Display: Instrument Serif, italic, on the landing headline only.
- Labels: Geist Mono 11px, uppercase, 0.1em tracking, muted. Utility class `label-caps`.

## Scale
Spacing on a 4px grid. One radius: 8px, or 0. Hairline borders at 1px. No shadows.

## Components
- StateBadge: a fact. Colour is the state, text is the value.
- Meter: a quantity against a cap. Amber, then warn at 85%, bad over.
- Stat: a number that matters with its label under it.
- Panel: verification rows. Label left, badge right, action button when there is one.
- Pin: repo, commit, programs with slot and match badges. Lives on every report.
- Timeline: the pipeline stages; the mark rotates on the current one.
- Stepper: numbered only because order carries meaning.
- OptionCards: pick one; selected is amber; disabled says soon.
- Tree: file tree with tri-state folders, lines per file.
- Empty: the mark, one line, one action.
- Bytecode: the profile of a deployed program as facts. Size and instruction count as Stats, capabilities and framework as neutral chips, crates as `name@version` in mono, source paths as a mono list, recovered Anchor names as chips. Neutral tones only: a capability is not a verdict.
- Methods strip: one row of chips on a report saying how it was produced: static, bytecode, fuzzing (with runs and crashes in mono), on-chain. Chips, never sentences.
- Evidence badge: on a finding, the kind of evidence behind it (static, fuzz, on-chain tx, bytecode) with the artifact in mono (a crash input, a signature, an offset). Pending tone.

## Layout archetypes
Wizard (stepper, main card, sticky summary rail) · Board (stat tiles, list) · Ledger (detail with side summary and a table) · Document (pin header, findings stream, side index) · Landing (a blueprint: a 40px grid, the mark drawn large, one headline, the scan input, four fact tiles; the grid bug is the only thing that moves).

## Motion
The mark's point pulses only while something runs. Meters animate width. On the landing, the grid bug walks. On a live surface, calls travel the wires. Nothing else moves.

## OG image
Pin line plus the five severity counts on the dark ground, the mark in the corner. Generated server-side per public report.

## Severity ladder

Five hues, one per severity, separate from the state colours (ok, warn, bad) so a finding's weight never borrows a state's meaning: `--sev-critical` the deepest red, `--sev-high` a lighter red, `--sev-medium` amber, `--sev-low` blue, `--sev-info` grey, each with a `-soft` ground. Light and dark values live in `frontend/src/styles/tokens.css`; the OG image uses the dark values. Badges, summary counts, index dots and the known strip all read from the ladder through `severityState`, `severityInk` and `severityDot`.
