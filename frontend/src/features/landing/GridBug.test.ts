import { describe, expect, it } from 'vitest'
import { CELL, cardLeft, cardWidth, freeNodes, key, neighbours, placeCard, startNode } from './grid'

// The landing at 1600x900: the frame the grid is drawn on, and the ink the
// page puts on it (mark, headline, subhead, input, link, tiles).
const W = 1600
const H = 766
const SOLID = [
  { left: 744, top: 80, right: 856, bottom: 192 },
  { left: 389, top: 228, right: 1211, bottom: 296 },
  { left: 555, top: 320, right: 1045, bottom: 344 },
  { left: 500, top: 380, right: 1100, bottom: 428 },
  { left: 740, top: 464, right: 860, bottom: 484 },
  { left: 250, top: 560, right: 1350, bottom: 668 },
]

describe('freeNodes', () => {
  const free = freeNodes(W, H, SOLID)

  it('opens the whole page, not one band', () => {
    const xs = [...free].map((k) => Number(k.split(',')[0]))
    const ys = [...free].map((k) => Number(k.split(',')[1]))
    // Nodes on both sides of the column, and above and below the content.
    expect(Math.min(...xs)).toBe(1)
    expect(Math.max(...xs)).toBe(Math.floor(W / CELL) - 1)
    expect(Math.min(...ys)).toBe(1)
    expect(Math.max(...ys)).toBe(Math.floor(H / CELL) - 1)
  })

  it('never stands on the page ink', () => {
    for (const k of free) {
      const [x, y] = k.split(',').map(Number) as [number, number]
      const cx = x * CELL
      const cy = y * CELL
      for (const r of SOLID) {
        expect(cx + 12 > r.left && cx - 12 < r.right && cy + 12 > r.top && cy - 12 < r.bottom).toBe(false)
      }
    }
  })

  it('reaches the far side of the page from where it starts', () => {
    // Flood fill: the point can actually walk to the other margin, so the
    // grid it roams is one connected page and not a pocket.
    const from = startNode(free, H)
    const seen = new Set([key(from.x, from.y)])
    const queue = [from]
    while (queue.length) {
      const at = queue.shift()!
      for (const n of neighbours(at.x, at.y, free)) {
        if (seen.has(key(n.x, n.y))) continue
        seen.add(key(n.x, n.y))
        queue.push(n)
      }
    }
    const reached = [...seen].map((k) => Number(k.split(',')[0]))
    expect(Math.max(...reached)).toBe(Math.floor(W / CELL) - 1)
    expect(seen.size).toBeGreaterThan(free.size * 0.9)
  })
})

describe('the card', () => {
  const rows = Math.floor(H / CELL)
  const walked = Array.from({ length: rows - 1 }, (_, i) => i + 1)
  const CARD = 286

  function box(place: { top?: number; bottom?: number }) {
    const top = place.top ?? H - (place.bottom ?? 0) - CARD
    return [top, top + CARD] as const
  }

  it.each(walked)('leaves the point on row %i uncovered', (y) => {
    const [top, bottom] = box(placeCard(y, H))
    expect(bottom <= y * CELL - 12 || top >= y * CELL + 12).toBe(true)
  })

  it.each(walked)('stays inside the frame on row %i', (y) => {
    const [top, bottom] = box(placeCard(y, H))
    expect(top).toBeGreaterThanOrEqual(0)
    expect(bottom).toBeLessThanOrEqual(H)
  })

  it('drops from the point, and rises when nothing fits below', () => {
    expect(placeCard(3, H)).toEqual({ top: 4 * CELL })
    expect(placeCard(17, H)).toEqual({ bottom: H - 16 * CELL })
  })

  it('fits a narrow page and stays inside it wherever the point is', () => {
    expect(cardWidth(375)).toBe(7 * CELL)
    expect(cardWidth(1600)).toBe(8 * CELL)
    const w = cardWidth(375)
    for (const x of [1, 4, 8]) {
      const left = cardLeft(x, 375, w)
      expect(left).toBeGreaterThanOrEqual(CELL)
      expect(left + w).toBeLessThanOrEqual(375 - CELL)
    }
  })
})
