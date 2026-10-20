# Visual identity brief

Design this before the first screen. Output: docs/identity.md with the chosen palette, type pairing, logo/mark, spacing scale, and component rules, plus frontend/src/styles/tokens.css.

## What the identity must feel like
- Dark, spacious, quiet. Numbers, hashes and IDs are first-class citizens and set in monospace.
- Professional enough for a team paying $540; native enough that a Solana developer feels at home.
- Confidence comes from verification badges and pinned facts, not from decoration.

## Hard constraints
- Original. No mascot. No duck. No borrowed marks or motifs from any audit product, launchpad, or exchange. If a choice would look familiar to someone who uses those products, choose again.
- One accent color for actions. Semantic colors for verified / caution / mismatch are separate and never used as accent.
- Wallet actions may carry the wallet ecosystem's convention if needed, but nothing else does.
- Type: a body face, a monospace face, and optionally a display face for the landing page only. All from Google Fonts or self-hosted. Declare fallback stacks.
- Both themes: dark is primary, light must be designed, not inverted.
- Five layout archetypes (SPEC.md §13). The identity is expressed inside them.
- Motion: purposeful and rare. State transitions on the timeline and the verification panel are the only places that earn animation.

## Deliverables
1. A symbol only. No wordmark and no product name as text anywhere in the UI. The mark must carry the identity alone at 16px (favicon), 24px (sidenav), and 512px (OG image corner). Concept: vigilance.
2. Palette as tokens: ground, surface, surface-2, line, line-strong, text, muted, faint, accent, accent-soft, ok, warn, bad, wallet.
3. Type scale: 6 sizes, 2 weights per face, letter-spacing rules for uppercase labels.
4. Spacing scale and radius scale. Pick a radius and use only it and zero.
5. Component rules for: Badge, Meter, Timeline, Pin (the pinning line), TreeSelect, StatCard, wizard stepper, order summary rail.
6. OG image template: pinning line plus severity counts.

## Anti-patterns to avoid
- Near-black with a lone acid-green pop, purple-to-blue gradients, everything centered, cards with an accent rail, emoji as icons, generic geometric sans as the only face.
