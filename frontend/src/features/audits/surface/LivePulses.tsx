import { useEffect, useMemo, useRef, useState } from 'react'
import { cn } from '@/lib/utils'
import { SIGNERS, sideId } from './model'
import type { Ix, MapEdge, Model } from './model'
import { pulsesOf } from './live'
import type { Pulse } from './live'
import type { Evidence } from './types'
import { time } from './types'
import type { Ports, Pt } from './usePorts'
import { wire } from './usePorts'

// Calls as motion, as a function of time. Every call has the moment it
// happened on chain; at the playhead's moment a dot is exactly as far
// along its path as the time since its call says, so moving the playhead
// forward advances every dot, moving it back runs them in reverse, and
// live is the playhead advancing at one second per second. A call enters
// from whoever signed or called, crosses the instruction it hit and goes
// on to what it called; a failed call turns red and stops short. Each
// view routes the same rows through its own ports. Off under reduced
// motion.

// Route is one dot's motion: the main path, its share of the dot's life,
// where a failure stops along it, and extra dots that split off it (a
// second callee, a write to an account below) after a delay.
export type Route = { d: string; ms: number; stop?: number; extra?: { d: string; ms: number; delay: number }[] }

// LiveMap is what a view needs to draw motion: the rows that may be in
// flight, the playhead's moment (null is live), and how long one call
// takes to cross the map in chain time.
export type LiveMap = { rows: Evidence[]; at: number | null; life: number }

// liveLag is how far behind the wall clock the live playhead stands, so
// rows the feed delivers a few seconds late are still ahead of it.
export const liveLag = 7000
// baseLife is how long a call takes to cross the map at 1×, in ms.
export const baseLife = 1800
export const entryMs = 900
export const cpiMs = 700
const writeMs = 500
// maxInFlight bounds the dots drawn at once; the rest are counted, not drawn.
const maxInFlight = 48

export function LivePulses({ size, live, route, enabled }: { size: { w: number; h: number }; live: LiveMap; route: (p: Pulse) => Route | null; enabled: boolean }) {
  // The candidate set: rows whose call lies within one life of a coarse
  // playhead, re-picked a few times a second; the frame loop then places
  // each one by the fine playhead.
  // Live, the wall clock ticks the coarse playhead; otherwise the moment
  // the player holds is the playhead, and a seek moves it at once.
  const [clock, setClock] = useState(() => Math.floor((Date.now() - liveLag) / 250))
  const isLive = live.at === null
  useEffect(() => {
    if (!isLive) return
    const id = setInterval(() => setClock(Math.floor((Date.now() - liveLag) / 250)), 250)
    return () => clearInterval(id)
  }, [isLive])
  const coarse = live.at === null ? clock : Math.floor(live.at / 250)
  const atRef = useRef(live.at)
  const lifeRef = useRef(live.life)
  useEffect(() => {
    atRef.current = live.at
    lifeRef.current = live.life
  }, [live.at, live.life])

  const flight = useMemo(() => {
    const T = coarse * 250 + 250
    const from = T - live.life - 500
    const inRange = live.rows.filter((e) => {
      const t = time(e.at ?? e.created_at)
      return t >= from && t <= T
    })
    // Thinned evenly, so a burst still shows every instruction it hit.
    const step = Math.max(1, inRange.length / maxInFlight)
    const picked: Evidence[] = []
    for (let i = 0; i < inRange.length && picked.length < maxInFlight; i += step) picked.push(inRange[Math.floor(i)])
    return pulsesOf(picked).map((p, i) => ({ p, at: time(picked[i].at ?? picked[i].created_at), r: route(p) })).filter((x): x is { p: Pulse; at: number; r: Route } => Boolean(x.r))
  }, [coarse, live.rows, live.life, route])

  // Every frame, each dot is placed at its point along its path for the
  // playhead's moment.
  const svg = useRef<SVGSVGElement>(null)
  const moving = enabled && flight.length > 0
  useEffect(() => {
    if (!moving) return
    let frame = 0
    const tick = () => {
      const T = atRef.current ?? Date.now() - liveLag
      const life = lifeRef.current
      const root = svg.current
      if (root) {
        root.querySelectorAll('g[data-at]').forEach((gEl) => {
          const g = gEl as SVGGElement
          const called = Number(g.dataset.at)
          const total = Number(g.dataset.total)
          // Where this call stands in its life, in route milliseconds.
          const elapsed = ((T - called) / life) * total
          g.querySelectorAll('circle[data-path]').forEach((cEl) => {
            const c = cEl as SVGCircleElement
            const path = g.querySelector(`path[data-id="${c.dataset.path}"]`) as SVGPathElement | null
            if (!path) return
            const ms = Number(c.dataset.ms), delay = Number(c.dataset.delay ?? 0), stop = Number(c.dataset.stop ?? 1)
            const f = (elapsed - delay) / ms
            if (f < 0 || f > 1.2) {
              c.setAttribute('opacity', '0')
              return
            }
            const u = Math.min(1, f) * stop
            const len = path.getTotalLength()
            const pt = path.getPointAtLength(len * u)
            c.setAttribute('transform', `translate(${pt.x.toFixed(1)} ${pt.y.toFixed(1)})`)
            c.setAttribute('opacity', f <= 1 ? '1' : String(Math.max(0, 1 - (f - 1) / 0.2)))
          })
        })
      }
      frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [moving])

  if (!enabled || size.w === 0 || flight.length === 0) return null
  return (
    <svg ref={svg} className="pointer-events-none absolute inset-0 z-20 overflow-visible" width={size.w} height={size.h} aria-hidden>
      {flight.map(({ p, at, r }) => {
        const tone = p.failed ? 'fill-bad' : 'fill-primary'
        const total = r.ms + Math.max(0, ...(r.extra ?? []).map((x) => x.delay + x.ms))
        return (
          <g key={p.key} data-at={at} data-total={total}>
            <path data-id="main" d={r.d} fill="none" stroke="none" />
            <circle r={p.failed ? 4 : 3.5} className={cn(tone)} data-path="main" data-ms={r.ms} data-stop={r.stop ?? 1} opacity="0" />
            {(r.extra ?? []).map((x, i) => (
              <g key={i}>
                <path data-id={`x${i}`} d={x.d} fill="none" stroke="none" />
                <circle r={2.5} className={cn(tone)} data-path={`x${i}`} data-ms={x.ms} data-delay={x.delay} opacity="0" />
              </g>
            ))}
          </g>
        )
      })}
    </svg>
  )
}

// Where a call enters the system map: the signers when it was signed
// for, an outside caller when one made it, nowhere drawable when an
// audited program did (its own row carries that hop).
function entry(p: Pulse, ports: Ports, audited: Set<string>): Pt | undefined {
  if (!p.via) return ports[`o:${SIGNERS}`]
  if (audited.has(p.via)) return undefined
  // A caller the side folded has no port of its own: the fold node's.
  return ports[`o:${sideId(`p:${p.via}`)}`] ?? ports['o:more:in'] ?? ports[`o:${SIGNERS}`]
}

// systemRoute draws a call across the system map: entry to the row it
// hit (or the program's box), then on to its first callee; other callees
// split off at the row.
export function systemRoute(ports: Ports, anchor: (id: string) => string, audited: Set<string>): (p: Pulse) => Route | null {
  return (p) => {
    const to = ports[`i:${anchor(p.ixId)}`]
    if (!to) return null
    const from = entry(p, ports, audited)
    const out = ports[`o:${anchor(p.ixId)}`]
    const callees = p.cpi.map((c) => ports[`i:p:${c}`] ?? ports['i:more:out']).filter((x): x is Pt => Boolean(x))
    const parts: string[] = []
    let ms = 0
    if (from) {
      parts.push(wire(from, to, 'across').d)
      ms += entryMs
    }
    if (out && callees.length > 0 && !p.failed) {
      parts.push(wire(out, callees[0], 'across').d)
      ms += cpiMs
    }
    if (parts.length === 0) return null
    const extra = out && !p.failed ? callees.slice(1).map((c) => ({ d: wire(out, c, 'across').d, ms: cpiMs, delay: from ? entryMs : 0 })) : []
    return { d: parts.join(' '), ms, stop: p.failed && from ? 0.6 : 1, extra }
  }
}

// instructionRoute draws a call through one instruction laid out in
// execution order: the caller into the handler, the handler out to what
// it called, and drops to the accounts it may have written.
export function instructionRoute(ports: Ports, ix: Ix, model: Model, inbound: MapEdge[]): (p: Pulse) => Route | null {
  const audited = new Set(model.programs.map((x) => x.pid))
  // A caller that is folded has no port of its own: it enters from the
  // fold's node.
  const port = (end: string): Pt | undefined => ports[`o:${end}`] ?? ports['o:more:in']
  return (p) => {
    if (p.ixId !== ix.id) return null
    const to = ports['i:comp'], out = ports['o:comp'], bl = ports['bl:comp'], br = ports['br:comp']
    if (!to) return null
    let from: Pt | undefined
    if (!p.via) from = port(SIGNERS)
    else if (audited.has(p.via)) {
      const e = inbound.find((x) => model.ixs.get(x.from)?.pid === p.via)
      from = e ? port(e.from) : undefined
    } else from = port(sideId(`p:${p.via}`)) ?? ports['o:more:in']
    const callees = p.cpi.map((c) => ports[`i:p:${c}`]).filter((x): x is Pt => Boolean(x))
    const parts: string[] = []
    let ms = 0
    if (from) {
      parts.push(wire(from, to, 'across').d)
      ms += entryMs
    }
    if (out && callees.length > 0 && !p.failed) {
      parts.push(wire(out, callees[0], 'across').d)
      ms += cpiMs
    }
    const extra: Route['extra'] = []
    if (out && !p.failed) for (const c of callees.slice(1)) extra.push({ d: wire(out, c, 'across').d, ms: cpiMs, delay: from ? entryMs : 0 })
    // What it commits: a drop to each writable account, after the handler.
    if (bl && br && !p.failed) {
      const writable = ix.accounts.filter((a) => a.facts?.writable === 'true' && !a.facts?.optional).slice(0, 4)
      for (const a of writable) {
        const top = ports[`n:${a.id}`]
        if (!top) continue
        const x = Math.max(bl.x + 18, Math.min(br.x - 18, top.x))
        extra.push({ d: wire({ x, y: bl.y }, top, 'down').d, ms: writeMs, delay: from ? entryMs : 0 })
      }
    }
    if (parts.length === 0 && extra.length === 0) return null
    if (parts.length === 0) {
      // Entered from an audited caller that is not drawn: start on the handler.
      parts.push(`M${to.x} ${to.y} L${to.x} ${to.y}`)
      ms = 1
    }
    return { d: parts.join(' '), ms, stop: p.failed && from ? 0.6 : 1, extra }
  }
}
