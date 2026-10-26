import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

// The first time a page offers a step, electricity crackles off its
// control for a few seconds: jagged bolts in the spark color, drawn,
// flickering, gone. Once per step per browser: the step is remembered the
// moment the bolts start, so a reload never repeats it, and a click on the
// control ends it early. No text, no pointer, nothing to dismiss.

const seenKey = (id: string) => `vig.spark.${id}`
const seen = (id: string) => {
  try {
    return localStorage.getItem(seenKey(id)) !== null
  } catch {
    return true
  }
}
const remember = (id: string) => {
  try {
    localStorage.setItem(seenKey(id), String(Date.now()))
  } catch {
    // nothing to remember into: the bolts still play this once
  }
}

const PAD = 24
const BOLT_MS = 1400
const TIMES = 3

type Bolt = { d: string; delay: number }

// Deterministic jitter, so the same box always sparks the same way.
function rng(seed: number) {
  let s = seed * 2654435761 + 1
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296
    return s / 4294967296
  }
}

// Each bolt leaves the box at an edge point and zigzags outward, three or
// four short segments with a sideways kick between them; every other bolt
// throws a short branch off its second joint.
export function bolts(w: number, h: number, n = 7): Bolt[] {
  const out: Bolt[] = []
  const per = 2 * (w + h)
  for (let i = 0; i < n; i++) {
    const r = rng(i + 1)
    const t = ((i + r() * 0.6) / n) * per
    let x: number, y: number, nx: number, ny: number
    if (t < w) {
      x = t; y = 0; nx = 0; ny = -1
    } else if (t < w + h) {
      x = w; y = t - w; nx = 1; ny = 0
    } else if (t < 2 * w + h) {
      x = 2 * w + h - t; y = h; nx = 0; ny = 1
    } else {
      x = 0; y = per - t; nx = -1; ny = 0
    }
    x += PAD
    y += PAD
    const segs = 3 + Math.floor(r() * 3)
    const kick = (nx === 0 ? 1 : 0)
    const tangentX = kick ? 1 : 0
    const tangentY = kick ? 0 : 1
    let d = `M${x.toFixed(1)} ${y.toFixed(1)}`
    let branch = ''
    for (let s = 0; s < segs; s++) {
      const len = 4 + r() * 5
      const side = (s % 2 === 0 ? 1 : -1) * (3 + r() * 4)
      x += nx * len + tangentX * side
      y += ny * len + tangentY * side
      d += ` L${x.toFixed(1)} ${y.toFixed(1)}`
      if (s === 1 && i % 2 === 0) {
        const bx = x + nx * (3 + r() * 4) - tangentX * side * 1.5
        const by = y + ny * (3 + r() * 4) - tangentY * side * 1.5
        branch = ` M${x.toFixed(1)} ${y.toFixed(1)} L${bx.toFixed(1)} ${by.toFixed(1)}`
      }
    }
    out.push({ d: d + branch, delay: Math.round(r() * 500) })
  }
  return out
}

export function Spark({ id, children, className, ...rest }: { id: string; children: React.ReactNode; className?: string } & React.HTMLAttributes<HTMLSpanElement>) {
  const [live, setLive] = useState(() => !seen(id))
  const box = useRef<HTMLSpanElement>(null)
  // The bolts leave the control itself, not the wrapper, which a column
  // layout may stretch.
  const [size, setSize] = useState<{ w: number; h: number; x: number; y: number } | null>(null)
  useLayoutEffect(() => {
    if (!live || !box.current) return
    const outer = box.current.getBoundingClientRect()
    const r = (box.current.firstElementChild ?? box.current).getBoundingClientRect()
    setSize({ w: r.width, h: r.height, x: r.left - outer.left, y: r.top - outer.top })
  }, [live])
  useEffect(() => {
    if (!live) return
    remember(id)
    const t = setTimeout(() => setLive(false), BOLT_MS * TIMES + 500)
    return () => clearTimeout(t)
  }, [live, id])
  const w = (size?.w ?? 0) + 2 * PAD
  const h = (size?.h ?? 0) + 2 * PAD
  return (
    <span ref={box} className={cn('relative inline-flex', className)} onClickCapture={() => setLive(false)} {...rest}>
      {children}
      {live && size && (
        <svg aria-hidden="true" data-spark={id} className="pointer-events-none absolute overflow-visible" style={{ left: size.x - PAD, top: size.y - PAD, width: w, height: h }} viewBox={`0 0 ${w} ${h}`} fill="none">
          {bolts(size.w, size.h).map((b, i) => (
            <path key={i} d={b.d} className="spark-bolt" style={{ animationDelay: `${b.delay}ms`, animationDuration: `${BOLT_MS}ms`, animationIterationCount: TIMES }} />
          ))}
        </svg>
      )}
    </span>
  )
}
