import type { components } from '@/api/client'
import type { Evidence } from './types'

// The live side of the surface, as data: what the feed sent, folded into
// one state the strip and the map read. Nothing here touches the DOM.

export type LiveView = components['schemas']['LiveView']
export type WatchStatus = components['schemas']['WatchStatus']
export type Span = components['schemas']['CoverageSpan']
export type Total = components['schemas']['NodeTotal']
export type Minute = components['schemas']['NodeMinute']
export type Bucket = components['schemas']['ActivityBucket']

// keptRows bounds what the tab remembers of the stream; the rollups keep
// the count.
export const keptRows = 400

// replayRows is how many of the newest rows the first read replays, so a
// map opened on a quiet minute still shows what the program does.
export const replayRows = 40

export type LiveState = {
  watch: WatchStatus | null
  rows: Evidence[] // newest last
  full: Evidence[] // the newest rows read in full, oldest first: the ones with inputs
  fresh: Evidence[] // what the last read added, for the map to animate
  replay: Evidence[] // the newest rows of the first read, animated once as recent
  cursor: string
  coverage: Span[]
  totals: Map<string, Total>
  minutes: Minute[]
  activity: Bucket[] // the whole watch, all nodes, in buckets
  bucket: number // bucket width in seconds
  reads: number
  error: string | null
}

export const emptyLive = (): LiveState => ({ watch: null, rows: [], full: [], fresh: [], replay: [], cursor: '', coverage: [], totals: new Map(), minutes: [], activity: [], bucket: 60, reads: 0, error: null })

// fold adds one read of the feed. Rows after the cursor append; a first
// read (no cursor) replaces.
export function fold(s: LiveState, v: LiveView): LiveState {
  const incoming = v.evidence ?? []
  const rows = s.cursor ? [...s.rows, ...incoming] : incoming
  return {
    watch: v.watch,
    rows: rows.length > keptRows ? rows.slice(rows.length - keptRows) : rows,
    full: s.full,
    fresh: s.cursor ? incoming : [],
    replay: s.cursor ? [] : incoming.slice(-replayRows),
    cursor: v.cursor || s.cursor,
    coverage: v.coverage ?? [],
    totals: new Map((v.totals ?? []).map((t) => [t.node_id, t])),
    minutes: v.minutes ?? [],
    activity: v.activity ?? [],
    bucket: v.bucket || 60,
    reads: s.reads + 1,
    error: null,
  }
}

export const failed = (s: LiveState, msg: string): LiveState => ({ ...s, fresh: [], replay: [], error: msg })

// withFull replaces the full-read rows from their own read.
export const withFull = (s: LiveState, full: Evidence[]): LiveState => ({ ...s, full, fresh: [], replay: [] })

// One call worth drawing: the instruction it hit, who called it, what it
// called, and whether it failed.
export type Pulse = { key: string; ixId: string; via: string; cpi: string[]; failed: boolean; slot: number; replay?: boolean }

export function pulsesOf(rows: Evidence[], replay = false): Pulse[] {
  const out: Pulse[] = []
  for (const e of rows) {
    // A call the map could place only on the program still lands on it.
    if (e.kind !== 'tx' || !(e.node_id.startsWith('ix:') || e.node_id.startsWith('p:'))) continue
    const s = (e.summary ?? {}) as Record<string, unknown>
    out.push({
      key: `${e.id ?? e.ref}:${e.node_id}`,
      ixId: e.node_id,
      via: typeof s.via === 'string' ? s.via : '',
      cpi: Array.isArray(s.cpi) ? (s.cpi as unknown[]).filter((x): x is string => typeof x === 'string') : [],
      failed: s.failed === true,
      slot: typeof s.slot === 'number' ? s.slot : 0,
      replay,
    })
  }
  return out
}

// A slot clock: seconds from slots, anchored on every (slot, time) pair the
// feed carried, so a span with no times still lands on the axis.
export type Clock = (slot: number) => number

const slotMs = 400

export function clockOf(spans: Span[], rows: Evidence[], head?: { slot: number; at: string }): Clock {
  const pairs: [number, number][] = []
  for (const sp of spans) {
    if (sp.from_at) pairs.push([sp.from_slot, Date.parse(sp.from_at)])
    if (sp.to_at) pairs.push([sp.to_slot, Date.parse(sp.to_at)])
  }
  for (const e of rows) {
    const slot = (e.summary as Record<string, unknown> | undefined)?.slot
    const at = e.at ?? e.created_at
    if (typeof slot === 'number' && at) pairs.push([slot, Date.parse(at)])
  }
  if (head) pairs.push([head.slot, Date.parse(head.at)])
  pairs.sort((a, b) => a[0] - b[0])
  if (pairs.length === 0) return () => Date.now() // unknowable: everything is now
  return (slot) => {
    if (slot <= pairs[0][0]) return pairs[0][1] - (pairs[0][0] - slot) * slotMs
    const last = pairs[pairs.length - 1]
    if (slot >= last[0]) return last[1] + (slot - last[0]) * slotMs
    let lo = 0, hi = pairs.length - 1
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1
      if (pairs[mid][0] <= slot) lo = mid
      else hi = mid
    }
    const [s0, t0] = pairs[lo], [s1, t1] = pairs[hi]
    return s1 === s0 ? t0 : t0 + ((slot - s0) / (s1 - s0)) * (t1 - t0)
  }
}

// The timeline's axis: the watch, start to now, linear. A player needs
// one scale, so a week and a minute read at whatever the buckets allow.
export type Axis = { t0: number; now: number; x: (t: number) => number; t: (x: number) => number }

export function axisOf(t0: number, now: number): Axis {
  const span = Math.max(1, now - t0)
  return {
    t0,
    now,
    x: (t) => (Math.max(t0, Math.min(now, t)) - t0) / span,
    t: (xu) => t0 + Math.max(0, Math.min(1, xu)) * span,
  }
}

// niceTicks picks label times at a round interval so at most about n fit.
export function niceTicks(t0: number, now: number, n = 8): number[] {
  const steps = [5, 10, 15, 30, 60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 172800, 604800].map((s) => s * 1000)
  const span = Math.max(1, now - t0)
  const step = steps.find((st) => span / st <= n) ?? steps[steps.length - 1]
  const out: number[] = []
  for (let t = Math.ceil(t0 / step) * step; t <= now; t += step) out.push(t)
  return out
}

export type Segment = { key: string; subject: string; kind: Span['kind']; x0: number; x1: number; from: number; to: number }
export type Tick = { key: string; x: number; kind: 'upgrade' | 'idl' | 'write'; at: number; label: string }

// layout places every span and every write on the axis, in unit
// coordinates. Spans thinner than a pixel still get one.
export function layout(spans: Span[], rows: Evidence[], clock: Clock, axis: Axis): { segments: Segment[]; ticks: Tick[] } {
  const segments: Segment[] = []
  for (const sp of spans) {
    const from = sp.from_at ? Date.parse(sp.from_at) : clock(sp.from_slot)
    const to = sp.to_at ? Date.parse(sp.to_at) : clock(sp.to_slot)
    if (!Number.isFinite(from) || !Number.isFinite(to)) continue
    const x0 = axis.x(Math.min(from, to)), x1 = axis.x(Math.max(from, to))
    segments.push({ key: sp.id ?? `${sp.subject}:${sp.from_slot}`, subject: sp.subject, kind: sp.kind, x0, x1: Math.max(x1, x0 + 0.002), from, to })
  }
  const ticks: Tick[] = []
  for (const e of rows) {
    if (e.kind !== 'write') continue
    const s = (e.summary ?? {}) as Record<string, unknown>
    const at = e.at ?? e.created_at
    const t = at ? Date.parse(at) : typeof s.slot === 'number' ? clock(s.slot) : NaN
    if (!Number.isFinite(t)) continue
    const kind: Tick['kind'] = s.kind === 'program_data' ? 'upgrade' : s.kind === 'idl' ? 'idl' : 'write'
    const label = kind === 'upgrade' ? `upgrade${typeof s.deploy_slot === 'number' ? ` · slot ${s.deploy_slot}` : ''}` : kind === 'idl' ? 'idl changed' : `write · ${e.node_id.split(':').slice(-1)[0]}`
    ticks.push({ key: e.id ?? `${e.node_id}:${e.ref}`, x: axis.x(t), kind, at: t, label })
  }
  return { segments, ticks }
}

// Calls per minute across every node, for the magnified tail.
export function perMinute(minutes: Minute[]): { at: number; calls: number; failed: number }[] {
  const m = new Map<string, { at: number; calls: number; failed: number }>()
  for (const x of minutes) {
    const have = m.get(x.minute) ?? { at: Date.parse(x.minute), calls: 0, failed: 0 }
    have.calls += x.calls
    have.failed += x.failed
    m.set(x.minute, have)
  }
  return [...m.values()].sort((a, b) => a.at - b.at)
}

export const sumCalls = (totals: Map<string, Total>): number => [...totals.values()].reduce((n, t) => n + t.calls, 0)
