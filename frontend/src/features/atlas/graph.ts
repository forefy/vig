import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY } from 'd3-force'

// A result whose rows have src_id and dst_id is a set of edges, and can be
// drawn as a graph: a network, laid out by forces, the result's own edges
// lit and, around them, the other relations the index holds for the same
// nodes as shadows, so a path reads inside its neighbourhood. A node's
// circle grows with the relations it has. Nothing here knows what the
// ids mean beyond their prefix, which picks a tone; a row's `src_label`
// or `dst_label`, when the statement gives one, names the node instead of
// its id.

export type Kind = 'function' | 'sink' | 'call' | 'var' | 'ret' | 'host' | 'subdomain' | 'resource' | 'body' | 'repo' | 'literal' | 'entry' | 'value' | 'param' | 'node'

export type Point = { x: number; y: number }
// A node from the result, or a shadow: one the result does not name but
// a relation reaches. `into` and `out` count the result's edges, `degree`
// every relation drawn, and `r` is the circle's radius by that degree.
export type GraphNode = { id: string; kind: Kind; label: string; degree: number; into: number; out: number; shadow: boolean; x: number; y: number; r: number; fx?: number | null; fy?: number | null }
export type GraphLink = { source: string; target: string; rel: string; confidence?: number; shadow: boolean; points: Point[] }
// key names the picture by the result's content, so a view kept for it
// survives the same rows run again and the shadows arriving around them.
// The result's nodes are laid out first and keep their places when the
// shadows come, so the picture's extent starts at `left` and `top`, below
// zero where the shadows reach past the result.
// `fit` is the result's own extent from the origin, which the view frames
// and which the shadows do not change; `named` are the nodes labelled at
// rest.
export type Graph = { nodes: GraphNode[]; links: GraphLink[]; dropped: number; left: number; top: number; width: number; height: number; fit: { w: number; h: number }; named: Map<string, Spot>; key: string }

export const MAX_NODES = 400
// Shadows drawn around a result, at most, and around any one of its nodes,
// so a hub does not take the whole neighbourhood for itself.
export const MAX_SHADOWS = 300
export const MAX_SHADOWS_EACH = 24
// The smallest circle, and the width of one label glyph.
export const R = 4
export const CHAR = 6.4

// radiusOf is a circle by its relations: a node with one is small, a hub
// of a hundred about three times as wide.
export const R_MAX = 14
// stepsOf gives each node its step along the edges: 0 where no edge
// arrives, else one more than the furthest node that reaches it. A cycle is
// cut where it closes, so every step is finite; steps are at most the
// number of nodes.
export function stepsOf(nodes: { id: string }[], edges: { source: string; target: string }[]): Map<string, number> {
  const step = new Map(nodes.map((n) => [n.id, 0]))
  const into = new Map<string, number>()
  const out = new Map<string, string[]>()
  for (const e of edges) {
    if (e.source === e.target || !step.has(e.source) || !step.has(e.target)) continue
    into.set(e.target, (into.get(e.target) ?? 0) + 1)
    const list = out.get(e.source)
    if (list) list.push(e.target)
    else out.set(e.source, [e.target])
  }
  // Kahn's order: the nodes no edge reaches first; what a cycle leaves
  // behind is released one at a time, its weakest-reached node first.
  const queue = nodes.filter((n) => !into.get(n.id)).map((n) => n.id)
  const done = new Set<string>()
  const left = new Map(into)
  while (done.size < step.size) {
    if (!queue.length) {
      const next = nodes.filter((n) => !done.has(n.id)).sort((a, b) => (left.get(a.id) ?? 0) - (left.get(b.id) ?? 0) || (a.id < b.id ? -1 : 1))[0]
      if (!next) break
      queue.push(next.id)
    }
    const id = queue.shift()!
    if (done.has(id)) continue
    done.add(id)
    for (const t of out.get(id) ?? []) {
      if (done.has(t)) continue
      step.set(t, Math.max(step.get(t)!, step.get(id)! + 1))
      const n = (left.get(t) ?? 1) - 1
      left.set(t, n)
      if (n === 0) queue.push(t)
    }
  }
  return step
}

// edgeNode is a node on the rim: one edge of the result or none.
const edgeNode = (n: GraphNode) => n.into + n.out <= 1

export const radiusOf = (degree: number, shadow: boolean) => Math.min(R_MAX, (shadow ? R - 1 : R) + Math.sqrt(Math.max(0, degree)) * 1.3)
export const LABEL_MAX = 36

// A result is drawn by its edges, when it names a source and a destination,
// or by its nodes alone, when a column names functions, calls, entries or
// hosts: then the relations around them are the picture.
export type EdgeCols = { src: string; dst: string; rel?: string }
// The columns whose values, joined by colons, make a node id under the prefix.
export type NodeCols = { node: string[]; prefix: string }
export type Cols = EdgeCols | NodeCols

// edgeColumns says whether a result is a set of edges: it names its source
// and destination columns, and the relation when there is one.
export function edgeColumns(columns: string[]): EdgeCols | null {
  const has = (c: string) => columns.includes(c)
  if (has('src_id') && has('dst_id')) return { src: 'src_id', dst: 'dst_id', ...(has('rel') ? { rel: 'rel' } : {}) }
  if (has('src') && has('dst')) return { src: 'src', dst: 'dst', ...(has('kind') ? { rel: 'kind' } : {}) }
  return null
}

// nodeColumn says which columns of a result name nodes, by the first of
// these shapes it carries: an id column, or the columns an id is made of
// (a function by its repository, path, name and line; a call by its
// repository, path, line and column).
const NODE_COLS: [string[], string][] = [
  [['fn_id'], 'fn:'],
  [['call_id'], 'call:'],
  [['entry_id'], 'entry:'],
  [['repo', 'path', 'qualname', 'line_start'], 'fn:'],
  [['repo', 'path', 'line', 'col'], 'call:'],
  [['host'], 'host:'],
]
export function nodeColumn(columns: string[]): NodeCols | null {
  for (const [node, prefix] of NODE_COLS) if (node.every((c) => columns.includes(c))) return { node, prefix }
  return null
}

export const drawColumns = (columns: string[]): Cols | null => edgeColumns(columns) ?? nodeColumn(columns)

// idsOf lists the ids a row names under the columns.
export function idsOf(r: Record<string, unknown>, cols: Cols): string[] {
  if ('node' in cols) {
    const parts = cols.node.map((c) => r[c])
    if (parts.some((v) => v === null || v === undefined || v === '')) return []
    return [cols.prefix + parts.map(String).join(':')]
  }
  const src = r[cols.src]
  const dst = r[cols.dst]
  return [src, dst].filter((x): x is string => typeof x === 'string' && x !== '')
}

const PREFIX: [string, Kind][] = [
  ['fn:', 'function'],
  ['sink:', 'sink'],
  ['call:', 'call'],
  ['var:', 'var'],
  ['ret:', 'ret'],
  ['host:', 'host'],
  ['subdomain:', 'subdomain'],
  ['resource:', 'resource'],
  ['body:', 'body'],
  ['repo:', 'repo'],
  ['literal:', 'literal'],
  ['entry:', 'entry'],
  ['value:', 'value'],
  ['param:', 'param'],
]

export function kindOf(id: string): Kind {
  for (const [p, k] of PREFIX) if (id.startsWith(p)) return k
  return 'node'
}

// labelOf is the short name of an id: a function by its name and line, a
// variable by its name, a call or a sink by its callee and the line it is
// on, a host by itself.
export function labelOf(id: string): string {
  const kind = kindOf(id)
  const rest = id.slice(id.indexOf(':') + 1)
  switch (kind) {
    case 'function': {
      const parts = rest.split(':')
      return parts.length >= 2 ? `${parts[parts.length - 2]}:${parts[parts.length - 1]}` : rest
    }
    case 'var': {
      const cut = rest.lastIndexOf('#')
      return cut >= 0 ? rest.slice(cut + 1) : rest
    }
    case 'call':
    case 'sink': {
      // A callee carries no `@`; a path may (`@scope/pkg`).
      const at = rest.indexOf('@')
      if (at > 0) {
        const line = rest.slice(rest.lastIndexOf(':') + 1)
        return /^\d+$/.test(line) ? `${rest.slice(0, at)}:${line}` : rest.slice(0, at)
      }
      const cut = rest.lastIndexOf('#')
      if (cut >= 0) return rest.slice(cut + 1)
      const parts = rest.split(':')
      return parts.length >= 2 ? `${parts[parts.length - 2]}:${parts[parts.length - 1]}` : rest
    }
    case 'body':
      return rest.slice(0, 10)
    case 'literal':
      return rest.split(':').slice(-2).join(':')
    case 'entry':
      return rest.slice(0, 16)
    case 'value':
      return rest.slice(0, 8)
    case 'param':
      return rest
    default:
      return rest.length > 40 ? `${rest.slice(0, 39)}…` : rest
  }
}

// shown is a label cut to fit, its head and its tail kept, since the tail
// (a line, a path's last part) is what tells two long labels apart.
// cutTo is a label cut to the room it has, `room` glyphs at most: its
// start and its end kept, an ellipsis between, so the line number stays.
export const cutTo = (label: string, room: number) => {
  const text = shown(label)
  if (text.length <= room) return text
  if (room < 4) return text.slice(0, Math.max(1, room - 1)) + '…'
  const tail = Math.min(8, Math.floor((room - 1) / 2))
  return `${text.slice(0, room - 1 - tail)}…${text.slice(-tail)}`
}
export const shown = (label: string) => (label.length > LABEL_MAX ? `${label.slice(0, LABEL_MAX - 13)}…${label.slice(-12)}` : label)

// Where a label sits against its circle's centre, as the text is drawn
// (its anchor point and alignment), and the box it takes, in graph px.
const LABEL_H = 13
export type Box = { x0: number; y0: number; x1: number; y1: number }
export type Spot = { dx: number; dy: number; anchor: 'start' | 'end' | 'middle'; box: Box }
// spots are the places a label may take, in the order tried: after the
// circle, before it, under it, over it, then after or before it a line up
// or a line down.
export function spots(n: { x: number; y: number; r: number; label: string }, s = 1): Spot[] {
  // The text and the halo it is drawn on.
  const w = (shown(n.label).length * CHAR + 4) * s
  const h = (LABEL_H + 2) * s
  const base = 3.5 * s
  return [
    { dx: n.r + 4, dy: base, anchor: 'start', box: { x0: n.x + n.r + 3, y0: n.y - h / 2 - 1, x1: n.x + n.r + 5 + w, y1: n.y + h / 2 } },
    { dx: -(n.r + 4), dy: base, anchor: 'end', box: { x0: n.x - n.r - 5 - w, y0: n.y - h / 2 - 1, x1: n.x - n.r - 3, y1: n.y + h / 2 } },
    { dx: 0, dy: n.r + h - 1, anchor: 'middle', box: { x0: n.x - w / 2 - 1, y0: n.y + n.r + 1, x1: n.x + w / 2 + 1, y1: n.y + n.r + h + 1 } },
    { dx: 0, dy: -(n.r + 4), anchor: 'middle', box: { x0: n.x - w / 2 - 1, y0: n.y - n.r - h - 1, x1: n.x + w / 2 + 1, y1: n.y - n.r - 1 } },
    ...[-h, h].flatMap((up): Spot[] => [
      { dx: n.r + 2, dy: base + up, anchor: 'start', box: { x0: n.x + n.r + 1, y0: n.y + up - h / 2 - 1, x1: n.x + n.r + 3 + w, y1: n.y + up + h / 2 } },
      { dx: -(n.r + 2), dy: base + up, anchor: 'end', box: { x0: n.x - n.r - 3 - w, y0: n.y + up - h / 2 - 1, x1: n.x - n.r - 1, y1: n.y + up + h / 2 } },
    ]),
  ]
}
const circleBox = (n: { x: number; y: number; r: number }): Box => ({ x0: n.x - n.r, y0: n.y - n.r, x1: n.x + n.r, y1: n.y + n.r })
const meets = (a: Box, b: Box) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1

// placeLabels names the nodes in the order given while each label lands
// clear of the labels already placed and of every circle but its own, so
// a crowded picture names its weightiest nodes and leaves the rest to the
// pointer. `s` is the label's size against the picture: above 1 when the
// picture is drawn small and its labels are kept readable. A result's node
// keeps its label inside `bounds`, the extent the view frames, so no label
// is cut at the frame's edge. A node in `loose` (a hub, the node in hand
// and its neighbours) may lay its label over circles, never over another
// label: it is drawn on a halo. Every label, a shadow's too, stays inside
// `seen`, the part of the picture the frame shows, so none is cut at the
// frame's edge. The circles and the labels are kept in a grid of cells.
export function placeLabels(nodes: GraphNode[], order: string[], s = 1, bounds?: Box, loose?: Set<string>, seen?: Box): Map<string, Spot> {
  const CELL = 48 * s
  const cells = new Map<string, { box: Box; id: string; label: boolean }[]>()
  const keys = (b: Box) => {
    const out: string[] = []
    for (let x = Math.floor(b.x0 / CELL); x <= Math.floor(b.x1 / CELL); x++) for (let y = Math.floor(b.y0 / CELL); y <= Math.floor(b.y1 / CELL); y++) out.push(`${x},${y}`)
    return out
  }
  const add = (box: Box, id: string, label: boolean) => {
    for (const k of keys(box)) {
      const list = cells.get(k)
      if (list) list.push({ box, id, label })
      else cells.set(k, [{ box, id, label }])
    }
  }
  for (const n of nodes) add(circleBox(n), n.id, false)
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const named = new Map<string, Spot>()
  const clear = (id: string, box: Box, over: boolean) => {
    for (const k of keys(box)) for (const other of cells.get(k) ?? []) if (other.id !== id && (other.label || !over) && meets(box, other.box)) return false
    return true
  }
  for (const id of order) {
    const n = byId.get(id)
    if (!n || named.has(id)) continue
    const within = (b: Box, c: Box) => b.x0 >= c.x0 && b.y0 >= c.y0 && b.x1 <= c.x1 && b.y1 <= c.y1
    const inside = (b: Box) => (!bounds || n.shadow || within(b, bounds)) && (!seen || within(b, seen))
    const over = loose?.has(id) ?? false
    const spot = spots(n, s).find((p) => inside(p.box) && clear(id, p.box, false)) ?? (over ? spots(n, s).find((p) => inside(p.box) && clear(id, p.box, true)) : undefined)
    if (!spot) continue
    named.set(id, spot)
    add(spot.box, `${id}\u0000label`, true)
  }
  return named
}

// byWeight is the order a picture names its nodes in: the result's own by
// their relations, then the shadows by theirs.
export const byWeight = (nodes: GraphNode[]) =>
  [...nodes].sort((a, b) => Number(a.shadow) - Number(b.shadow) || b.degree - a.degree || (a.id < b.id ? -1 : 1)).map((n) => n.id)

// HUBS of the result's nodes, the weightiest with three relations or more,
// are named even where the picture is crowded.
export const HUBS = 10
export const hubsOf = (nodes: GraphNode[]) =>
  new Set(
    byWeight(nodes.filter((n) => !n.shadow && n.degree >= 3))
      .slice(0, HUBS),
  )

// pathOf draws an edge through the points the layout gave it: a straight
// line between two, else each leg a curve that leaves and arrives level.
export function pathOf(points: Point[]): string {
  if (points.length === 0) return ''
  if (points.length === 2) return `M ${points[0]!.x} ${points[0]!.y} L ${points[1]!.x} ${points[1]!.y}`
  const [first, ...rest] = points
  let d = `M ${first!.x} ${first!.y}`
  let prev = first!
  for (const p of rest) {
    const dx = (p.x - prev.x) / 2
    d += ` C ${prev.x + dx} ${prev.y}, ${p.x - dx} ${p.y}, ${p.x} ${p.y}`
    prev = p
  }
  return d
}

// layout builds the graph from the result's rows, adds the shadows that
// touch its nodes, and lays the network out by forces: links pull, nodes
// push, the whole leans to its centre. Rows beyond the node cap are
// counted, not drawn. The layout is deterministic: every node starts on a
// spiral by its order, and the forces settle the same way every time.
export function layout(rows: Record<string, unknown>[], cols: Cols, shadows: Record<string, unknown>[] = [], max = MAX_NODES): Graph {
  const nodes = new Map<string, GraphNode>()
  const links: GraphLink[] = []
  const seen = new Set<string>()
  let dropped = 0
  const make = (id: string, shadow: boolean): GraphNode => ({ id, kind: kindOf(id), label: labelOf(id), degree: 0, into: 0, out: 0, shadow, x: 0, y: 0, r: R })
  for (const r of rows) {
    if ('node' in cols) {
      const [id] = idsOf(r, cols)
      if (!id || nodes.has(id)) continue
      if (nodes.size + 1 > max) {
        dropped++
        continue
      }
      const made = make(id, false)
      // A row may name its node better than its id: a label column, or a
      // request by its method and path.
      if (typeof r.label === 'string' && r.label) made.label = r.label
      else if (cols.prefix === 'entry:' && typeof r.method === 'string' && typeof r.path === 'string' && r.method) made.label = `${r.method} ${r.path}`
      nodes.set(id, made)
      continue
    }
    const src = r[cols.src]
    const dst = r[cols.dst]
    if (typeof src !== 'string' || typeof dst !== 'string' || !src || !dst) continue
    const add = [src, dst].filter((x) => !nodes.has(x))
    if (nodes.size + add.length > max) {
      dropped++
      continue
    }
    for (const x of add) nodes.set(x, make(x, false))
    if (typeof r.src_label === 'string' && r.src_label) nodes.get(src)!.label = r.src_label
    if (typeof r.dst_label === 'string' && r.dst_label) nodes.get(dst)!.label = r.dst_label
    const rel = cols.rel ? String(r[cols.rel] ?? '') : ''
    const key = `${src}\u0000${dst}\u0000${rel}`
    if (src === dst || seen.has(key)) continue
    seen.add(key)
    nodes.get(src)!.degree++
    nodes.get(src)!.out++
    nodes.get(dst)!.degree++
    nodes.get(dst)!.into++
    const c = Number(r.confidence)
    links.push({ source: src, target: dst, rel, shadow: false, points: [], ...(r.confidence !== undefined && r.confidence !== null && Number.isFinite(c) ? { confidence: c } : {}) })
  }
  const pathKey = `${[...nodes.keys()].join('\u0001')}\u0002${links.map((l) => `${l.source}>${l.target}:${l.rel}`).join('\u0001')}`
  // The shadows: a relation the result left out, between a node it has
  // and one it may not. The pair of a node to itself and a relation already
  // lit are not shadows.
  let shade = 0
  const each = new Map<string, number>()
  for (const r of shadows) {
    if (shade >= MAX_SHADOWS) break
    const src = r.src_id
    const dst = r.dst_id
    if (typeof src !== 'string' || typeof dst !== 'string' || !src || !dst || src === dst) continue
    const lit = [src, dst].filter((x) => nodes.has(x) && !nodes.get(x)!.shadow)
    if (!lit.length) continue
    if (lit.some((x) => (each.get(x) ?? 0) >= MAX_SHADOWS_EACH)) continue
    const rel = typeof r.rel === 'string' ? r.rel : ''
    const key = `${src}\u0000${dst}\u0000${rel}`
    if (seen.has(key)) continue
    const add = [src, dst].filter((x) => !nodes.has(x))
    if (nodes.size + add.length > max) continue
    for (const x of add) nodes.set(x, make(x, true))
    if (typeof r.src_label === 'string' && r.src_label && nodes.get(src)!.shadow) nodes.get(src)!.label = r.src_label
    if (typeof r.dst_label === 'string' && r.dst_label && nodes.get(dst)!.shadow) nodes.get(dst)!.label = r.dst_label
    seen.add(key)
    shade++
    for (const x of lit) each.set(x, (each.get(x) ?? 0) + 1)
    nodes.get(src)!.degree++
    nodes.get(dst)!.degree++
    const c = Number(r.confidence)
    links.push({ source: src, target: dst, rel, shadow: true, points: [], ...(r.confidence !== undefined && r.confidence !== null && Number.isFinite(c) ? { confidence: c } : {}) })
  }
  const list = [...nodes.values()]
  const key = pathKey
  if (!list.length) return { nodes: [], links: [], dropped, left: 0, top: 0, width: 0, height: 0, fit: { w: 0, h: 0 }, named: new Map(), key }
  for (const n of list) n.r = radiusOf(n.degree, n.shadow)
  const lit = list.filter((n) => !n.shadow)
  const shaded = list.filter((n) => n.shadow)
  type SimLink = { source: string; target: string; shadow: boolean }

  // The simulation runs to rest here, not on screen, so the picture opens
  // still. The result's nodes settle first, on their own, from a spiral by
  // their order and by their own relations, so they land in the same
  // places whether the shadows have come or not.
  const side = Math.max(360, Math.sqrt(lit.length) * 110)
  // Each node of the result has a step: how far along the result's edges
  // it sits from where they start. A result that flows (a read reaching a
  // sink, a page and what it triggers) is pulled gently left to right by
  // step, so it reads from its start to its end; one with no steps to
  // speak of keeps its plain network.
  const step = stepsOf(lit, links.filter((l) => !l.shadow))
  // Only a result with paths of two steps or more flows; a fan of callers
  // into a few hubs is one step, and pulled into two columns it is a wall.
  const deepest = Math.max(0, ...step.values()) >= 2 ? Math.max(...step.values()) : 0
  const STEP_GAP = 150
  const stepX = (n: GraphNode) => side / 2 + ((step.get(n.id) ?? 0) - deepest / 2) * STEP_GAP
  lit.forEach((n, i) => {
    const a = i * 2.399963
    const d = 14 * Math.sqrt(i)
    n.x = (deepest > 0 ? stepX(n) : side / 2) + d * Math.cos(a)
    n.y = side / 2 + d * Math.sin(a)
  })
  const own = (n: GraphNode) => radiusOf(n.into + n.out, false)
  // The cooling is fitted to the picture: a few hundred steps for a large
  // one is more than the eye can tell from settled.
  const cool = (ticks: number) => 1 - Math.pow(0.001, 1 / ticks)
  const firstTicks = Math.min(170, 70 + lit.length)
  const first = forceSimulation(lit)
    .alphaDecay(cool(firstTicks))
    .force('link', forceLink<GraphNode, SimLink>(links.filter((l) => !l.shadow).map((l) => ({ source: l.source, target: l.target, shadow: false }))).id((n) => n.id).distance(92).strength(0.7))
    // An edge node, one edge of the result or none, is pushed harder, so
    // the rim of the picture spreads out instead of clumping on its hub. The
    // result's own edges decide it, so the shadows never move these nodes.
    .force('charge', forceManyBody<GraphNode>().strength((n) => (edgeNode(n) ? -340 : -170)).theta(1).distanceMax(520))
    .force('collide', forceCollide<GraphNode>().radius((n) => own(n) + (edgeNode(n) ? 22 : 16)).strength(0.8))
    .force('x', deepest > 0 ? forceX<GraphNode>(stepX).strength(0.22) : forceX<GraphNode>(side / 2).strength(0.05))
    .force('y', forceY<GraphNode>(side / 2).strength(0.05))
    .stop()
  for (let i = 0; i < firstTicks; i++) first.tick()

  // Then the shadows settle around them, the result's nodes held where
  // they are; each shadow starts beside the node of the result it touches.
  if (shaded.length) {
    for (const n of lit) {
      n.fx = n.x
      n.fy = n.y
    }
    const anchor = new Map<string, GraphNode>()
    for (const l of links) {
      if (!l.shadow) continue
      const s = nodes.get(l.source)!
      const t = nodes.get(l.target)!
      if (s.shadow && !t.shadow && !anchor.has(s.id)) anchor.set(s.id, t)
      if (t.shadow && !s.shadow && !anchor.has(t.id)) anchor.set(t.id, s)
    }
    shaded.forEach((n, i) => {
      const at = anchor.get(n.id) ?? lit[0]!
      const a = i * 2.399963
      n.x = at.x + 40 * Math.cos(a)
      n.y = at.y + 40 * Math.sin(a)
    })
    const cx = lit.reduce((t, n) => t + n.x, 0) / lit.length
    const cy = lit.reduce((t, n) => t + n.y, 0) / lit.length
    const secondTicks = Math.min(110, 50 + Math.round(shaded.length / 3))
    const second = forceSimulation(list)
      .alpha(0.6)
      .alphaDecay(cool(secondTicks))
      .force('link', forceLink<GraphNode, SimLink>(links.map((l) => ({ source: l.source, target: l.target, shadow: l.shadow }))).id((n) => n.id).distance((l) => (l.shadow ? 84 : 92)).strength((l) => (l.shadow ? 0.25 : 0.7)))
      .force('charge', forceManyBody<GraphNode>().strength((n) => (n.shadow ? -150 : edgeNode(n) ? -340 : -170)).theta(1).distanceMax(380))
      .force('collide', forceCollide<GraphNode>().radius((n) => n.r + (n.shadow ? 11 : edgeNode(n) ? 22 : 16)).strength(0.8))
      .force('x', forceX<GraphNode>(cx).strength(0.03))
      .force('y', forceY<GraphNode>(cy).strength(0.03))
      .stop()
    for (let i = 0; i < secondTicks; i++) second.tick()
    for (const n of lit) {
      delete n.fx
      delete n.fy
    }
  }

  // The picture takes its origin from the result's nodes, so they keep
  // their coordinates when the shadows come, with room for the labels on
  // the right; the shadows may reach above or left of it.
  // The origin and the result's extent are measured with the largest a
  // circle can grow, so the shadows, which grow the circles they touch,
  // move neither.
  const margin = 24
  let litX = Infinity
  let litY = Infinity
  for (const n of lit) {
    litX = Math.min(litX, n.x - R_MAX)
    litY = Math.min(litY, n.y - R_MAX)
  }
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  let labelRight = -Infinity
  for (const n of list) {
    n.x = Math.round((n.x - litX + margin) * 10) / 10
    n.y = Math.round((n.y - litY + margin) * 10) / 10
    minX = Math.min(minX, n.x - n.r)
    minY = Math.min(minY, n.y - n.r)
    maxX = Math.max(maxX, n.x + n.r)
    maxY = Math.max(maxY, n.y + n.r)
    if (!n.shadow) labelRight = Math.max(labelRight, n.x + n.r + 5 + shown(n.label).length * CHAR)
  }
  const left = Math.min(0, Math.floor(minX - margin))
  const top = Math.min(0, Math.floor(minY - margin))
  const byId = new Map(list.map((n) => [n.id, n]))
  // A line runs from the edge of one circle to the edge of the other.
  for (const l of links) {
    const s = byId.get(l.source)!
    const t = byId.get(l.target)!
    const dx = t.x - s.x
    const dy = t.y - s.y
    const len = Math.hypot(dx, dy) || 1
    const ux = dx / len
    const uy = dy / len
    l.points = [
      { x: Math.round((s.x + ux * (s.r + 1)) * 10) / 10, y: Math.round((s.y + uy * (s.r + 1)) * 10) / 10 },
      { x: Math.round((t.x - ux * (t.r + 2)) * 10) / 10, y: Math.round((t.y - uy * (t.r + 2)) * 10) / 10 },
    ]
  }
  let litRight = 0
  let litBottom = 0
  for (const n of lit) {
    litRight = Math.max(litRight, n.x + R_MAX + 5 + shown(n.label).length * CHAR)
    litBottom = Math.max(litBottom, n.y + R_MAX)
  }
  const fit = { w: Math.ceil(litRight + margin), h: Math.ceil(litBottom + margin) }
  const named = placeLabels(lit, byWeight(lit), 1, { x0: 0, y0: 0, x1: fit.w, y1: fit.h }, hubsOf(lit))
  return { nodes: list, links, dropped, left, top, width: Math.ceil(Math.max(maxX, labelRight) + margin - left), height: Math.ceil(maxY + margin - top), fit, named, key }
}
