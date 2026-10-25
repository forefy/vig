# Storylines

Subject: vig, a reporting dashboard and determinism layer for security audit reports.
One thing: every finding, from any source, lands in one shape, pinned to a commit, and nothing is ever lost.
In the viewer's words: "vig is where all our findings land, and it keeps them honest."
Spine: one real finding, `01 Invoice lookup skips the organisation check` in the seeded web demo (Ledgerly, an invented invoicing app), from the moment it is posted to the moment the owner's corrections show beside it.
Length 45 s, 1920 x 1080, 30 fps, silent by design (a README autoplays muted); facts from README.md and the seeded demo, never from a drawing.

## A. The door (recommended: a stranger follows one document through a real product)

Device: one object across apps. Form: object-to-object chain.
A findings document is posted from an operator's terminal, an agent's JSON and a fuzzer's crash, passes the shape check at the door, and lands as a row in the real report. The camera never leaves the thing it followed: the row becomes the finding, the finding becomes its patch, the patch pulls back into a report that remembers every correction.

First two seconds: three source panes fill the frame, each printing its own kind of text (the operator's findings.json, an agent's POST, the tools' evidence lines (burp, sqlmap, ffuf)). At 1.2 s the title lands at left: "Findings arrive from everywhere".

Beats: hook (sources print) → tension (three shapes, severity words that do not agree) → reveal (the shape: `normal_use → expected → steps → impact`, required fields, the check's own advice; "One shape. Pinned. Evidence bound.") → proof (the site map lit by a week of traffic → zoom through the invoice route → the report: ladder, the IDOR finding clicked through as a chain, the fix as a patch, the role finding re-rated with its reason, `exploitable by = anyone` folds four to two) → payoff (pull back: the changes log unfolds on top, the archived strip, the lessons) → claim ("Every finding. One shape. Nothing lost.") → end card (the mark draws itself, `github.com/forefy/vig`).

Look: the product's own dark set (#0f0f12 ground with its #17171b grid, #18181b cards, #27272a borders), Instrument Sans for titles, Geist Mono for everything the product prints, mint #86efac on one word per shot. Real screens at 1.6 to 2x, never a window floating in the middle.
Sound: none, by design: the film plays muted in a README; type and the product's own text carry it. A music bed can be added later.

## B. Two reports

Device: life before and after. Form: split screen to one.
Left half: the old way, a PDF report page and a spreadsheet of findings churning (severity written four ways, no commit, a fix as a paragraph). Right half: the same findings in vig, calm. The split collapses into the right half, which then gets the tour.

First two seconds: the frame splits down the middle; left, a page of prose scrolls past too fast to read; right, the severity ladder of the real report counts up 1 2 2 1 1.

Beats: hook (split) → tension (left churns: four severity words, a fix as a paragraph) → reveal (the split collapses to vig, "One shape") → proof (query line, chain, patch, re-rate) → payoff (changes log) → claim → end card.
Look and sound: as A.
Why not first: the left half has to be invented (no named competitor, no real artefact to show), and an invented artefact is the one thing the launch rules forbid.

## C. The surface lights up (the bold one)

Device: the object is the explanation. Form: an endless zoom.
The film is one dive. It opens on the surface map of a program, lit by traffic. The camera dives into one instruction node, which turns out to be a finding's location; into the line, which turns out to be the patch; then pulls all the way out through the finding, the report, the changes log, to the lessons for the next audit.

First two seconds: the surface map fills the frame, nodes already lit, the camera already moving toward one node.

Beats: hook (the map) → tension (the lit node has no finding yet) → reveal (the dive lands on finding 01, "One shape") → proof (patch, chain read upward, re-rate) → payoff (the pull-out to the whole report and the lessons) → claim → end card.
Look and sound: as A.
Why not first: the sink (findings arriving from several sources) has no place in a single dive, and the brief asked for it first.
