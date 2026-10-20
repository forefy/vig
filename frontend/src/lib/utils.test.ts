import { describe, expect, it } from 'vitest'
import { baseUnits, dur, relTime } from './utils'

describe('baseUnits', () => {
  it('places the point by decimals and groups the whole part', () => {
    expect(baseUnits('1234567890', 6)).toBe('1,234.5678')
    expect(baseUnits('1000000', 6)).toBe('1')
    expect(baseUnits('-2500000000', 9)).toBe('-2.5')
  })
  it('keeps dust when the whole part is zero', () => {
    expect(baseUnits('1', 9)).toBe('0.000000001')
    expect(baseUnits('0', 9)).toBe('0')
    expect(baseUnits('-0', 6)).toBe('0')
  })
  it('handles zero decimals and amounts past 2^53', () => {
    expect(baseUnits('42', 0)).toBe('42')
    expect(baseUnits('123456789012345678901234', 6)).toBe('123,456,789,012,345,678.9012')
  })
  it('passes through what it cannot parse', () => {
    expect(baseUnits('n/a', 6)).toBe('n/a')
  })
})

describe('dur', () => {
  it('picks one unit', () => {
    expect(dur(30_000)).toBe('30s')
    expect(dur(-90_000)).toBe('2m')
    expect(dur(5 * 3600_000)).toBe('5h')
    expect(dur(3 * 86400_000)).toBe('3d')
  })
})

describe('relTime', () => {
  it('reads a moment either side of now as now', () => {
    const now = Date.parse('2026-10-19T12:00:00Z')
    expect(relTime('2026-10-19T12:00:02Z', now)).toBe('now')
    expect(relTime('2026-10-19T11:59:57Z', now)).toBe('now')
    expect(relTime('2026-10-19T11:58:00Z', now)).toBe('2m ago')
    expect(relTime('2026-10-19T12:30:00Z', now)).toBe('in 30m')
  })
})
