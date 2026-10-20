import { useCallback, useLayoutEffect, useRef, useState } from 'react'

export type Pt = { x: number; y: number }
export type Ports = Record<string, Pt>

// Where every [data-port] element sits inside one wrapper, so an SVG laid
// over ordinary DOM can draw wires between them. Re-measured when the
// layout key changes, when the wrapper resizes and once fonts have loaded.
export function usePorts(layoutKey: string) {
  const ref = useRef<HTMLDivElement>(null)
  const [ports, setPorts] = useState<Ports>({})
  const [size, setSize] = useState({ w: 0, h: 0 })

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    const base = el.getBoundingClientRect()
    // Client rects are in visual pixels: a page zoom (the reader's text
    // size) or a transform scales them, while the svg draws in layout
    // pixels. The wrapper's own two widths give the factor to divide out.
    const w = el.offsetWidth, h = el.offsetHeight
    const k = w > 0 && base.width > 0 ? base.width / w : 1
    const next: Ports = {}
    el.querySelectorAll<HTMLElement>('[data-port]').forEach((p) => {
      const r = p.getBoundingClientRect()
      next[p.dataset.port ?? ''] = { x: Math.round(((r.left - base.left + r.width / 2) / k) * 2) / 2, y: Math.round(((r.top - base.top + r.height / 2) / k) * 2) / 2 }
    })
    setPorts((prev) => (same(prev, next) ? prev : next))
    setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }))
  }, [])

  useLayoutEffect(() => {
    measure()
  }, [measure, layoutKey])

  useLayoutEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => measure())
    ro.observe(el)
    // The reader's text size is a zoom on the root element.
    const mo = new MutationObserver(() => measure())
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['style', 'class'] })
    void document.fonts?.ready.then(measure)
    return () => {
      ro.disconnect()
      mo.disconnect()
    }
  }, [measure])

  return { ref, ports, size }
}

function same(a: Ports, b: Ports): boolean {
  const ka = Object.keys(a), kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  for (const k of ka) if (!b[k] || a[k].x !== b[k].x || a[k].y !== b[k].y) return false
  return true
}

// A cubic between two ports. side says which way each end leaves: wires
// between a left and a right column bow horizontally; a loop leaves and
// returns on the right.
export function wire(a: Pt, b: Pt, kind: 'across' | 'loop' | 'down'): { d: string; mid: Pt } {
  let c1: Pt, c2: Pt
  if (kind === 'loop') {
    const x = Math.max(a.x, b.x) + 56 + Math.min(60, Math.abs(a.y - b.y) / 6)
    c1 = { x, y: a.y }
    c2 = { x, y: b.y }
  } else if (kind === 'down') {
    const dy = (b.y - a.y) / 2
    c1 = { x: a.x, y: a.y + dy }
    c2 = { x: b.x, y: b.y - dy }
  } else {
    const dx = (b.x - a.x) / 2
    c1 = { x: a.x + dx, y: a.y }
    c2 = { x: b.x - dx, y: b.y }
  }
  return {
    d: `M${a.x} ${a.y} C ${c1.x} ${c1.y}, ${c2.x} ${c2.y}, ${b.x} ${b.y}`,
    mid: { x: (a.x + 3 * c1.x + 3 * c2.x + b.x) / 8, y: (a.y + 3 * c1.y + 3 * c2.y + b.y) / 8 },
  }
}

export const strokeOf = (n: number): number => (n > 0 ? Math.min(5, 1 + Math.log2(n + 1) * 0.7) : 1)
