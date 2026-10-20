import type { Node, Edge, Evidence, Program, Replay, ReplayAccount, Surface, NodeStats, TokenMove } from './types'
import { statsOf, time, outcomeWord } from './types'

// The ports model: what the two zooms and the two lenses read. It is built
// once per response from the surface document and its evidence, and holds
// nothing the API did not send. Links between instructions of two audited
// programs come from the source when it names the callee instruction, and
// from traffic when one signature lands on both.

export const SIGNERS = 'signers'
const IN = 'in:' // a program drawn on the entry side, for the same program id

export type Depth = [declared: boolean, observed: boolean, replayed: boolean, reviewed: boolean]

export type Ix = {
  id: string
  node: Node
  pid: string
  accounts: Node[]
  txs: Evidence[]
  replays: Replay[]
  reviews: Evidence[]
  stats?: NodeStats
  depth: Depth
  group?: string
}

export type Group = { key: string; label: string; ixs: Ix[] }

export type ProgramBox = {
  id: string // node id, p:<pid>
  pid: string
  node?: Node
  info: Program
  ixs: Ix[]
  groups: Group[] | null
  init?: Ix
}

export type Outside = { id: string; pid: string; node?: Node; name: string; both: boolean; fog: boolean }

export type MapEdge = {
  id: string
  from: string
  to: string
  kind: 'in' | 'out' | 'loop'
  count: number
  signed: boolean
  inSource: boolean
  facts: Record<string, string>
  sigs: string[]
}

export type TxRow = { ix: Ix; ev: Evidence; s: Record<string, unknown> }
export type Tx = { sig: string; at?: string; rows: TxRow[]; replays: Replay[]; hops: [string, string][]; live?: boolean }

export type AccountUse = { ix: Ix; node: Node; index: number }

// One address seen standing in for an account, and how often.
export type KeySeen = { key: string; count: number; signer: boolean }

// keysOf lists the addresses a call passed, in declaration order.
export const keysOf = (s: Record<string, unknown>): string[] => (Array.isArray(s.keys) ? (s.keys as unknown[]).filter((k): k is string => typeof k === 'string') : [])

// keyFor is the address a call passed for one declared account.
export const keyFor = (s: Record<string, unknown>, index: number): string | undefined => keysOf(s)[index]

// keysSeen aggregates the addresses passed for one account across the
// sampled calls of the given uses, busiest first.
export function keysSeen(uses: AccountUse[]): KeySeen[] {
  const m = new Map<string, KeySeen>()
  for (const u of uses)
    for (const ev of u.ix.txs) {
      const s = summaryOf(ev)
      const k = keyFor(s, u.index)
      if (!k) continue
      const signers = Array.isArray(s.signer_keys) ? (s.signer_keys as string[]) : []
      const have = m.get(k) ?? { key: k, count: 0, signer: false }
      have.count++
      have.signer = have.signer || signers.includes(k)
      m.set(k, have)
    }
  return [...m.values()].sort((a, b) => b.count - a.count || a.key.localeCompare(b.key))
}

// The target's own words for the things the map draws: the unit of
// height, who enters, what a program exposes, what the dock holds, the
// stages a call passes, and where a transaction is read. On the web a
// host stands for a program, a route for an instruction, a parameter for
// an account, and a request for a transaction.
export type Words = {
  evm: boolean
  web: boolean
  height: string
  signers: string
  ixs: string
  program: string
  programs: string
  units: string
  calls: string
  txs: string
  accounts: string
  // The node id prefix of a program's box: p: on chain, host: on the web.
  boxPrefix: string
  // The six stages of one call, in execution order.
  stages: [string, string, string, string, string, string]
  txHref: (sig: string, pid?: string) => string
}

// A write on a program node that changes what was audited: an upgrade of
// the code (or its authority), or a new interface.
export type DriftRow = { ev: Evidence; kind: 'upgrade' | 'idl'; pid: string; at: string; s: Record<string, unknown> }

export type Model = {
  words: Words
  drift: DriftRow[]
  byId: Map<string, Node>
  programs: ProgramBox[]
  ixs: Map<string, Ix>
  callers: Outside[]
  targets: Outside[]
  edges: MapEdge[]
  edgeById: Map<string, MapEdge>
  txs: Map<string, Tx>
  accounts: Map<string, AccountUse[]>
  maxObserved: number
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '')
// The program (or host) a node belongs to, from its id: p:<pid>, ix:<pid>:…,
// host:<host>, route:<host>:/… (a host may carry a port, a path starts
// with a slash).
const pidOf = (nodeId: string): string =>
  nodeId.startsWith('p:') ? nodeId.slice(2) : nodeId.startsWith('ix:') ? nodeId.split(':')[1] : nodeId.startsWith('host:') ? nodeId.slice(5) : nodeId.startsWith('route:') ? nodeId.slice(6, nodeId.indexOf(':/')) : ''
export const sideId = (programNodeId: string): string => IN + programNodeId
export const isSide = (id: string): boolean => id.startsWith(IN)

export function summaryOf(e: Evidence): Record<string, unknown> {
  return (e.summary ?? {}) as Record<string, unknown>
}

// Group a large program's instructions by the source directory they live
// in, else by the word their names share (create_thread, close_thread). A
// host's routes group by their first path segment, which is the sitemap.
function groupsOf(ixs: Ix[], web: boolean): Group[] | null {
  if (ixs.length <= 8) return null
  if (web) {
    const segs = new Map<string, Ix[]>()
    for (const ix of ixs) {
      const seg = `/${ix.node.name.split('/').filter(Boolean)[0] ?? ''}`
      segs.set(seg, [...(segs.get(seg) ?? []), ix])
    }
    if (segs.size < 2 || segs.size > ixs.length / 2) return null
    return [...segs.entries()]
      .map(([label, list]) => ({ key: label, label, ixs: list }))
      .sort((a, b) => b.ixs.reduce((n, x) => n + x.txs.length, 0) - a.ixs.reduce((n, x) => n + x.txs.length, 0) || a.label.localeCompare(b.label))
  }
  const dirs = new Map<string, Ix[]>()
  for (const ix of ixs) {
    // The instruction's own module when the source has one per instruction,
    // else the directory of the handler's file.
    const file = ix.node.facts?.file ?? ''
    const parts = file.split('/')
    const dir = ix.node.facts?.module ?? (parts.length > 1 ? parts[parts.length - 2] : '')
    dirs.set(dir, [...(dirs.get(dir) ?? []), ix])
  }
  let buckets = dirs
  // Modules only help when they divide the program: one directory holding
  // most of it says nothing, so the names decide instead.
  const biggest = Math.max(...[...dirs.values()].map((l) => l.length))
  if (dirs.size < 2 || biggest > ixs.length * 0.6 || [...dirs.keys()].every((d) => d === '' || d === 'src')) {
    const freq = new Map<string, number>()
    for (const ix of ixs) for (const w of new Set(ix.node.name.split('_'))) freq.set(w, (freq.get(w) ?? 0) + 1)
    buckets = new Map()
    for (const ix of ixs) {
      // verb_noun naming: the last word is the noun when others share it,
      // else the first word; a name nothing shares goes to other.
      const words = ix.node.name.split('_')
      const shared = (w: string) => (freq.get(w) ?? 0) >= 2
      const last = words[words.length - 1]
      const pick = words.length > 1 && shared(last) ? last : words.length > 1 && shared(words[0]) ? words[0] : 'other'
      buckets.set(pick, [...(buckets.get(pick) ?? []), ix])
    }
    if (buckets.size < 2 || buckets.size > ixs.length / 2) return null
  }
  return [...buckets.entries()]
    .map(([label, list]) => ({ key: label || 'src', label: label || 'src', ixs: list }))
    .sort((a, b) => b.ixs.reduce((n, x) => n + x.txs.length, 0) - a.ixs.reduce((n, x) => n + x.txs.length, 0) || a.label.localeCompare(b.label))
}

const chainStages: Words['stages'] = ['enters', 'loads', 'validates', 'runs', 'calls', 'commits']

export function wordsOf(surface: Surface, byId: Map<string, Node>): Words {
  if (surface.adapter === 'webapp') {
    // A request's ref is "METHOD url": the link is the url itself.
    return { evm: false, web: true, height: '', signers: 'clients', ixs: 'routes', program: 'host', programs: 'hosts', units: 'ms', calls: 'nav', txs: 'requests', accounts: 'parameters', boxPrefix: 'host:', stages: ['from', 'takes', 'in', 'handles', 'navigates to', 'done'], txHref: (ref) => ref.slice(ref.indexOf(' ') + 1) }
  }
  if (surface.adapter === 'ethereum') {
    const explorerOf = (pid?: string) => str(byId.get(`p:${pid ?? ''}`)?.facts?.explorer) || str([...byId.values()].find((n) => n.kind === 'program')?.facts?.explorer) || 'https://etherscan.io'
    return { evm: true, web: false, height: 'block', signers: 'senders', ixs: 'functions', program: 'contract', programs: 'contracts', units: 'gas', calls: 'calls out', txs: 'transactions', accounts: 'accounts', boxPrefix: 'p:', stages: chainStages, txHref: (sig, pid) => `${explorerOf(pid)}/tx/${sig}` }
  }
  return { evm: false, web: false, height: 'slot', signers: 'signers', ixs: 'instructions', program: 'program', programs: 'programs', units: 'cu', calls: 'cpi', txs: 'transactions', accounts: 'accounts', boxPrefix: 'p:', stages: chainStages, txHref: (sig) => `https://explorer.solana.com/tx/${sig}` }
}

export function buildModel(surface: Surface, evidence: Evidence[], programs: Program[]): Model {
  const nodes = surface.nodes ?? []
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const words = wordsOf(surface, byId)
  // On the web the boxes are the hosts on the map; the audit holds no
  // deployment record for them, so each gets a bare identity.
  const infos: Program[] = words.web
    ? nodes.filter((n) => n.kind === 'host' && !n.external).map((n) => ({ id: n.facts?.host ?? n.name, match: 'unverified', idl_present: false, ownership_proven: false, bytecode_changed: false }) as Program)
    : programs
  const audited = new Set(infos.map((p) => p.id))

  const ixs = new Map<string, Ix>()
  for (const n of nodes) {
    if ((n.kind !== 'instruction' && n.kind !== 'route') || n.external) continue
    ixs.set(n.id, { id: n.id, node: n, pid: pidOf(n.parent ?? n.id), accounts: [], txs: [], replays: [], reviews: [], stats: statsOf(n), depth: [false, false, false, false] })
  }
  for (const n of nodes) if ((n.kind === 'account' || n.kind === 'param') && n.parent) ixs.get(n.parent)?.accounts.push(n)

  const txs = new Map<string, Tx>()
  const drift: DriftRow[] = []
  evidence.forEach((e, i) => {
    const ix = ixs.get(e.node_id)
    if (!ix) {
      const s = summaryOf(e)
      if (e.kind === 'write' && e.node_id.startsWith('p:') && (s.kind === 'program_data' || s.kind === 'idl')) {
        drift.push({ ev: e, kind: s.kind === 'idl' ? 'idl' : 'upgrade', pid: e.node_id.slice(2), at: e.at ?? e.created_at ?? '', s })
      }
      return
    }
    if (e.kind === 'replay') {
      const r = e.summary as unknown as Omit<Replay, 'key' | 'at'> | undefined
      if (r && typeof r.replay === 'string') ix.replays.push({ ...r, at: e.at ?? e.created_at, key: e.id ?? `${e.node_id}:${i}` })
    } else if (e.kind === 'review') ix.reviews.push(e)
    else if (e.ref) {
      ix.txs.push(e)
      const t = txs.get(e.ref) ?? { sig: e.ref, at: e.at ?? e.created_at, rows: [], replays: [], hops: [] }
      t.rows.push({ ix, ev: e, s: summaryOf(e) })
      txs.set(e.ref, t)
    }
  })
  for (const ix of ixs.values()) {
    ix.replays.sort((a, b) => time(b.at) - time(a.at))
    ix.txs.sort((a, b) => time(b.at ?? b.created_at) - time(a.at ?? a.created_at))
    for (const r of ix.replays) {
      const t = txs.get(r.replay)
      if (t) t.replays.push(r)
    }
    ix.depth = [ix.node.source !== 'traffic', ix.txs.length > 0, ix.replays.length > 0, ix.node.attention === 'reviewed' || ix.node.attention === 'finding']
  }

  // ---- edges
  const edgeById = new Map<string, MapEdge>()
  const add = (from: string, to: string, kind: MapEdge['kind'], count: number, e?: Edge, sig?: string) => {
    const id = `${from}>${to}`
    const have = edgeById.get(id) ?? { id, from, to, kind, count: 0, signed: false, inSource: false, facts: {}, sigs: [] }
    have.count += count
    if (e) {
      have.signed = have.signed || e.facts?.signed === 'true'
      have.inSource = have.inSource || e.source === 'static'
      have.facts = { ...e.facts, ...have.facts }
    }
    if (sig && !have.sigs.includes(sig)) have.sigs.push(sig)
    edgeById.set(id, have)
    return have
  }

  // What one signature did, as hops. A row reached through an audited
  // program is tied to that program's row in the same transaction.
  const entered = new Map<string, number>() // ix id -> times signed for directly
  const num = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined)
  for (const t of txs.values()) {
    // Execution order, when the sampler recorded it.
    t.rows.sort((a, b) => (num(a.s.seq) ?? 0) - (num(b.s.seq) ?? 0))
    const callerOf = (row: TxRow): string => {
      const via = str(row.s.via)
      if (!via) return SIGNERS
      if (!audited.has(via)) return sideId(`p:${via}`)
      // The caller is the nearest earlier call of that program, one level up.
      const seq = num(row.s.seq), depth = num(row.s.depth)
      const before = t.rows.filter((r) => r !== row && r.ix.pid === via && (seq === undefined || (num(r.s.seq) ?? 0) < seq))
      const up = before.filter((r) => depth === undefined || (num(r.s.depth) ?? 1) === depth - 1)
      return (up[up.length - 1] ?? before[before.length - 1])?.ix.id ?? `p:${via}`
    }
    const top = t.rows.filter((r) => !str(r.s.via))
    const rest = t.rows.filter((r) => str(r.s.via))
    for (const row of [...top, ...rest]) {
      const from = callerOf(row)
      const root = str(row.s.root)
      if (from === SIGNERS) entered.set(row.ix.id, (entered.get(row.ix.id) ?? 0) + 1)
      else if (root && !audited.has(root)) {
        // Signed for at the top, then handed down to whoever called us.
        t.hops.push([SIGNERS, sideId(`p:${root}`)])
        if (isSide(from) && from !== sideId(`p:${root}`)) t.hops.push([sideId(`p:${root}`), from])
      } else if (isSide(from)) t.hops.push([SIGNERS, from])
      t.hops.push([from, row.ix.id])
      if (ixs.has(from)) add(from, row.ix.id, 'loop', 1, undefined, t.sig)
      const cpi = Array.isArray(row.s.cpi) ? (row.s.cpi as unknown[]).map(str).filter(Boolean) : []
      for (const target of cpi) {
        if (audited.has(target)) continue // its own row carries that hop
        t.hops.push([row.ix.id, `p:${target}`])
      }
    }
    const seen = new Set<string>()
    t.hops = t.hops.filter(([a, b]) => (seen.has(`${a}>${b}`) ? false : (seen.add(`${a}>${b}`), true)))
  }

  for (const e of surface.edges ?? []) {
    const fromIx = ixs.has(e.from), toIx = ixs.has(e.to)
    if (fromIx && toIx) {
      const have = edgeById.get(`${e.from}>${e.to}`)
      if (have) add(e.from, e.to, 'loop', 0, e)
      else add(e.from, e.to, 'loop', e.count ?? 0, e)
    } else if (fromIx) {
      // A call into an audited program is drawn on the instruction it
      // reached when traffic or source says which; else on the program.
      const tp = pidOf(e.to)
      const covered = audited.has(tp) && [...edgeById.values()].some((x) => x.from === e.from && ixs.get(x.to)?.pid === tp)
      if (covered) {
        for (const x of edgeById.values()) if (x.from === e.from && ixs.get(x.to)?.pid === tp && e.facts?.signed === 'true') x.signed = true
        continue
      }
      // A call into an audited program with no instruction known lands on
      // that program's box, not among the outside programs.
      add(e.from, e.to, audited.has(tp) ? 'loop' : 'out', e.count ?? 0, e)
    } else if (toIx) {
      const fp = pidOf(e.from)
      if (audited.has(fp)) {
        const tied = [...edgeById.values()].filter((x) => x.to === e.to && ixs.get(x.from)?.pid === fp).reduce((n, x) => n + x.count, 0)
        const rest = (e.count ?? 0) - tied
        if (rest > 0) add(e.from, e.to, 'loop', rest, e)
      } else add(sideId(e.from), e.to, 'in', e.count ?? 0, e)
    }
  }
  for (const [id, n] of entered) add(SIGNERS, id, 'in', n)
  // Later static edges may name instructions traffic also tied; settle sigs.
  for (const t of txs.values())
    for (const [a, b] of t.hops) {
      const e = edgeById.get(`${a}>${b}`)
      if (e && !e.sigs.includes(t.sig)) e.sigs.push(t.sig)
    }

  const edges = [...edgeById.values()]

  // ---- outside programs, on the side they appear
  const outside = (id: string): Outside => {
    const nodeId = isSide(id) ? id.slice(IN.length) : id
    const node = byId.get(nodeId)
    const pid = pidOf(nodeId)
    return { id, pid, node, name: node?.name ?? pid, both: false, fog: nodeId === 'p:static:dynamic' }
  }
  const callerIds = new Set(edges.filter((e) => e.kind === 'in' && e.from !== SIGNERS).map((e) => e.from))
  const targetIds = new Set(edges.filter((e) => e.kind === 'out').map((e) => e.to))
  for (const t of txs.values()) for (const [, b] of t.hops) if (b.startsWith('p:') && !audited.has(pidOf(b))) targetIds.add(b)
  const callers = [...callerIds].map(outside)
  const targets = [...targetIds].map(outside)
  for (const c of callers) {
    const twin = targets.find((t) => t.pid === c.pid)
    if (twin) c.both = twin.both = true
  }
  const weight = (o: Outside) => edges.filter((e) => e.from === o.id || e.to === o.id).reduce((n, e) => n + e.count, 0)
  callers.sort((a, b) => weight(b) - weight(a))
  targets.sort((a, b) => Number(a.fog) - Number(b.fog) || weight(b) - weight(a) || a.name.localeCompare(b.name))

  // ---- programs
  const boxes: ProgramBox[] = infos.map((info) => {
    const list = [...ixs.values()].filter((x) => x.pid === info.id).sort((a, b) => b.txs.length - a.txs.length || a.node.name.localeCompare(b.node.name))
    const groups = groupsOf(list, words.web)
    if (groups) for (const g of groups) for (const x of g.ixs) x.group = `${info.id}:${g.key}`
    const init = list.find((x) => /^init(ialize)?(_|$)/.test(x.node.name))
    const id = `${words.boxPrefix}${info.id}`
    return { id, pid: info.id, node: byId.get(id), info, ixs: list, groups, init }
  })

  // ---- accounts by name across instructions
  const accounts = new Map<string, AccountUse[]>()
  for (const ix of ixs.values())
    ix.accounts.forEach((a, index) => {
      if (/^Program|^Interface$|^Sysvar/.test(a.facts?.type ?? '')) return
      accounts.set(a.name, [...(accounts.get(a.name) ?? []), { ix, node: a, index }])
    })

  let maxObserved = 1
  for (const ix of ixs.values()) if (ix.txs.length > maxObserved) maxObserved = ix.txs.length
  drift.sort((a, b) => time(b.at) - time(a.at))
  return { words, drift, byId, programs: boxes, ixs, callers, targets, edges, edgeById, txs, accounts, maxObserved }
}

// How an instruction uses an account, in one or two words; for a route's
// parameter, where it travels.
export function access(f: Record<string, string> | undefined): string {
  const a = f ?? {}
  if (a.in) return a.in
  const word = a.init ? 'init' : a.close ? 'close' : a.writable ? 'write' : a.signer ? 'signer' : 'read'
  return a.raw ? `${word} · raw` : word
}

// The checks the framework runs before the handler, from the source.
export function checks(f: Record<string, string> | undefined): string[] {
  const a = f ?? {}
  if (a.raw) return []
  const out: string[] = []
  if (a.signer) out.push('signer')
  if (a.seeds) out.push(a.bump === 'true' ? 'seeds' : 'seeds, no bump')
  if (a.type && /^(Account|AccountLoader|InterfaceAccount|SystemAccount|Box)/.test(a.type)) out.push('owner')
  if (a.has_one) out.push('has_one')
  if (a.owner) out.push('owner =')
  if (a.address) out.push('address')
  if (a.constraints) out.push(`${a.constraints} constraint${a.constraints === '1' ? '' : 's'}`)
  return out
}

// The stage of one execution an outcome reached: 1 enters, 2 loads,
// 3 validates, 4 runs, 5 calls, 6 commits.
export type Outcome = { ok: boolean; word: string; units?: number; stage: number }

function stageOf(name: string, code: number | undefined, foreign: boolean): number {
  if (foreign) return 5
  if (code !== undefined) {
    if (code >= 2000 && code < 2500) return 3
    if (code >= 3000 && code < 4000) return 2
    if (code >= 4100 && code < 4200) return 1
    return 4
  }
  if (/^Constraint/.test(name)) return 3
  if (/^Account|^InvalidProgram|^Declared/.test(name)) return 2
  return 4
}

export function thenOf(row: TxRow): Outcome {
  // A request: its status says how it went, its duration is its cost.
  if (typeof row.s.status === 'number') {
    const ok = row.s.status < 400
    return { ok, word: String(row.s.status), units: typeof row.s.ms === 'number' ? row.s.ms : undefined, stage: ok ? 6 : 4 }
  }
  const failed = Boolean(row.s.failed)
  const units = typeof row.s.units === 'number' ? row.s.units : undefined
  if (!failed) return { ok: true, word: 'ok', units, stage: 6 }
  const name = str(row.s.error) || 'failed'
  const at = str(row.s.error_program)
  return { ok: false, word: name, units, stage: stageOf(name, undefined, Boolean(at) && at !== row.ix.pid) }
}

export function nowOf(r: Replay, pid: string): Outcome {
  if (r.outcome === 'ok') return { ok: true, word: 'ok', units: r.units, stage: 6 }
  const name = r.error?.name || r.outcome
  const word = outcomeWord(r)
  const failedIn = [...(r.logs ?? [])].reverse().find((l) => / failed/.test(l))
  const foreign = Boolean(failedIn) && !failedIn!.includes(pid) && (r.calls ?? []).length > 1 && !/custom program error/.test(failedIn!) ? true : false
  return { ok: false, word, units: r.units, stage: stageOf(name, r.error?.code, foreign) }
}

// What a replay did to one named account, short enough for a cell.
// index is the account's position in the instruction: a program with no
// IDL replays with unnamed accounts, and the source declares them in the
// order the instruction receives them.
export function replayAccount(r: Replay | undefined, name: string, index?: number): ReplayAccount | undefined {
  const list = r?.accounts ?? []
  return list.find((x) => x.name === name) ?? (index !== undefined && list.every((x) => !x.name) ? list[index] : undefined)
}

export function changeWord(r: Replay, name: string, index?: number): { text: string; tone: 'ok' | 'bad' | 'warn' | 'faint' } {
  const a = replayAccount(r, name, index)
  if (!a) return { text: '·', tone: 'faint' }
  const ch = a.changed ?? []
  if (ch.length === 0) return { text: r.outcome === 'ok' ? 'unchanged' : 'rolled back', tone: 'faint' }
  if (ch.includes('exists')) return a.before ? { text: 'closed', tone: 'bad' } : { text: 'created', tone: 'ok' }
  const parts: string[] = []
  if (ch.includes('lamports')) {
    const d = (a.after?.lamports ?? 0) - (a.before?.lamports ?? 0)
    parts.push(`${d > 0 ? '+' : ''}${(d / 1e9).toLocaleString('en-US', { maximumFractionDigits: 4 })} SOL`)
  }
  const fields = ch.filter((k) => k !== 'lamports' && k !== 'size')
  if (fields.length === 1) parts.push(fields[0])
  else if (fields.length > 1) parts.push(`${fields.length} fields`)
  if (ch.includes('size')) parts.push('size')
  return { text: parts.join(' · '), tone: 'ok' }
}

export type { TokenMove }
