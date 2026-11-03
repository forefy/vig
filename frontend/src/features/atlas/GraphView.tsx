import { useEffect, useMemo, useRef, useState } from 'react'
import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react'
import { StateBadge, Copy, Empty } from '@/components/primitives'
import { cn } from '@/lib/utils'
import { CHAR, byWeight, cutTo, hubsOf, layout, pathOf, placeLabels, radiusOf, shown, spots } from './graph'
import type { Cols, Graph as Picture, GraphNode, Kind } from './graph'

// A result of edges drawn as a network. A circle per id in its kind's
// tone, sized by its relations, its label after it; an edge per row, in
// the relation's tone, dashed below full confidence. Around the result,
// the other relations the index holds for its nodes are shadows: faint
// lines to faint circles, named only when the pointer rests on them or on
// a neighbour, so the path reads inside its neighbourhood.
// The picture opens framing the result, however small that draws it, and
// fit frames it again. Labels keep a readable size at any scale and
// give way to one another: drawn small, the picture names its weightiest
// nodes; closer, it names more. A press on the frame takes it (on a touch
// screen, a first touch only takes it): then one pointer pans, two pinch,
// a scroll pans and a wheel with ctrl or cmd zooms; before that press the
// page scrolls over it as usual. The − and + buttons zoom either way. A
// press on a node selects it and centres it: its whole id, what touches it
// and the place it names open beside the picture on a wide screen and
// under it on a narrow one; expand asks the edges table for what touches
// it and the console merges the rows, so the picture grows from the
// query. Nodes take the keyboard: tab to one, enter or space to press it.

// Grey is the picture: what a thing is reads from its label, not a hue.
// A function, a host or an entry is the foreground, a call or a value a
// step down, a variable or a body fainter; every edge is grey until it is
// hovered or picked. A sink is red, as everywhere.
const TONE: Record<Kind, { fill: string; text: string }> = {
  host: { fill: 'fill-foreground', text: 'text-foreground' },
  subdomain: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  resource: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  sink: { fill: 'fill-bad', text: 'text-bad' },
  body: { fill: 'fill-faint', text: 'text-muted-foreground' },
  repo: { fill: 'fill-foreground', text: 'text-foreground' },
  function: { fill: 'fill-foreground', text: 'text-foreground' },
  call: { fill: 'fill-muted-foreground', text: 'text-foreground' },
  var: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  ret: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  literal: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  entry: { fill: 'fill-foreground', text: 'text-foreground' },
  value: { fill: 'fill-muted-foreground', text: 'text-foreground' },
  param: { fill: 'fill-muted-foreground', text: 'text-muted-foreground' },
  node: { fill: 'fill-foreground', text: 'text-foreground' },
}

// The result's own edges read in the foreground, a second relation a step
// down and long-dashed, any further one dotted; a sink's edge is red. A
// shadow is faint and solid, and a shadow circle is the faintest grey
// whatever it is, but a sink.
const REL_STYLES: { tone: string; dash?: string }[] = [{ tone: 'stroke-foreground' }, { tone: 'stroke-muted-foreground', dash: '8 4' }]
const REL_DEFAULT = 'stroke-foreground'
const REL_SHADOW = 'stroke-muted-foreground'

const K_MIN = 0.15
const K_MAX = 4
// A picked node is shown at least this close, so it and its neighbours read.
const K_PICK = 0.8
// A label's size on screen, against its size at 1, whatever the zoom: so
// zooming in frees room and names more.
const TEXT = 0.9
// A frame narrower than this (a phone) opens no smaller than K_NARROW, on
// the weightiest node, where a whole large result would be too small to
// read or press; fit still shows it whole.
const NARROW = 520
const K_NARROW = 0.55
type View = { k: number; x: number; y: number }

// zoomed is the view scaled by f around a point of the frame.
function zoomed(v: View, f: number, px: number, py: number): View {
  const k = Math.min(K_MAX, Math.max(K_MIN, v.k * f))
  return { k, x: px - ((px - v.x) * k) / v.k, y: py - ((py - v.y) * k) / v.k }
}

type Touch = { x: number; y: number }

// The layout runs in a worker where the page has one, so settling a large
// picture does not hold the page; where it has none (or the worker fails)
// it runs here. Only the fields the layout reads cross to the worker.
let worker: Worker | null | undefined
let asks = 0
const NO_SHADOWS: Record<string, unknown>[] = []
function layoutWorker(): Worker | null {
  if (worker !== undefined) return worker
  try {
    worker = new Worker(new URL('./layout.worker.ts', import.meta.url), { type: 'module' })
  } catch {
    worker = null
  }
  return worker
}
const plain = (rows: Record<string, unknown>[], fields: string[]) =>
  rows.map((r) => {
    const o: Record<string, unknown> = {}
    for (const f of fields) {
      const v = r[f]
      if (v === undefined) continue
      o[f] = typeof v === 'bigint' ? Number(v) : v === null || typeof v !== 'object' ? v : String(v)
    }
    return o
  })
const EDGE_FIELDS = ['src_id', 'dst_id', 'rel', 'confidence', 'src_label', 'dst_label']

function useNetwork(rows: Record<string, unknown>[], cols: Cols, shadows: Record<string, unknown>[]): Picture | null {
  const here = typeof Worker === 'undefined'
  const local = useMemo(() => (here ? layout(rows, cols, shadows) : null), [here, rows, cols, shadows])
  const [made, setMade] = useState<Picture | null>(null)
  useEffect(() => {
    if (here) return
    let live = true
    const id = ++asks
    const fallback = () => Promise.resolve().then(() => live && setMade(layout(rows, cols, shadows)))
    const w = layoutWorker()
    if (!w) {
      void fallback()
      return () => {
        live = false
      }
    }
    const onMessage = (e: MessageEvent<{ id: number; g: Picture }>) => {
      if (live && e.data.id === id) setMade(e.data.g)
    }
    const onError = () => {
      worker = null
      void fallback()
    }
    w.addEventListener('message', onMessage)
    w.addEventListener('error', onError)
    const fields = 'node' in cols ? [...cols.node, 'label', 'method', 'path'] : [cols.src, cols.dst, ...(cols.rel ? [cols.rel] : []), 'confidence', 'src_label', 'dst_label']
    w.postMessage({ id, rows: plain(rows, fields), cols, shadows: plain(shadows, EDGE_FIELDS) })
    return () => {
      live = false
      w.removeEventListener('message', onMessage)
      w.removeEventListener('error', onError)
    }
  }, [here, rows, cols, shadows])
  return here ? local : made
}

export function Graph({ rows, cols, shadows, onExpand, place }: { rows: Record<string, unknown>[]; cols: Cols; shadows?: Record<string, unknown>[]; onExpand?: (id: string) => void; place?: (id: string) => ReactNode }) {
  const g = useNetwork(rows, cols, shadows ?? NO_SHADOWS)
  if (!g) return <div aria-busy="true" className="h-[280px] rounded-md border border-border bg-card/50" />
  return <Network g={g} onExpand={onExpand} place={place} />
}

function Network({ g, onExpand, place }: { g: Picture; onExpand?: (id: string) => void; place?: (id: string) => ReactNode }) {
  const byId = useMemo(() => new Map(g.nodes.map((n) => [n.id, n])), [g])
  // A pick or a hover counts only while its node is in the picture, so a
  // new result does not open dimmed around a node it lacks.
  const [hotId, setHot] = useState<string | null>(null)
  const [pickedId, setPicked] = useState<string | null>(null)
  const hot = hotId && byId.has(hotId) ? hotId : null
  const picked = pickedId && byId.has(pickedId) ? pickedId : null
  // The view: a scale and an offset in graph px, changed by wheel and drag,
  // kept with the picture it was made for by content, so the same rows
  // run again keep it; a new picture opens fitted.
  const [movedOn, setMovedOn] = useState<{ key: string; v: View } | null>(null)
  const moved = movedOn && movedOn.key === g.key ? movedOn.v : null
  // A picked node is centred at the scale of the moment, until the view
  // moves again.
  const [focusOn, setFocusOn] = useState<{ key: string; id: string; k: number } | null>(null)
  // A node expanded is followed into the grown picture, which is laid out
  // again, so the reader stays on it.
  const [follow, setFollow] = useState<{ id: string; k: number } | null>(null)
  const focus = useMemo(() => (focusOn && focusOn.key === g.key ? focusOn : follow && byId.has(follow.id) ? { key: g.key, id: follow.id, k: follow.k } : null), [focusOn, follow, g.key, byId])
  // The frame takes the wheel and the touch only once pressed, so the
  // page scrolls over it until then.
  const [active, setActive] = useState(false)
  const [box, setBox] = useState({ w: 800, h: 480 })
  // The picture is shown once the frame is measured, so it does not open at
  // a guessed size and move.
  const [measured, setMeasured] = useState(false)
  const frame = useRef<HTMLDivElement>(null)
  const svg = useRef<SVGSVGElement>(null)
  // The pointers on the frame; one drags, two pinch.
  const pointers = useRef(new Map<number, Touch>())
  const gesture = useRef<{ view: View; x: number; y: number; d: number; moved: boolean } | null>(null)
  // A tap on a touch screen picks the node nearest where the finger lifted;
  // the click the browser sends after it, which it may move onto another
  // node, is let pass.
  const tapped = useRef(false)
  const first = useRef<{ id: number; x: number; y: number; moved: boolean } | null>(null)
  const tap = (cx: number, cy: number) => {
    const id = nearest(cx, cy)
    if (id) pick(id)
    // Whatever node the browser's click after it lands on, it is let pass.
    tapped.current = true
    setTimeout(() => (tapped.current = false), 400)
  }
  const [dragging, setDragging] = useState(false)
  const pane = useRef<HTMLDivElement>(null)
  const drawn = g.nodes.length > 0
  useEffect(() => {
    const el = frame.current
    if (!el) return
    const ro = new ResizeObserver(([entry]) => {
      if (!entry) return
      setBox({ w: entry.contentRect.width, h: entry.contentRect.height })
      setMeasured(true)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [drawn])
  // The opening view frames the result's own extent, which the shadows do
  // not change, so the result holds still on screen when they arrive.
  const fw = g.fit.w
  const fh = g.fit.h
  const framed = useMemo(() => {
    const k = Math.min(1, Math.max(K_MIN, Math.min((box.w - 24) / Math.max(1, fw), (box.h - 24) / Math.max(1, fh))))
    return { k, x: (box.w - fw * k) / 2, y: (box.h - fh * k) / 2 }
  }, [box, fw, fh])
  const heaviest = useMemo(() => byId.get(byWeight(g.nodes.filter((n) => !n.shadow))[0] ?? ''), [g, byId])
  const fitted = useMemo(() => {
    if (box.w >= NARROW || framed.k >= K_NARROW || !heaviest) return framed
    return { k: K_NARROW, x: box.w / 2 - heaviest.x * K_NARROW, y: box.h / 2 - heaviest.y * K_NARROW }
  }, [box, framed, heaviest])
  const centred = useMemo(() => {
    if (!focus) return null
    const n = byId.get(focus.id)
    if (!n) return null
    // A node with few relations is framed with all of them, smaller if it
    // must be, so none is left outside; a hub sits a third of the way in,
    // so what it reaches, to its right, shows.
    // The result's own edges decide it, so the view holds when the
    // relations around the result arrive; a node with no edge of its own
    // (a result of nodes) is framed with the relations around it.
    const tiesOf = (own: boolean) => {
      const out = new Set<string>()
      for (const l of g.links) {
        if (own && l.shadow) continue
        if (l.source === n.id) out.add(l.target)
        if (l.target === n.id) out.add(l.source)
      }
      return out
    }
    const mine = tiesOf(true)
    const ties = mine.size > 0 ? mine : tiesOf(false)
    if (ties.size > 0 && ties.size <= 12) {
      const all = [n, ...[...ties].map((id) => byId.get(id)).filter((m): m is GraphNode => m !== undefined)]
      const pad = 48
      // Sized by the result's own edges, so the frame holds when the
      // relations around the result arrive and the circles grow.
      const r = (m: GraphNode) => radiusOf(m.into + m.out, false)
      const x0 = Math.min(...all.map((m) => m.x - r(m))) - pad
      const x1 = Math.max(...all.map((m) => m.x + r(m))) + pad * 3
      const y0 = Math.min(...all.map((m) => m.y - r(m))) - pad
      const y1 = Math.max(...all.map((m) => m.y + r(m))) + pad
      const k = Math.max(K_MIN, Math.min(focus.k, box.w / (x1 - x0), box.h / (y1 - y0)))
      return { k, x: box.w / 2 - ((x0 + x1) / 2) * k, y: box.h / 2 - ((y0 + y1) / 2) * k }
    }
    return { k: focus.k, x: box.w / 3 - n.x * focus.k, y: box.h / 2 - n.y * focus.k }
  }, [focus, byId, box, g])
  // The whole picture, the relations around the result included.
  const whole = useMemo(() => {
    let x0 = Infinity
    let y0 = Infinity
    let x1 = -Infinity
    let y1 = -Infinity
    for (const n of g.nodes) {
      x0 = Math.min(x0, n.x - n.r)
      y0 = Math.min(y0, n.y - n.r)
      x1 = Math.max(x1, n.x + n.r)
      y1 = Math.max(y1, n.y + n.r)
    }
    if (!Number.isFinite(x0)) return framed
    const pad = 12
    const k = Math.min(1, Math.max(K_MIN, Math.min(box.w / Math.max(1, x1 - x0 + pad * 2), box.h / Math.max(1, y1 - y0 + pad * 2))))
    return { k, x: box.w / 2 - ((x0 + x1) / 2) * k, y: box.h / 2 - ((y0 + y1) / 2) * k }
  }, [g, box, framed])
  const [fitAllOn, setFitAllOn] = useState<string | null>(null)
  const resting = (fitAllOn === g.key ? whole : null) ?? centred ?? fitted
  const view = moved ?? resting
  const setMoved = (f: ((cur: View | null) => View) | null) => setMovedOn((cur) => (f === null ? null : { key: g.key, v: f(cur && cur.key === g.key ? cur.v : null) }))
  // A scroll pans; a pinch on a trackpad, which arrives as a wheel with
  // ctrl held, or a wheel with ctrl or cmd zooms around the pointer by the
  // size of the gesture. The listener is attached by hand because React's
  // wheel listeners are passive and the page would scroll. Until the frame
  // is pressed only the zoom gesture is taken, and the page scrolls.
  useEffect(() => {
    const el = svg.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      const zoom = e.ctrlKey || e.metaKey
      if (!active && !zoom) return
      e.preventDefault()
      const rect = el.getBoundingClientRect()
      const px = e.clientX - rect.left
      const py = e.clientY - rect.top
      // A wheel that counts lines or pages is read in pixels.
      const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? rect.height : 1
      const dx = e.deltaX * unit
      const dy = e.deltaY * unit
      setMoved((cur) => {
        const v = cur ?? resting
        if (zoom) {
          const d = Math.max(-40, Math.min(40, dy))
          return zoomed(v, Math.exp(-d * 0.01), px, py)
        }
        return { k: v.k, x: v.x - dx, y: v.y - dy }
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resting, g, active])
  // A press anywhere else lets the frame go.
  useEffect(() => {
    if (!active) return
    const off = (e: PointerEvent) => {
      if (frame.current && !frame.current.contains(e.target as Node)) setActive(false)
    }
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setActive(false)
    }
    document.addEventListener('pointerdown', off)
    document.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('pointerdown', off)
      document.removeEventListener('keydown', key)
    }
  }, [active])

  // A pick keeps the picture in view, where its colour is: beside it the
  // pane scrolls itself; under it, the reader scrolls on to the pane.
  useEffect(() => {
    if (!picked || !frame.current) return
    frame.current.scrollIntoView({ block: 'nearest' })
  }, [picked])

  const near = useMemo(() => {
    const at = hot ?? picked
    if (!at) return null
    const s = new Set([at])
    for (const l of g.links) {
      if (l.source === at) s.add(l.target)
      if (l.target === at) s.add(l.source)
    }
    return s
  }, [hot, picked, g])
  // The picked node's first relations, in or out, shadows included, are
  // coloured, and so are the edges that reach them.
  const touched = useMemo(() => {
    if (!picked) return null
    const s = new Set<string>()
    for (const l of g.links) {
      if (l.source === picked) s.add(l.target)
      if (l.target === picked) s.add(l.source)
    }
    s.delete(picked)
    return s
  }, [picked, g])

  // The labels are drawn at one size on screen, by steps of the zoom
  // buttons, and placed for that size inside what the frame shows: the node
  // under the pointer or picked first, then its neighbours, then the
  // result's nodes by their relations; a shadow is named only beside the
  // one in hand. The frame's part of the picture is taken by steps of a few
  // dozen pixels, so a pan does not place them again at every move.
  const coarse = typeof matchMedia === 'function' && matchMedia('(pointer: coarse)').matches
  const step = Math.pow(1.25, Math.round(Math.log(view.k) / Math.log(1.25)))
  const text = TEXT / step
  const hand = hot ?? picked
  const q = (v: number) => Math.round(v / 48) * 48
  const sx = q(view.x)
  const sy = q(view.y)
  const sk = Math.round(view.k * 50) / 50
  // A margin of half a step of the pan, and a few pixels, on every side.
  const seen = useMemo(() => {
    const pad = 28 / sk
    return { x0: -sx / sk + pad, y0: -sy / sk + pad, x1: (box.w - sx) / sk - pad, y1: (box.h - sy) / sk - pad }
  }, [sk, sx, sy, box])
  // The frame's part of the picture as it is now, and the spot that leaves
  // it least.
  const frameBox = { x0: -view.x / view.k, y0: -view.y / view.k, x1: (box.w - view.x) / view.k, y1: (box.h - view.y) / view.k }
  const leastOut = (options: ReturnType<typeof spots>) => {
    const out = (b: (typeof options)[number]['box']) => Math.max(0, frameBox.x0 - b.x0) + Math.max(0, b.x1 - frameBox.x1) + Math.max(0, frameBox.y0 - b.y0) + Math.max(0, b.y1 - frameBox.y1)
    return options.reduce((best, o) => (out(o.box) < out(best.box) ? o : best), options[0]!)
  }
  const named = useMemo(() => {
    const first = hand && near ? [hand, ...byWeight(g.nodes.filter((n) => near.has(n.id) && n.id !== hand))] : []
    const rest = byWeight(g.nodes.filter((n) => !n.shadow))
    const loose = new Set([...first, ...hubsOf(g.nodes)])
    return placeLabels(g.nodes, [...first, ...rest], text, { x0: 0, y0: 0, x1: g.fit.w, y1: g.fit.h }, loose, seen)
  }, [g, near, hand, text, seen])
  // Each relation of the result in its own tone, in the order they come.
  const relOrder = useMemo(() => [...new Set(g.links.filter((l) => !l.shadow && l.rel !== 'sinks').map((l) => l.rel || 'edge'))], [g])
  const relStroke = (rel: string) => (rel === 'sinks' ? 'stroke-bad' : (REL_STYLES[relOrder.indexOf(rel || 'edge')]?.tone ?? REL_DEFAULT))
  const relDash = (rel: string) => {
    const i = relOrder.indexOf(rel || 'edge')
    return i < 0 ? undefined : i < REL_STYLES.length ? REL_STYLES[i]!.dash : '1 3'
  }

  // The picked node's neighbours, when they are few, are all named: one the
  // crowd left no place takes a spot clear of every other label, over
  // circles if it must, or stays to the pane's list.
  const extra = new Map<string, ReturnType<typeof spots>[number]>()
  if (picked && touched && touched.size <= 24) {
    const boxes = [...named.values()].map((x) => x.box)
    // The picked node's own label, wherever it went, is taken first.
    const self = byId.get(picked)
    if (self && !named.has(picked)) boxes.push(leastOut(spots(self, text)).box)
    const meets = (a: { x0: number; y0: number; x1: number; y1: number }, b: { x0: number; y0: number; x1: number; y1: number }) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1
    for (const id of byWeight(g.nodes.filter((m) => touched.has(m.id) && !named.has(m.id)))) {
      const m = byId.get(id)!
      const free = spots(m, text).filter((o) => !boxes.some((b) => meets(o.box, b)))
      if (!free.length) continue
      const spot = leastOut(free)
      extra.set(id, spot)
      boxes.push(spot.box)
    }
  }

  // The node in hand is always named. Where the crowd left it no place it
  // takes one anyway, and a label already there gives way to it.
  const handSpots = new Map<string, ReturnType<typeof spots>[number]>()
  for (const id of [hot, picked]) {
    if (!id || named.has(id) || handSpots.has(id)) continue
    const m = byId.get(id)
    if (m) handSpots.set(id, leastOut(spots(m, text)))
  }
  const hidden = (id: string, box: { x0: number; y0: number; x1: number; y1: number }) => {
    for (const [hid, hs] of handSpots) if (hid !== id && box.x0 < hs.box.x1 && hs.box.x0 < box.x1 && box.y0 < hs.box.y1 && hs.box.y0 < box.y1) return true
    return false
  }

  if (!g.nodes.length) return <Empty title="no edges" />
  const counts = new Map<Kind, number>()
  for (const n of g.nodes) if (!n.shadow) counts.set(n.kind, (counts.get(n.kind) ?? 0) + 1)
  const rels = new Map<string, number>()
  for (const l of g.links) if (!l.shadow) rels.set(l.rel || 'edge', (rels.get(l.rel || 'edge') ?? 0) + 1)
  // Edges below full confidence are a name matched, not a call resolved.
  const byName = g.links.filter((l) => !l.shadow && l.confidence !== undefined && l.confidence < 1).length
  const node = picked ? byId.get(picked) : undefined
  // The picked node's neighbours each way, counted in its card; the
  // picture shows and names them.
  const ties: { side: 'in' | 'out'; list: GraphNode[] }[] = []
  if (node) {
    for (const side of ['in', 'out'] as const) {
      const ids = new Set<string>()
      for (const l of g.links) {
        if (side === 'in' && l.target === node.id) ids.add(l.source)
        if (side === 'out' && l.source === node.id) ids.add(l.target)
      }
      const list = byWeight([...ids].map((id) => byId.get(id)).filter((m): m is GraphNode => m !== undefined)).map((id) => byId.get(id)!)
      if (list.length) ties.push({ side, list })
    }
  }
  const at = hot ?? picked

  // Where the pointers are, as a centre and a spread, in frame px.
  const spread = () => {
    const rect = svg.current?.getBoundingClientRect()
    const ps = [...pointers.current.values()]
    const x = ps.reduce((s, p) => s + p.x, 0) / ps.length - (rect?.left ?? 0)
    const y = ps.reduce((s, p) => s + p.y, 0) / ps.length - (rect?.top ?? 0)
    const d = ps.length >= 2 ? Math.hypot(ps[0]!.x - ps[1]!.x, ps[0]!.y - ps[1]!.y) : 0
    return { x, y, d }
  }
  const pick = (id: string) => {
    if (id === picked) {
      setPicked(null)
      return
    }
    setPicked(id)
    setFocusOn({ key: g.key, id, k: Math.max(view.k, K_PICK) })
    setFollow(null)
    setMovedOn(null)
    setFitAllOn(null)
  }
  const grow = (id: string) => {
    setFitAllOn(null)
    setPicked(id)
    setFollow({ id, k: Math.max(view.k, K_PICK) })
    onExpand?.(id)
  }
  const onDown = (e: ReactPointerEvent<SVGSVGElement>) => {
    // On a touch screen the page scrolls under a finger until the frame is
    // taken, so a first touch only takes it: it pans nothing, and if it
    // lifts where it landed it picks the node there.
    if (e.pointerType === 'touch' && !active) {
      setActive(true)
      first.current = { id: e.pointerId, x: e.clientX, y: e.clientY, moved: false }
      return
    }
    setActive(true)
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const s = spread()
    gesture.current = { view, x: s.x, y: s.y, d: s.d, moved: gesture.current?.moved ?? false }
    setDragging(true)
  }
  const onMove = (e: ReactPointerEvent<SVGSVGElement>) => {
    const f = first.current
    if (f && f.id === e.pointerId && Math.abs(e.clientX - f.x) + Math.abs(e.clientY - f.y) > 3) f.moved = true
    if (!pointers.current.has(e.pointerId)) return
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    const st = gesture.current
    if (!st) return
    const s = spread()
    const dx = s.x - st.x
    const dy = s.y - st.y
    if (Math.abs(dx) + Math.abs(dy) > 3 || (st.d > 0 && Math.abs(s.d - st.d) > 3)) st.moved = true
    if (!st.moved) return
    // Two pointers scale the view they started on around where they began,
    // then carry it with their centre; one pointer only carries it.
    const scaled = st.d > 0 && s.d > 0 ? zoomed(st.view, s.d / st.d, st.x, st.y) : st.view
    setMoved(() => ({ k: scaled.k, x: scaled.x + dx, y: scaled.y + dy }))
  }
  const nearest = (cx: number, cy: number) => {
    const rect = svg.current?.getBoundingClientRect()
    if (!rect) return null
    const gx = (cx - rect.left - view.x) / view.k
    const gy = (cy - rect.top - view.y) / view.k
    let best: string | null = null
    let gap = 22 / view.k
    for (const n of g.nodes) {
      const d = Math.hypot(n.x - gx, n.y - gy) - n.r
      if (d <= gap) {
        gap = d
        best = n.id
      }
    }
    return best
  }
  const onUp = (e: ReactPointerEvent<SVGSVGElement>) => {
    const f = first.current
    if (f && f.id === e.pointerId) {
      first.current = null
      if (e.type === 'pointerup' && !f.moved) tap(e.clientX, e.clientY)
      return
    }
    const still = e.type === 'pointerup' && e.pointerType === 'touch' && pointers.current.size === 1 && pointers.current.has(e.pointerId) && gesture.current !== null && !gesture.current.moved && gesture.current.d === 0
    if (still) tap(e.clientX, e.clientY)
    pointers.current.delete(e.pointerId)
    if (pointers.current.size > 0) {
      // A finger lifted out of a pinch: the other carries on from here.
      const s = spread()
      gesture.current = { view, x: s.x, y: s.y, d: s.d, moved: true }
      return
    }
    setDragging(false)
    // The click a browser sends after a drag must not pick a node, so a
    // gesture that moved lingers until that click has passed.
    if (gesture.current?.moved) setTimeout(() => (gesture.current = null), 0)
    else gesture.current = null
  }
  // fit frames everything drawn, the relations around the result too, so
  // nothing is left outside the frame, on a phone as on a desk. It holds
  // until the reader pans, zooms or picks, so relations that arrive after
  // the press are framed too.
  const fit = () => {
    setFitAllOn(g.key)
    setMovedOn(null)
    setFocusOn(null)
    setFollow(null)
  }
  const zoomBy = (f: number) => setMoved((cur) => zoomed(cur ?? resting, f, box.w / 2, box.h / 2))
  // Under a finger a control takes the usual 44 px; under a mouse it stays small.
  const touch = '[@media(pointer:coarse)]:min-h-11 [@media(pointer:coarse)]:min-w-11'
  const tool = cn('rounded-sm border border-border px-1.5 py-0.5 hover:text-foreground', touch)
  const height = `min(64vh, ${Math.max(280, fh + 32)}px)`
  const around = g.nodes.filter((n) => n.shadow).length
  const litEdges = g.links.filter((l) => !l.shadow).length

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-muted-foreground">
        <span>
          <span className="num text-foreground">{g.nodes.length - around}</span> nodes
        </span>
        {litEdges > 0 && (
          <span>
            <span className="num text-foreground">{litEdges}</span> edges
          </span>
        )}
        {[...counts.entries()].map(([k, n]) => (
          <span key={k} className="inline-flex items-center gap-1.5">
            <svg width="8" height="8" aria-hidden="true">
              <circle cx="4" cy="4" r="4" className={TONE[k].fill} />
            </svg>
            {k} <span className="num text-foreground">{n}</span>
          </span>
        ))}
        {[...rels.entries()].map(([rel, n]) => (
          <span key={rel} className="inline-flex items-center gap-1.5">
            <svg width="16" height="8" aria-hidden="true" className={relStroke(rel)} strokeWidth="2">
              <line x1="0" y1="4" x2="16" y2="4" strokeDasharray={relDash(rel)} />
            </svg>
            {rel} <span className="num text-foreground">{n}</span>
          </span>
        ))}
        {byName > 0 && (
          <span className="inline-flex items-center gap-1.5">
            <svg width="16" height="8" aria-hidden="true" className={REL_DEFAULT} strokeWidth="2">
              <line x1="0" y1="4" x2="16" y2="4" strokeDasharray="3 3" />
            </svg>
            by name <span className="num text-foreground">{byName}</span>
          </span>
        )}
        {around > 0 && (
          <span className="inline-flex items-center gap-1.5">
            <svg width="16" height="8" aria-hidden="true" className={cn(REL_SHADOW, 'opacity-40')} strokeWidth="2">
              <line x1="0" y1="4" x2="16" y2="4" />
            </svg>
            around <span className="num text-foreground">{around}</span>
          </span>
        )}
        {g.dropped > 0 && (
          <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">
            <span className="num">{g.dropped}</span> rows not drawn
          </StateBadge>
        )}
        <span className="ml-auto inline-flex items-center gap-1">
          <button type="button" onClick={() => zoomBy(1 / 1.25)} aria-label="Zoom out" className={cn(tool, 'w-6 text-center')}>
            −
          </button>
          <button type="button" onClick={() => zoomBy(1.25)} aria-label="Zoom in" className={cn(tool, 'w-6 text-center')}>
            +
          </button>
          <button type="button" onClick={fit} className={tool}>
            fit
          </button>
        </span>
      </div>
      <div className={cn('grid items-start gap-3', node && 'lg:grid-cols-[minmax(0,1fr)_minmax(340px,42%)]')}>
        <div ref={frame} style={{ height }} className={cn('overflow-hidden rounded-md border bg-card/50', active ? 'border-foreground/40' : 'border-border')}>
          <svg
            ref={svg}
            width="100%"
            height="100%"
            role="group"
            aria-label="The result as a graph"
            onPointerDown={onDown}
            onPointerMove={onMove}
            onPointerUp={onUp}
            onPointerCancel={onUp}
            onPointerLeave={onUp}
            className={cn('block select-none', active ? 'touch-none' : 'touch-pan-y', dragging ? 'cursor-grabbing' : 'cursor-grab')}
          >
            <defs>
              <marker id="atlas-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" className="fill-muted-foreground" />
              </marker>
              <marker id="atlas-arrow-lit" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" className="fill-foreground" />
              </marker>
              <marker id="atlas-arrow-touched" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                <path d="M 0 0 L 10 5 L 0 10 z" className="fill-foreground" />
              </marker>
            </defs>
            <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`} visibility={measured ? undefined : 'hidden'}>
              {g.links.map((l, i) => {
                const lit = at !== null && (l.source === at || l.target === at)
                const tint = picked !== null && (l.source === picked || l.target === picked)
                return (
                  <path
                    key={i}
                    d={pathOf(l.points)}
                    fill="none"
                    markerEnd={tint ? 'url(#atlas-arrow-touched)' : lit ? 'url(#atlas-arrow-lit)' : l.shadow ? undefined : 'url(#atlas-arrow)'}
                    vectorEffect="non-scaling-stroke"
                    strokeWidth={lit ? 1.8 : l.shadow ? 0.8 : 1.4}
                    strokeDasharray={l.confidence !== undefined && l.confidence < 1 ? '3 3' : l.shadow ? undefined : relDash(l.rel)}
                    data-touched={tint || undefined} className={cn(tint ? 'stroke-foreground' : lit ? 'stroke-foreground' : l.shadow ? REL_SHADOW : relStroke(l.rel), at !== null && !lit ? 'opacity-15' : tint ? 'opacity-90' : l.shadow ? 'opacity-30' : 'opacity-85')}
                  >
                    <title>{l.rel ? `${l.rel} ${l.source} → ${l.target}` : `${l.source} → ${l.target}`}</title>
                  </path>
                )
              })}
              {g.nodes.map((n) => {
                const size = n.r
                const dim = near !== null && !near.has(n.id)
                const on = hot === n.id || picked === n.id
                const hit = touched?.has(n.id) ?? false
                // The press area is larger than a small circle on screen,
                // larger still under a finger.
                const reach = Math.max(size, (coarse ? 14 : 7) / view.k)
                return (
                  <g
                    key={n.id}
                    transform={`translate(${n.x} ${n.y})`}
                    role="button"
                    tabIndex={0}
                    aria-label={n.label}
                    aria-pressed={picked === n.id}
                    onPointerEnter={(e) => e.pointerType === 'mouse' && setHot(n.id)}
                    onPointerLeave={(e) => e.pointerType === 'mouse' && setHot(null)}
                    onFocus={(e) => e.currentTarget.matches(':focus-visible') && setHot(n.id)}
                    onBlur={() => setHot(null)}
                    onClick={(e) => {
                      e.stopPropagation()
                      if (gesture.current?.moved || tapped.current) return
                      pick(n.id)
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        pick(n.id)
                      }
                    }}
                    onDoubleClick={(e) => {
                      e.stopPropagation()
                      if (onExpand) grow(n.id)
                    }}
                    className={cn('cursor-pointer outline-none', dim ? 'opacity-25' : n.shadow && !on && !hit && 'opacity-50')}
                  >
                    <title>{n.id}</title>
                    {reach > size && <circle r={reach} fill="transparent" />}
                    <circle r={size} vectorEffect="non-scaling-stroke" className={cn(hit && n.kind !== 'sink' ? 'fill-foreground' : n.shadow && n.kind !== 'sink' ? 'fill-faint' : TONE[n.kind].fill, on ? 'stroke-foreground' : hit && 'stroke-foreground')} strokeWidth={1.5} data-touched={hit || undefined} />
                  </g>
                )
              })}
              {/* The labels, above every circle, so none is covered. */}
              <g aria-hidden="true" pointerEvents="none">
                {g.nodes.map((n) => {
                  const on = hot === n.id || picked === n.id
                  // The node in hand is always named: where the crowd left it
                  // no place, at the one that leaves the frame least.
                  const hit = touched?.has(n.id) ?? false
                  const spot = named.get(n.id) ?? extra.get(n.id) ?? handSpots.get(n.id)
                  if (!spot || hidden(n.id, spot.box)) return null
                  const dim = near !== null && !near.has(n.id)
                  return (
                    <text
                      key={n.id}
                      data-node={n.id}
                      x={n.x + (spot?.dx ?? n.r + 4)}
                      y={n.y + (spot?.dy ?? 3.5 * text)}
                      textAnchor={spot?.anchor ?? 'start'}
                      style={{ fontSize: 10.5 * text, paintOrder: 'stroke' }}
                      strokeWidth={3 / view.k}
                      strokeLinejoin="round"
                      className={cn('stroke-background font-mono', hit ? 'text-foreground' : TONE[n.kind].text, on && 'font-medium', dim ? 'opacity-25' : n.shadow && !on && !hit && 'opacity-50')}
                      fill="currentColor"
                    >
                      {(() => {
                        // A label wider than the room the frame leaves it is cut to fit, not at the edge.
                        if (!spot) return shown(n.label)
                        const x = n.x + spot.dx
                        const room = spot.anchor === 'start' ? frameBox.x1 - x : spot.anchor === 'end' ? x - frameBox.x0 : 2 * Math.min(x - frameBox.x0, frameBox.x1 - x)
                        return cutTo(n.label, Math.floor(room / (CHAR * text)) - 1)
                      })()}
                    </text>
                  )
                })}
              </g>
            </g>
          </svg>
        </div>
        {node && (
          <div ref={pane} style={{ ['--pane-h' as string]: height }} className="flex min-w-0 flex-col gap-2 lg:max-h-[var(--pane-h)] lg:overflow-auto [&>*]:shrink-0">
            <div className="flex flex-col gap-1.5 rounded-md border border-border bg-card/50 px-3 py-2 font-mono text-[11.5px]">
              <div className="flex items-start gap-2">
                <span className={cn('mt-0.5 text-[10px] tracking-[0.1em] uppercase', TONE[node.kind].text)}>{node.kind}</span>
                <span className="min-w-0 flex-1 break-all">{node.id}</span>
                <Copy value={node.id} iconOnly />
              </div>
              <div className="flex items-center gap-2 text-muted-foreground">
                <span>
                  <span className="num text-foreground">{ties.find((t) => t.side === 'in')?.list.length ?? 0}</span> in · <span className="num text-foreground">{ties.find((t) => t.side === 'out')?.list.length ?? 0}</span> out · <span className="num text-foreground">{node.degree}</span> relations
                </span>
                {onExpand && (
                  <button type="button" onClick={() => grow(node.id)} className={cn('ml-auto h-6 rounded-sm border border-border px-2 hover:border-primary hover:text-foreground [@media(pointer:coarse)]:h-11', touch)}>
                    expand
                  </button>
                )}
              </div>
            </div>
            {place?.(node.id)}
          </div>
        )}
      </div>
    </div>
  )
}
