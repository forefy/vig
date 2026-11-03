import { describe, expect, it } from 'vitest'
import { byWeight, cutTo, stepsOf, drawColumns, edgeColumns, hubsOf, idsOf, kindOf, labelOf, layout, nodeColumn, pathOf, placeLabels, shown } from './graph'

describe('graph', () => {
  it('draws a result that names its ends', () => {
    expect(edgeColumns(['src_id', 'dst_id', 'rel'])).toEqual({ src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(edgeColumns(['src', 'dst', 'kind'])).toEqual({ src: 'src', dst: 'dst', rel: 'kind' })
    expect(edgeColumns(['repo', 'path'])).toBeNull()
  })
  it('names a node by its id', () => {
    expect(kindOf('fn:acme/app:server/api.go:get:10')).toBe('function')
    expect(labelOf('fn:acme/app:server/api.go:get:10')).toBe('get:10')
    expect(labelOf('var:acme/app:server/api.go:get:10#id')).toBe('id')
    expect(labelOf('call:db.Query@server/db.go:35')).toBe('db.Query:35')
    expect(labelOf('sink:acme/app:f.go:h:1#acme/app:f.go:g:2#exec.Command')).toBe('exec.Command')
    expect(labelOf('host:app.acme.test')).toBe('app.acme.test')
    expect(kindOf('resource:arn:aws:s3:::x')).toBe('resource')
  })
  it('names a picture by its content, the same rows again the same', () => {
    const rows = [{ src_id: 'host:a', dst_id: 'fn:acme/app:f.go:h:1', rel: 'routes' }]
    const a = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    const b = layout(rows.map((r) => ({ ...r })), { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(a).not.toBe(b)
    expect(a.key).toBe(b.key)
    expect(layout([{ src_id: 'host:b', dst_id: 'fn:acme/app:f.go:h:1', rel: 'routes' }], { src: 'src_id', dst: 'dst_id', rel: 'rel' }).key).not.toBe(a.key)
  })
  it('takes a label the statement gives', () => {
    const g = layout([{ src_id: 'host:a', dst_id: 'entry:0a1b2c3d4e5f6071', rel: 'requests', dst_label: 'GET /api/v1/models' }], { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(g.nodes.map((n) => n.label)).toEqual(['a', 'GET /api/v1/models'])
  })
  it('lays every node out inside the frame, sizes it by its relations, and counts what it left out', () => {
    const rows = []
    for (let i = 0; i < 12; i++) rows.push({ src_id: 'host:a', dst_id: `fn:acme/app:f.go:h${i}:1`, rel: 'routes', confidence: i % 2 ? 1 : 0.8 })
    rows.push({ src_id: 'host:a', dst_id: 'host:a', rel: 'self' })
    rows.push({ src_id: 'host:a', dst_id: 'fn:acme/app:f.go:h1:1', rel: 'routes' })
    rows.push({ src_id: 'fn:acme/app:f.go:h1:1', dst_id: 'sink:exec.Command@acme/app:f.go:4', rel: 'sinks' })
    const g = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(g.nodes).toHaveLength(14)
    expect(g.links).toHaveLength(13)
    expect(g.dropped).toBe(0)
    const by = new Map(g.nodes.map((n) => [n.id, n]))
    expect(by.get('host:a')!.degree).toBe(12)
    expect(by.get('host:a')!.out).toBe(12)
    expect(by.get('host:a')!.r).toBeGreaterThan(by.get('sink:exec.Command@acme/app:f.go:4')!.r)
    for (const n of g.nodes) {
      expect(n.shadow).toBe(false)
      expect(n.x - n.r).toBeGreaterThanOrEqual(0)
      expect(n.x + n.r).toBeLessThanOrEqual(g.width)
      expect(n.y - n.r).toBeGreaterThanOrEqual(0)
      expect(n.y + n.r).toBeLessThanOrEqual(g.height)
    }
    // No two circles sit on one another.
    for (const a of g.nodes) for (const b of g.nodes) if (a !== b) expect(Math.hypot(a.x - b.x, a.y - b.y), `${a.id} ${b.id}`).toBeGreaterThan(a.r + b.r)
    // A line runs between the circles' edges, not their centres.
    for (const l of g.links) {
      const s = by.get(l.source)!
      const t = by.get(l.target)!
      expect(l.points).toHaveLength(2)
      expect(Math.hypot(l.points[0]!.x - s.x, l.points[0]!.y - s.y)).toBeLessThan(s.r + 2)
      expect(Math.hypot(l.points[1]!.x - t.x, l.points[1]!.y - t.y)).toBeLessThan(t.r + 3)
    }
    expect(pathOf(g.links[0]!.points)).toMatch(/^M [\d.-]+ [\d.-]+ L [\d.-]+ [\d.-]+$/)
    const small = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' }, [], 5)
    expect(small.nodes.length).toBeLessThanOrEqual(5)
    expect(small.dropped).toBeGreaterThan(0)
  })
  it('lays out the same rows the same way twice', () => {
    const rows = []
    for (let i = 0; i < 9; i++) rows.push({ src_id: `fn:acme/app:f.go:a${i}:1`, dst_id: `fn:acme/app:f.go:b${(i * 7) % 5}:2`, rel: 'calls' })
    const a = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    const b = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    expect(a.nodes.map((n) => [n.id, n.x, n.y])).toEqual(b.nodes.map((n) => [n.id, n.x, n.y]))
  })
})

describe('shadows', () => {
  it('draws the relations around a result faint, named only by the result, and counts them in the key', () => {
    const rows = [{ src_id: 'fn:acme/app:f.go:h:1', dst_id: 'fn:acme/app:f.go:g:2', rel: 'calls' }]
    const around = [
      { src_id: 'fn:acme/app:f.go:g:2', dst_id: 'fn:acme/app:f.go:k:3', rel: 'calls' },
      { src_id: 'fn:acme/app:f.go:x:9', dst_id: 'fn:acme/app:f.go:h:1', rel: 'calls' },
      // Already lit, a self pair, and one that touches nothing the result has: not drawn.
      { src_id: 'fn:acme/app:f.go:h:1', dst_id: 'fn:acme/app:f.go:g:2', rel: 'calls' },
      { src_id: 'fn:acme/app:f.go:g:2', dst_id: 'fn:acme/app:f.go:g:2', rel: 'calls' },
      { src_id: 'fn:acme/app:f.go:p:7', dst_id: 'fn:acme/app:f.go:q:8', rel: 'calls' },
    ]
    const g = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' }, around)
    expect(g.nodes.filter((n) => !n.shadow).map((n) => n.id)).toEqual(['fn:acme/app:f.go:h:1', 'fn:acme/app:f.go:g:2'])
    expect(g.nodes.filter((n) => n.shadow).map((n) => n.id).sort()).toEqual(['fn:acme/app:f.go:k:3', 'fn:acme/app:f.go:x:9'])
    expect(g.links.filter((l) => l.shadow)).toHaveLength(2)
    expect(g.links.filter((l) => !l.shadow)).toHaveLength(1)
    const by = new Map(g.nodes.map((n) => [n.id, n]))
    // The result's own counts stay the result's; relations count the shadows too.
    expect(by.get('fn:acme/app:f.go:g:2')).toMatchObject({ into: 1, out: 0, degree: 2 })
    expect(by.get('fn:acme/app:f.go:h:1')).toMatchObject({ into: 0, out: 1, degree: 2 })
    // The key names the result, so a view kept for it survives the shadows.
    expect(layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' }).key).toBe(g.key)
  })
  it('keeps the result\'s nodes in their places when the shadows come, and every node inside the extent', () => {
    const rows = []
    for (let i = 0; i < 8; i++) rows.push({ src_id: `fn:acme/app:f.go:a${i}:1`, dst_id: `fn:acme/app:f.go:b${i % 3}:2`, rel: 'calls' })
    const around = []
    for (let i = 0; i < 30; i++) around.push({ src_id: `fn:acme/app:f.go:b${i % 3}:2`, dst_id: `fn:acme/app:g.go:s${i}:9`, rel: 'calls' })
    const cols = { src: 'src_id', dst: 'dst_id', rel: 'rel' }
    const alone = layout(rows, cols)
    const shaded = layout(rows, cols, around)
    const at = (g: typeof alone) => g.nodes.filter((n) => !n.shadow).map((n) => [n.id, n.x, n.y])
    expect(at(shaded)).toEqual(at(alone))
    expect(shaded.nodes.filter((n) => n.shadow)).toHaveLength(30)
    expect(alone).toMatchObject({ left: 0, top: 0 })
    for (const n of shaded.nodes) {
      expect(n.x - n.r, n.id).toBeGreaterThanOrEqual(shaded.left)
      expect(n.x + n.r, n.id).toBeLessThanOrEqual(shaded.left + shaded.width)
      expect(n.y - n.r, n.id).toBeGreaterThanOrEqual(shaded.top)
      expect(n.y + n.r, n.id).toBeLessThanOrEqual(shaded.top + shaded.height)
    }
  })
})

describe('node results', () => {
  it('names nodes from an id column or the columns an id is made of', () => {
    expect(nodeColumn(['repo', 'path', 'qualname', 'line_start', 'is_handler'])).toEqual({ node: ['repo', 'path', 'qualname', 'line_start'], prefix: 'fn:' })
    expect(nodeColumn(['repo', 'fn_id'])).toEqual({ node: ['fn_id'], prefix: 'fn:' })
    expect(nodeColumn(['session', 'method', 'host', 'entry_id'])).toEqual({ node: ['entry_id'], prefix: 'entry:' })
    expect(nodeColumn(['host', 'path', 'param', 'n'])).toEqual({ node: ['host'], prefix: 'host:' })
    expect(nodeColumn(['value', 'kind', 'places'])).toBeNull()
    expect(drawColumns(['src_id', 'dst_id', 'fn_id'])).toEqual({ src: 'src_id', dst: 'dst_id' })
    expect(idsOf({ repo: 'acme/app', path: 'f.go', qualname: 'h', line_start: 10 }, { node: ['repo', 'path', 'qualname', 'line_start'], prefix: 'fn:' })).toEqual(['fn:acme/app:f.go:h:10'])
    expect(idsOf({ repo: 'acme/app', path: 'f.go', qualname: null, line_start: 10 }, { node: ['repo', 'path', 'qualname', 'line_start'], prefix: 'fn:' })).toEqual([])
  })
  it('draws a node result as its nodes with the relations around them', () => {
    const rows = [
      { repo: 'acme/app', path: 'f.go', qualname: 'h', line_start: 10 },
      { repo: 'acme/app', path: 'f.go', qualname: 'g', line_start: 20 },
      { repo: 'acme/app', path: 'f.go', qualname: 'h', line_start: 10 },
    ]
    const around = [{ src_id: 'fn:acme/app:f.go:h:10', dst_id: 'fn:acme/app:f.go:k:30', rel: 'calls' }]
    const g = layout(rows, { node: ['repo', 'path', 'qualname', 'line_start'], prefix: 'fn:' }, around)
    expect(g.nodes.filter((n) => !n.shadow).map((n) => n.id)).toEqual(['fn:acme/app:f.go:h:10', 'fn:acme/app:f.go:g:20'])
    expect(g.links.filter((l) => !l.shadow)).toHaveLength(0)
    expect(g.links.filter((l) => l.shadow)).toHaveLength(1)
    expect(g.nodes.find((n) => n.id === 'fn:acme/app:f.go:k:30')!.shadow).toBe(true)
  })
})

describe('labels', () => {
  it('cuts a long label in the middle, keeping the tail that tells it apart, and reads a call past an @ in its path', () => {
    const a = shown('s.getQueryBuilder(db). Insert(s.tablePrefix+"board_members"):513')
    const b = shown('s.getQueryBuilder(db). Insert(s.tablePrefix+"categories"):88')
    expect(a.length).toBeLessThanOrEqual(36)
    expect(a).not.toBe(b)
    expect(a.endsWith(':513')).toBe(true)
    expect(labelOf('call:render@acme/app:webapp/node_modules/@scope/pkg/index.js:41')).toBe('render:41')
  })
  it('names the weightiest nodes first, each label clear of the others and of every other circle', () => {
    const rows = []
    for (let i = 0; i < 60; i++) rows.push({ src_id: `fn:acme/app:f.go:aVeryLongFunctionNameNumber${i}:1`, dst_id: `fn:acme/app:f.go:hub${i % 4}:2`, rel: 'calls' })
    const g = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    for (const s of [1, 2.5]) {
      const named = placeLabels(g.nodes, byWeight(g.nodes), s)
      const placed = g.nodes.filter((n) => named.has(n.id))
      expect(placed.length).toBeGreaterThan(0)
      for (const a of placed) {
        const ba = named.get(a.id)!.box
        for (const b of placed) {
          if (a === b) continue
          const bb = named.get(b.id)!.box
          expect(ba.x0 < bb.x1 && bb.x0 < ba.x1 && ba.y0 < bb.y1 && bb.y0 < ba.y1, `${a.id} over ${b.id}`).toBe(false)
        }
        for (const c of g.nodes) {
          if (c === a) continue
          expect(ba.x0 < c.x + c.r && c.x - c.r < ba.x1 && ba.y0 < c.y + c.r && c.y - c.r < ba.y1, `${a.id} over the circle of ${c.id}`).toBe(false)
        }
      }
      // The hubs, with the most relations, are named before the leaves.
      for (let i = 0; i < 4; i++) expect(named.has(`fn:acme/app:f.go:hub${i}:2`)).toBe(true)
    }
    // Drawn small, the labels are larger against the picture and fewer fit.
    expect(placeLabels(g.nodes, byWeight(g.nodes), 2.5).size).toBeLessThan(placeLabels(g.nodes, byWeight(g.nodes), 1).size)
  })
  it('takes a node\'s name from its row: a request by its method and path, a route by its label', () => {
    const entries = layout([{ entry_id: 'a2f1c0e406db511e', method: 'GET', path: '/api/v1/models' }], { node: ['entry_id'], prefix: 'entry:' })
    expect(entries.nodes[0]!.label).toBe('GET /api/v1/models')
    const routes = layout([{ fn_id: 'acme/app:f.go:h:10', method: 'POST', path: 'f.go', label: 'POST /api/run' }], { node: ['fn_id'], prefix: 'fn:' })
    expect(routes.nodes[0]!.label).toBe('POST /api/run')
    const fns = layout([{ fn_id: 'acme/app:f.go:h:10', method: 'POST', path: 'f.go' }], { node: ['fn_id'], prefix: 'fn:' })
    expect(fns.nodes[0]!.label).toBe('h:10')
  })
  it('keeps the result\'s extent when the shadows come', () => {
    const rows = [{ src_id: 'fn:acme/app:f.go:h:1', dst_id: 'fn:acme/app:f.go:g:2', rel: 'calls' }]
    const around = []
    for (let i = 0; i < 12; i++) around.push({ src_id: 'fn:acme/app:f.go:g:2', dst_id: `fn:acme/app:g.go:s${i}:9`, rel: 'calls' })
    const cols = { src: 'src_id', dst: 'dst_id', rel: 'rel' }
    expect(layout(rows, cols, around).fit).toEqual(layout(rows, cols).fit)
  })
})

describe('hubs', () => {
  it('names a crowded hub over the circles around it, never over another label', () => {
    const rows = []
    for (let i = 0; i < 40; i++) rows.push({ src_id: `fn:acme/app:f.go:leafWithAName${i}:1`, dst_id: 'fn:acme/app:f.go:theHubEveryoneCalls:2', rel: 'calls' })
    const g = layout(rows, { src: 'src_id', dst: 'dst_id', rel: 'rel' })
    const hub = 'fn:acme/app:f.go:theHubEveryoneCalls:2'
    const order = byWeight(g.nodes)
    const loose = placeLabels(g.nodes, order, 2.5, undefined, hubsOf(g.nodes))
    expect(hubsOf(g.nodes).has(hub)).toBe(true)
    expect(loose.has(hub)).toBe(true)
    const hb = loose.get(hub)!.box
    for (const [id, spot] of loose) {
      if (id === hub) continue
      const b = spot.box
      expect(hb.x0 < b.x1 && b.x0 < hb.x1 && hb.y0 < b.y1 && b.y0 < hb.y1, `the hub over ${id}`).toBe(false)
    }
  })
})

describe('cutTo', () => {
  it('keeps a label that fits and cuts one that does not, start and end kept', () => {
    expect(cutTo('getConnected:592', 30)).toBe('getConnected:592')
    const cut = cutTo('getGitlabUserInfoByMattermostID:405', 16)
    expect(cut.length).toBe(16)
    expect(cut.endsWith('ID:405')).toBe(true)
    expect(cut).toContain('…')
    expect(cutTo('abcdef', 3)).toBe('ab…')
  })
})

describe('stepsOf', () => {
  it('counts each node\'s step along the edges, and cuts a cycle', () => {
    const n = (...ids: string[]) => ids.map((id) => ({ id }))
    const e = (...pairs: string[]) => pairs.map((p) => ({ source: p[0]!, target: p[1]! }))
    expect(Object.fromEntries(stepsOf(n('a', 'b', 'c', 'd'), e('ab', 'bc', 'ac', 'cd')))).toEqual({ a: 0, b: 1, c: 2, d: 3 })
    expect(Object.fromEntries(stepsOf(n('a', 'b'), []))).toEqual({ a: 0, b: 0 })
    const cyc = stepsOf(n('a', 'b', 'c'), e('ab', 'bc', 'ca'))
    for (const v of cyc.values()) expect(v).toBeLessThanOrEqual(2)
  })
})
