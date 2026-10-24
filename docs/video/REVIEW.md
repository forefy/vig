# Review

The film: 45 s, 1920 x 1080, 30 fps, silent by design. Storyline A of `STORYLINES.md` ("The door"), built as `SPEC.md`.

## Creative calls

- Every product frame is a 2x capture of the real app on the seeded demo (`scripts/capture.cjs`): the surface map of `vig-demo/token-program`, the owner's report of `vig-demo/vault-programs` before and after a real re-rating, each keystroke of the reason and of the query, the changes log open, the admin lessons page. Nothing is drawn to look like the product.
- The query folds on `exploitable by = anyone`, four to two. The seed was changed for that: findings 03 and 04 now say `any_signer`, which is what an arbitrary CPI and a same-transaction revival need (a funded account that signs). Before that change all four live findings said `anyone` and the facet folded nothing.
- The chain is clicked through for real: the cursor opens `expected`, step 1, step 2 and `impact` on finding 01, and each state is the captured card.
- The hook's three documents are the real exported findings document, the real request headers of the operator API, and the real fuzzing block of the report's `methods`. The door prints the real advice the dry-run check returns for that document.
- Titles are short plain sentences with a verb, rewritten after the first cut read as machine copy. The product's name appears only on the end card.
- Silent: the film is made for a README, which plays muted. A music bed can be added as one `<audio>` in `index.html` (`sound.md` §3).
- Not built: B "Two reports" (before and after, split screen) because its old-way half would be invented; C "The surface lights up" (one endless zoom) because the sink has no place in it.

## Measured, looked at, unchecked

- `npx hyperframes check --json`: no errors. Kept warnings: `film_silent` (by design); `text_below_floor` (the three source panes while they shrink into the door, and the pane headers at 24 px); `line_unread` on "chain" and "required" (they leave with the door card, 0.13 s).
- Looked at: the beat sheet, the five seam strips, the phone sheet, and 22 single frames across every beat.
- Unchecked: the film was watched only as stills. Nobody has heard it, because there is nothing to hear.

## Weakest shot

The surface beat (13 to 19 s): the map is shown as a push and a hold; it does not act. With more time: capture the map with the live timeline playing (a short real screen recording of the pulses) instead of one still.

## Notes kept

- t=28.5 s: the camera's move onto the re-rate dialog is sampled mid-blur on the sheet; it lands sharp at 29.1 s.
- t=19 to 21 s: the report header at 1.6x shows 13 px labels at 21 px, under the 26 px floor of `types/launch.md`; the header is a one-second establishing shot and every line meant to be read is pushed to 2.0x after it.

## Recapture

```bash
# from the repository root, with the backend on :7112 pointed at vig_demo and the frontend on :7111
NODE_PATH=frontend/node_modules REPORT_AUDIT=<delivered vault-programs audit id> SURFACE_AUDIT=<token-program audit id> node docs/video/scripts/capture.cjs
```

The demo must be freshly seeded (the script performs one real re-rating), and the token-program surface must have finished building (about a minute after the backend starts).
