// The blueprint's geometry. The grid is drawn across the whole page, so the
// point walks the whole page: every node of it that is not standing on the
// page's own ink.
export const CELL = 40
// The card carries five severity columns; below seven cells they stop being
// readable, and eight is as wide as it ever needs to be.
const MIN_W = 7 * CELL
const MAX_W = 8 * CELL
// Taller than the card ever renders (~286px), so the fit test errs towards
// moving the card rather than towards covering the point.
const CARD_H = 300
// The point's radius: how close its centre may come to a letter.
const REACH = 12

export type Box = { left: number; top: number; right: number; bottom: number }

export const key = (x: number, y: number) => `${x},${y}`

// Every node the point may stand on: inside the frame, clear of every solid
// thing on the page. Nodes, not cells - the point sits on the intersections.
export function freeNodes(frameW: number, frameH: number, solid: Box[]) {
  const cols = Math.floor(frameW / CELL)
  const rows = Math.floor(frameH / CELL)
  const free = new Set<string>()
  for (let y = 1; y <= rows - 1; y++) {
    for (let x = 1; x <= cols - 1; x++) {
      const cx = x * CELL
      const cy = y * CELL
      const covered = solid.some((r) => cx + REACH > r.left && cx - REACH < r.right && cy + REACH > r.top && cy - REACH < r.bottom)
      if (!covered) free.add(key(x, y))
    }
  }
  return free
}

// Where the point starts: the leftmost free node, nearest the middle of the
// page. Deterministic, so the first paint is the same every time.
export function startNode(free: ReadonlySet<string>, frameH: number) {
  const mid = frameH / 2 / CELL
  let best: { x: number; y: number } | null = null
  for (const k of free) {
    const [x, y] = k.split(',').map(Number) as [number, number]
    if (!best || x < best.x || (x === best.x && Math.abs(y - mid) < Math.abs(best.y - mid))) best = { x, y }
  }
  return best ?? { x: 1, y: 1 }
}

export function neighbours(x: number, y: number, free: ReadonlySet<string>) {
  return [
    { x: x + 1, y },
    { x: x - 1, y },
    { x, y: y + 1 },
    { x, y: y - 1 },
  ].filter((n) => free.has(key(n.x, n.y)))
}

// As wide as eight cells, or as much of a narrow page as there is.
export function cardWidth(frameW: number) {
  return Math.max(MIN_W, Math.min(MAX_W, Math.floor((frameW - 2 * CELL) / CELL) * CELL))
}

// Centred under the point, kept inside the frame, snapped to the grid.
export function cardLeft(x: number, frameW: number, cardW: number) {
  const want = Math.round((x * CELL - cardW / 2) / CELL) * CELL
  return Math.max(CELL, Math.min(want, frameW - CELL - cardW))
}

// The card drops from the point and rises from it when nothing fits below. It
// never takes the point's own row.
export function placeCard(y: number, frameH: number, cardH = CARD_H) {
  const drop = (y + 1) * CELL
  const rise = (y - 1) * CELL
  if (drop + cardH <= frameH) return { top: drop }
  if (rise - cardH >= 0) return { bottom: frameH - rise }
  return { top: Math.max(CELL, Math.min(drop, frameH - cardH - CELL)) }
}
