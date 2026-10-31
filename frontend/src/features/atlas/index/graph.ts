// A result with src_id, dst_id and rel columns is a set of edges. This lays
// them out left to right: a node's column is the longest chain of edges that
// reaches it from a node nothing points at, so a handler sits left of what
// it calls and a sink at the far right. Rows inside a column follow the
// average row of the nodes pointing in, which keeps most edges short and
// uncrossed. Cycles (a neighbourhood walked both ways) are broken by placing
// what is left one column right of its first placed parent.

export type Kind = 'function' | 'sink' | 'var' | 'host' | 'subdomain' | 'resource' | 'body' | 'repo' | 'finding' | 'literal' | 'node'
export type GNode = { id: string; kind: Kind; label: string; place: string; detail: string; col: number; row: number }
export type GEdge = { src: string; dst: string; rel: string; confidence?: number }
export type Layout = { nodes: GNode[]; edges: GEdge[]; cols: number; rows: number; dropped: number }

const PREFIX: [string, Kind][] = [
  ['fn:', 'function'],
  ['sink:', 'sink'],
  ['host:', 'host'],
  ['subdomain:', 'subdomain'],
  ['resource:', 'resource'],
  ['body:', 'body'],
  ['repo:', 'repo'],
  ['finding:', 'finding'],
  ['literal:', 'literal'],
]

export function kindOf(id: string): Kind {
  for (const [p, k] of PREFIX) if (id.startsWith(p)) return k
  return id.includes('#') ? 'var' : 'node'
}

const squash = (s: string, n = 34) => {
  const t = s.replace(/\s+/g, ' ').trim()
  return t.length > n ? `${t.slice(0, n - 1)}…` : t
}

// fn ids are repo:path:name:line, repo being owner/name.
function fnParts(fnId: string): { repo: string; path: string; name: string; line: string } {
  const parts = fnId.split(':')
  const line = parts.pop() ?? ''
  const name = parts.pop() ?? ''
  const repo = parts.shift() ?? ''
  return { repo, path: parts.join(':'), name, line }
}

export function labelOf(id: string): { label: string; place: string; detail: string } {
  const kind = kindOf(id)
  const rest = id.slice(id.indexOf(':') + 1)
  switch (kind) {
    case 'function': {
      const f = fnParts(rest)
      return { label: f.name === '<anon>' ? `<anon>:${f.line}` : f.name, place: `${f.path.split('/').pop()}:${f.line}`, detail: `${f.repo} ${f.path}:${f.line}` }
    }
    case 'sink': {
      const cut = rest.indexOf('#')
      const callee = cut === -1 ? rest : rest.slice(cut + 1)
      return { label: squash(callee), place: '', detail: callee }
    }
    case 'var': {
      const cut = id.lastIndexOf('#')
      const f = fnParts(id.slice(0, cut))
      return { label: id.slice(cut + 1), place: `${f.name} ${f.path.split('/').pop()}`, detail: `${f.name} ${f.path}:${f.line}` }
    }
    case 'resource': {
      const tail = rest.split(/[/:]/).filter(Boolean).pop() ?? rest
      return { label: squash(tail), place: '', detail: rest }
    }
    case 'body':
      return { label: rest.slice(0, 12), place: '', detail: rest }
    case 'literal': {
      const parts = rest.split(':')
      const line = parts.pop() ?? ''
      const path = parts.slice(1).join(':')
      return { label: `${path.split('/').pop()}:${line}`, place: parts[0] ?? '', detail: rest }
    }
    case 'finding': {
      const parts = rest.split(':')
      return { label: parts.pop() ?? rest, place: '', detail: rest }
    }
    case 'node':
      return { label: squash(id), place: '', detail: id }
    default:
      return { label: squash(rest), place: '', detail: rest }
  }
}

export function layout(rows: Record<string, unknown>[], cols: { src: string; dst: string; rel: string }, max = 160): Layout {
  const seen = new Set<string>()
  const edges: GEdge[] = []
  const ids: string[] = []
  let dropped = 0
  for (const r of rows) {
    const src = r[cols.src]
    const dst = r[cols.dst]
    if (typeof src !== 'string' || typeof dst !== 'string' || !src || !dst) continue
    const add = [src, dst].filter((x) => !seen.has(x))
    if (ids.length + add.length > max) {
      dropped += add.length
      continue
    }
    for (const x of add) {
      seen.add(x)
      ids.push(x)
    }
    if (src === dst) continue
    const c = Number(r.confidence)
    edges.push({ src, dst, rel: String(r[cols.rel] ?? ''), ...(r.confidence !== undefined && r.confidence !== null && Number.isFinite(c) ? { confidence: c } : {}) })
  }

  const out = new Map<string, string[]>()
  const into = new Map<string, string[]>()
  for (const id of ids) {
    out.set(id, [])
    into.set(id, [])
  }
  for (const e of edges) {
    out.get(e.src)!.push(e.dst)
    into.get(e.dst)!.push(e.src)
  }

  // Longest path from the sources, Kahn's order.
  const col = new Map<string, number>()
  const indeg = new Map(ids.map((id) => [id, into.get(id)!.length]))
  const queue = ids.filter((id) => indeg.get(id) === 0)
  for (const id of queue) col.set(id, 0)
  while (queue.length) {
    const u = queue.shift()!
    for (const v of out.get(u)!) {
      col.set(v, Math.max(col.get(v) ?? 0, col.get(u)! + 1))
      indeg.set(v, indeg.get(v)! - 1)
      if (indeg.get(v) === 0) queue.push(v)
    }
  }
  // What a cycle kept back goes one column right of its first placed parent.
  let left = ids.filter((id) => !col.has(id))
  while (left.length) {
    const next: string[] = []
    for (const id of left) {
      const placed = into.get(id)!.filter((p) => col.has(p))
      if (placed.length) col.set(id, Math.min(...placed.map((p) => col.get(p)!)) + 1)
      else next.push(id)
    }
    if (next.length === left.length) {
      col.set(next[0]!, 0)
      next.shift()
    }
    left = next
  }

  const byCol = new Map<number, string[]>()
  for (const id of ids) byCol.set(col.get(id)!, [...(byCol.get(col.get(id)!) ?? []), id])
  const order = [...byCol.keys()].sort((a, b) => a - b)
  const row = new Map<string, number>()
  const label = new Map(ids.map((id) => [id, labelOf(id)]))
  // A node wants the average row of its neighbours on one side; nodes keep
  // that order and are pushed down only as far as the one above needs. Down
  // from the parents, then the first column back up to its children, then
  // down once more, so sources sit beside what they reach.
  const place = (list: string[], side: Map<string, string[]>) => {
    const want = new Map<string, number>()
    for (const id of list) {
      const ns = side.get(id)!.filter((p) => row.has(p))
      want.set(id, ns.length ? ns.reduce((s, p) => s + row.get(p)!, 0) / ns.length : Number.POSITIVE_INFINITY)
    }
    list.sort((a, b) => want.get(a)! - want.get(b)! || label.get(a)!.label.localeCompare(label.get(b)!.label))
    let cur = -1
    for (const id of list) {
      const w = want.get(id)!
      cur = Number.isFinite(w) ? Math.max(cur + 1, Math.round(w)) : cur + 1
      row.set(id, cur)
    }
  }
  for (const c of order) place(byCol.get(c)!, into)
  if (order.length > 1) {
    place(byCol.get(order[0]!)!, out)
    const least = Math.min(...ids.map((id) => row.get(id)!))
    for (const id of ids) row.set(id, row.get(id)! - least)
    for (const c of order.slice(1)) place(byCol.get(c)!, into)
  }
  // Rows no column uses are dropped, so clusters sit close.
  const used = [...new Set(row.values())].sort((a, b) => a - b)
  const dense = new Map(used.map((r, i) => [r, i]))
  for (const id of ids) row.set(id, dense.get(row.get(id)!)!)

  const nodes: GNode[] = ids.map((id) => ({ id, kind: kindOf(id), ...label.get(id)!, col: order.indexOf(col.get(id)!), row: row.get(id)! }))
  return { nodes, edges, cols: order.length, rows: nodes.length ? Math.max(...nodes.map((n) => n.row)) + 1 : 0, dropped }
}
