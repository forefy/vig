import { describe, expect, it } from 'vitest'
import { axisOf, clockOf, emptyLive, fold, keptRows, layout, niceTicks, perMinute, pulsesOf, sumCalls } from './live'
import type { LiveView, Span } from './live'
import type { Evidence } from './types'

const P = 'GauwsjaNa7fmSRwCg9APFhZMkgxSz31q5HfNuLtRR522'
const row = (id: string, node: string, kind: Evidence['kind'], summary: Record<string, unknown>, at = '2026-09-27T00:00:00Z'): Evidence => ({ id, node_id: node, kind, ref: `sig-${id}`, at, summary }) as Evidence
const view = (rows: Evidence[], cursor: string, extra: Partial<LiveView> = {}): LiveView =>
  ({ watch: { state: 'live', watched: [] }, evidence: rows, cursor, coverage: [], totals: [], minutes: [], ...extra }) as LiveView

describe('fold', () => {
  it('replaces on the first read, appends after the cursor, keeps the newest', () => {
    let s = fold(emptyLive(), view([row('a', `ix:${P}:deposit`, 'tx', {})], 'a'))
    expect(s.rows.map((r) => r.id)).toEqual(['a'])
    expect(s.fresh).toEqual([])
    expect(s.replay.map((r) => r.id)).toEqual(['a'])
    expect(s.cursor).toBe('a')
    s = fold(s, view([row('b', `ix:${P}:deposit`, 'tx', {}), row('c', `ix:${P}:deposit`, 'tx', {})], 'c', { totals: [{ node_id: `ix:${P}:deposit`, calls: 3, failed: 1, units: 9 }] }))
    expect(s.rows.map((r) => r.id)).toEqual(['a', 'b', 'c'])
    expect(s.fresh.map((r) => r.id)).toEqual(['b', 'c'])
    expect(s.replay).toEqual([])
    expect(sumCalls(s.totals)).toBe(3)
    // An empty read keeps the cursor and adds nothing.
    s = fold(s, view([], '', {}))
    expect(s.cursor).toBe('c')
    expect(s.fresh).toEqual([])
    const many = Array.from({ length: keptRows + 50 }, (_, i) => row(`m${i}`, `ix:${P}:deposit`, 'tx', {}))
    s = fold(s, view(many, 'z'))
    expect(s.rows).toHaveLength(keptRows)
    expect(s.rows[s.rows.length - 1].id).toBe(`m${keptRows + 49}`)
  })
})

describe('pulsesOf', () => {
  it('keeps calls on instructions with who called and what they called', () => {
    const rows = [
      row('a', `ix:${P}:deposit`, 'tx', { via: 'JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4', cpi: ['TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA'], failed: true, slot: 5 }),
      row('b', `p:${P}`, 'tx', { unresolved: true }),
      row('c', `acct:${P}:deposit:vault`, 'write', {}),
    ]
    const p = pulsesOf(rows)
    expect(p).toHaveLength(2)
    expect(pulsesOf(rows, true).every((x) => x.replay)).toBe(true)
    expect(p[1].ixId).toBe(`p:${P}`)
    expect(p[0]).toMatchObject({ ixId: `ix:${P}:deposit`, via: 'JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4', cpi: ['TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA'], failed: true, slot: 5 })
  })
})

describe('clock and axis', () => {
  const t = (iso: string) => Date.parse(iso)
  const spans: Span[] = [
    { subject: P, kind: 'backfill', from_slot: 1000, to_slot: 2000, from_at: '2026-09-27T00:00:00Z', to_at: '2026-09-27T00:06:40Z' },
    { subject: P, kind: 'gap', from_slot: 2001, to_slot: 3000 },
  ]
  it('interpolates between known pairs and extrapolates at 400ms a slot', () => {
    const clock = clockOf(spans, [])
    expect(clock(1500)).toBe(t('2026-09-27T00:03:20Z'))
    expect(clock(2000 + 150)).toBe(t('2026-09-27T00:06:40Z') + 150 * 400)
    expect(clock(900)).toBe(t('2026-09-27T00:00:00Z') - 100 * 400)
  })
  it('lays spans and ticks on a linear axis', () => {
    const now = t('2026-09-27T02:00:00Z')
    const axis = axisOf(now - 2 * 3600_000, now)
    expect(axis.x(now - 3600_000)).toBeCloseTo(0.5, 5)
    expect(axis.x(now - 3 * 3600_000)).toBe(0)
    expect(axis.x(now + 1)).toBe(1)
    expect(axis.t(0.25)).toBe(now - 1.5 * 3600_000)
    const rows = [row('u', `p:${P}`, 'write', { kind: 'program_data', deploy_slot: 2500 }, '2026-09-27T01:30:00Z'), row('w', `acct:${P}:deposit:vault`, 'write', {}, '2026-09-27T00:03:20Z')]
    const { segments, ticks } = layout(spans, rows, clockOf(spans, []), axis)
    expect(segments).toHaveLength(2)
    expect(segments[0].kind).toBe('backfill')
    expect(segments[1].kind).toBe('gap')
    expect(segments[1].x0).toBeGreaterThanOrEqual(segments[0].x1 - 0.002)
    expect(ticks.map((k) => k.kind)).toEqual(['upgrade', 'write'])
    expect(ticks[0].x).toBeCloseTo(0.75, 5)
    expect(ticks[0].label).toBe('upgrade · slot 2500')
  })
  it('picks round label intervals', () => {
    const now = t('2026-09-27T02:00:00Z')
    const ticks = niceTicks(now - 50 * 60_000, now)
    expect(ticks.length).toBeGreaterThan(3)
    expect(ticks.length).toBeLessThanOrEqual(9)
    expect(ticks.every((x) => x % 600_000 === 0)).toBe(true)
    expect(niceTicks(now - 7 * 86400_000, now).every((x) => x % 86400_000 === 0)).toBe(true)
  })
})

describe('perMinute', () => {
  it('sums nodes per minute in order', () => {
    const m = perMinute([
      { node_id: 'a', minute: '2026-09-27T01:01:00Z', calls: 2, failed: 1 },
      { node_id: 'b', minute: '2026-09-27T01:00:00Z', calls: 5, failed: 0 },
      { node_id: 'b', minute: '2026-09-27T01:01:00Z', calls: 3, failed: 0 },
    ])
    expect(m.map((x) => [x.calls, x.failed])).toEqual([[5, 0], [5, 1]])
  })
})
