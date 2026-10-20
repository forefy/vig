import { describe, expect, it } from 'vitest'
import { buildModel, SIGNERS, sideId, thenOf, nowOf, checks, access, keysSeen, keyFor } from './model'
import type { Evidence, Node, Program, Surface, Replay } from './types'

const T = 'ThreadProg1111111111111111111111111111111111'
const F = 'FiberProg11111111111111111111111111111111111'
const OUT = 'SRSLprog111111111111111111111111111111111111'

const ix = (pid: string, name: string, facts: Record<string, string> = {}, source: Node['source'] = 'static'): Node =>
  ({ id: `ix:${pid}:${name}`, parent: `p:${pid}`, kind: 'instruction', name, source, facts, attention: 'unseen', observed: 0 }) as Node
const acct = (pid: string, of: string, name: string, facts: Record<string, string>): Node =>
  ({ id: `acct:${pid}:${of}:${name}`, parent: `ix:${pid}:${of}`, kind: 'account', name, source: 'static', facts, attention: 'unseen', observed: 0 }) as Node
const prog = (pid: string, external = false): Node => ({ id: `p:${pid}`, kind: 'program', name: pid.slice(0, 4), source: 'traffic', external, attention: 'unseen', observed: 0 }) as Node
const tx = (node: string, ref: string, summary: Record<string, unknown>): Evidence => ({ node_id: node, kind: 'tx', ref, at: '2026-09-20T10:00:00Z', summary }) as Evidence

const programs = [{ id: T, match: 'unverified', idl_present: false, ownership_proven: false, bytecode_changed: false }, { id: F, match: 'unverified', idl_present: false, ownership_proven: false, bytecode_changed: false }] as Program[]

function fixture(): { surface: Surface; evidence: Evidence[] } {
  const nodes: Node[] = [
    prog(T), prog(F), prog(OUT, true),
    ix(T, 'update_fiber', { file: 'programs/thread/src/lib.rs', line: '163' }), ix(T, 'update_thread'), ix(T, 'close_fiber'),
    ix(F, 'update'), ix(F, 'close'),
    acct(T, 'update_fiber', 'thread', { writable: 'true', seeds: 'b"thread"', bump: 'true', type: 'Account<Thread>', has_one: 'authority' }),
    acct(T, 'update_fiber', 'fiber', { writable: 'true', raw: 'true', type: 'UncheckedAccount' }),
    acct(F, 'update', 'thread', { signer: 'true', type: 'Signer' }),
  ]
  const edges = [
    { from: `ix:${T}:update_fiber`, to: `ix:${F}:update`, kind: 'cpi', source: 'static', facts: { signed: 'true', file: 'programs/thread/src/lib.rs', line: '171' } },
    { from: `ix:${T}:close_fiber`, to: `ix:${F}:close`, kind: 'cpi', source: 'static', facts: { signed: 'true' } },
    { from: `p:${OUT}`, to: `ix:${T}:update_fiber`, kind: 'cpi', count: 1 },
    { from: `p:${OUT}`, to: `ix:${T}:update_thread`, kind: 'cpi', count: 1 },
    { from: `p:${T}`, to: `ix:${F}:update`, kind: 'cpi', count: 1 },
    { from: `ix:${T}:update_fiber`, to: `p:${F}`, kind: 'cpi', count: 1 },
  ] as Surface['edges']
  const evidence = [
    // One transaction: OUT calls update_thread then update_fiber; the second calls fiber.update.
    tx(`ix:${T}:update_thread`, 'sigA', { via: OUT, depth: 2, seq: 2 }),
    tx(`ix:${T}:update_fiber`, 'sigA', { via: OUT, depth: 2, seq: 3, cpi: [F] }),
    tx(`ix:${F}:update`, 'sigA', { via: T, root: OUT, depth: 3, seq: 4, cpi: ['11111111111111111111111111111111'] }),
  ]
  return { surface: { adapter: 'solana', nodes, edges, built_at: '2026-09-20T10:00:00Z' } as Surface, evidence }
}

describe('ports model', () => {
  it('ties a nested call to the instruction that made it, not the first of that program', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, evidence, programs)
    const loop = m.edgeById.get(`ix:${T}:update_fiber>ix:${F}:update`)
    expect(loop?.kind).toBe('loop')
    expect(loop?.count).toBe(1)
    expect(loop?.signed).toBe(true) // from the static edge on the same pair
    expect(m.edgeById.has(`ix:${T}:update_thread>ix:${F}:update`)).toBe(false)
    // The program-level edges into and out of an audited program are covered by the loop.
    expect(m.edges.some((e) => e.to === `p:${F}` || e.from === `p:${T}`)).toBe(false)
    expect(m.targets.some((t) => t.pid === F)).toBe(false)
  })

  it('routes one signature across programs in execution order', () => {
    const { surface, evidence } = fixture()
    const t = buildModel(surface, evidence, programs).txs.get('sigA')!
    const side = sideId(`p:${OUT}`)
    expect(t.hops).toEqual([
      [SIGNERS, side],
      [side, `ix:${T}:update_thread`],
      [side, `ix:${T}:update_fiber`],
      [`ix:${T}:update_fiber`, `ix:${F}:update`],
      [`ix:${F}:update`, 'p:11111111111111111111111111111111'],
    ])
  })

  it('keeps a source-only link as a dashed candidate and indexes accounts by name', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, evidence, programs)
    const e = m.edgeById.get(`ix:${T}:close_fiber>ix:${F}:close`)
    expect(e?.count).toBe(0)
    expect(e?.inSource).toBe(true)
    expect(m.accounts.get('thread')?.map((u) => u.ix.node.name).sort()).toEqual(['update', 'update_fiber'])
    expect(m.ixs.get(`ix:${T}:update_fiber`)?.depth).toEqual([true, true, false, false])
  })

  it('reads validation and access from account facts', () => {
    expect(checks({ seeds: 'x', bump: 'true', type: 'Account<Thread>', has_one: 'authority' })).toEqual(['seeds', 'owner', 'has_one'])
    expect(checks({ raw: 'true', writable: 'true' })).toEqual([])
    expect(access({ raw: 'true', writable: 'true' })).toBe('write · raw')
    expect(access({ init: 'true', writable: 'true' })).toBe('init')
  })

  it('places a failure at the stage it died in', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, [...evidence, tx(`ix:${F}:close`, 'sigB', { failed: true, error: 'ConstraintSeeds' }), tx(`ix:${T}:close_fiber`, 'sigC', { failed: true, error: 'Custom', error_program: F })], programs)
    expect(thenOf(m.txs.get('sigB')!.rows[0]).stage).toBe(3)
    expect(thenOf(m.txs.get('sigC')!.rows[0]).stage).toBe(5)
    const r = { replay: 'x', outcome: 'custom 6004', units: 1, error: { code: 6004, raw: 'custom 6004' }, key: 'k' } as Replay
    expect(nowOf(r, T).stage).toBe(4)
    expect(nowOf({ ...r, error: { code: 3012, raw: '' } }, T).stage).toBe(2)
    expect(nowOf({ ...r, outcome: 'ok' }, T).stage).toBe(6)
  })
})

describe('inputs', () => {
  it('aggregates the keys passed for one account across calls', () => {
    const { surface, evidence } = fixture()
    const k1 = 'KeyOne111111111111111111111111111111111111', k2 = 'KeyTwo111111111111111111111111111111111111'
    const m = buildModel(surface, [...evidence,
      tx(`ix:${T}:update_fiber`, 'sigX', { keys: [k1, k2], signer_keys: [k1] }),
      tx(`ix:${T}:update_fiber`, 'sigY', { keys: [k1, 'KeyThree1111111111111111111111111111111111'] }),
    ], programs)
    const uses = m.accounts.get('thread')!.filter((u) => u.ix.node.name === 'update_fiber')
    expect(uses[0].index).toBe(0)
    const seen = keysSeen(uses)
    expect(seen[0]).toEqual({ key: k1, count: 2, signer: true })
    expect(keyFor({ keys: [k1, k2] }, 1)).toBe(k2)
    expect(keyFor({}, 0)).toBeUndefined()
  })
})

describe('web tree', () => {
  const H = 'app.example.com', M = 'mcp.example.com'
  const host = (h: string, source: Node['source'] = 'static'): Node => ({ id: `host:${h}`, kind: 'host', name: h, source, facts: { host: h }, attention: 'unseen', observed: 0 }) as Node
  const route = (h: string, path: string, facts: Record<string, string> = {}, source: Node['source'] = 'static', extra: Partial<Node> = {}): Node =>
    ({ id: `route:${h}:${path}`, parent: `host:${h}`, kind: 'route', name: path, source, facts: { host: h, path, ...facts }, attention: 'unseen', observed: 0, ...extra }) as Node
  const param = (h: string, path: string, name: string, where: string): Node =>
    ({ id: `route:${h}:${path}${where === 'body' ? '#' : '?'}${name}`, parent: `route:${h}:${path}`, kind: 'param', name, source: 'operator', facts: { in: where }, attention: 'unseen', observed: 0 }) as Node
  const req = (h: string, path: string, method: string, url: string, status: number, ms: number): Evidence =>
    ({ node_id: `route:${h}:${path}`, kind: 'request', ref: `${method} ${url}`, at: '2026-10-19T10:00:00Z', summary: { method, path: new URL(url).pathname, status, ms } }) as Evidence

  function fixture(): { surface: Surface; evidence: Evidence[] } {
    const nodes: Node[] = [
      host(H), host(M),
      route(H, '/login', { methods: 'GET, POST' }, 'operator'),
      route(H, '/api/projects/{id}/servers', {}, 'static', { attention: 'finding', findings: [4] }),
      route(M, '/mcp', {}, 'static', { attention: 'finding', findings: [5] }),
      route(H, '/health', {}, 'operator', { attention: 'reviewed' }),
      param(H, '/login', 'next', 'query'),
      param(H, '/login', 'password', 'body'),
      param(H, '/api/projects/{id}/servers', 'next', 'query'),
    ]
    const edges = [{ from: `route:${H}:/login`, to: `route:${H}:/api/projects/{id}/servers`, kind: 'nav', count: 2, source: 'traffic' }] as Surface['edges']
    const evidence = [
      req(H, '/login', 'POST', `https://${H}/login`, 200, 120),
      req(H, '/login', 'GET', `https://${H}/login?next=%2Fhome`, 403, 40),
      req(H, '/api/projects/{id}/servers', 'GET', `https://${H}/api/projects/42/servers`, 200, 80),
    ]
    return { surface: { adapter: 'webapp', nodes, edges, built_at: '2026-10-19T10:00:00Z' } as Surface, evidence }
  }

  it('boxes hosts, rows routes and ports parameters, with no program records', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, evidence, [])
    expect(m.words.web).toBe(true)
    expect(m.words.ixs).toBe('routes')
    expect(m.programs.map((p) => p.pid)).toEqual([H, M])
    expect(m.programs[0].id).toBe(`host:${H}`)
    expect(m.programs[0].node?.kind).toBe('host')
    expect(m.programs[0].ixs.map((x) => x.node.name)).toEqual(['/login', '/api/projects/{id}/servers', '/health'])
    const login = m.ixs.get(`route:${H}:/login`)!
    expect(login.pid).toBe(H)
    expect(login.accounts.map((a) => a.name)).toEqual(['next', 'password'])
    expect(login.txs).toHaveLength(2)
    expect(login.depth).toEqual([true, true, false, false])
    expect(m.ixs.get(`route:${H}:/api/projects/{id}/servers`)?.depth).toEqual([true, true, false, true])
    expect(m.ixs.get(`route:${H}:/health`)?.depth).toEqual([true, false, false, true])
    // A parameter name shared by two routes is one entry, as an account is.
    expect(m.accounts.get('next')?.map((u) => u.ix.node.name).sort()).toEqual(['/api/projects/{id}/servers', '/login'])
    expect(access({ in: 'body' })).toBe('body')
  })

  it('enters every request from the clients and keeps nav between routes as a loop', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, evidence, [])
    expect(m.edgeById.get(`${SIGNERS}>route:${H}:/login`)?.count).toBe(2)
    const nav = m.edgeById.get(`route:${H}:/login>route:${H}:/api/projects/{id}/servers`)
    expect(nav?.kind).toBe('loop')
    expect(nav?.count).toBe(2)
    expect(m.callers).toEqual([])
    expect(m.targets).toEqual([])
    const t = m.txs.get(`GET https://${H}/login?next=%2Fhome`)!
    expect(t.hops).toEqual([[SIGNERS, `route:${H}:/login`]])
    expect(m.words.txHref(t.sig)).toBe(`https://${H}/login?next=%2Fhome`)
  })

  it('reads a request outcome from its status', () => {
    const { surface, evidence } = fixture()
    const m = buildModel(surface, evidence, [])
    const bad = thenOf(m.txs.get(`GET https://${H}/login?next=%2Fhome`)!.rows[0])
    expect(bad).toEqual({ ok: false, word: '403', units: 40, stage: 4 })
    expect(thenOf(m.txs.get(`POST https://${H}/login`)!.rows[0]).ok).toBe(true)
  })
})
