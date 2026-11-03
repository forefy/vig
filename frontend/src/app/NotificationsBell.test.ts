import { describe, expect, it } from 'vitest'
import { groupByKind, unreadCount } from '@/lib/notifications'

const n = (id: string, read: string | null) =>
  ({ id, user_id: 'u', kind: 'audit_state', payload: {}, channels: ['inapp'], delivered: {}, read_at: read, created_at: '2026-09-09T00:00:00Z' }) as never

describe('unreadCount', () => {
  it('counts rows without read_at', () => {
    expect(unreadCount([n('a', null), n('b', '2026-09-09T01:00:00Z'), n('c', null)])).toBe(2)
  })
  it('is zero for empty or missing lists', () => {
    expect(unreadCount([])).toBe(0)
    expect(unreadCount(undefined)).toBe(0)
  })
})

describe('groupByKind', () => {
  const at = (id: string, kind: string, created: string, read: string | null = null) => ({ id, kind, payload: {}, read_at: read, created_at: created }) as never
  it('folds a kind into one group, newest group first, items newest first', () => {
    const items = [
      at('a', 'audit_overdue', '2026-11-01T10:00:00Z'),
      at('b', 'report_revised', '2026-11-02T09:00:00Z', '2026-11-02T09:05:00Z'),
      at('c', 'audit_overdue', '2026-11-02T08:00:00Z', '2026-11-02T08:30:00Z'),
      at('d', 'audit_overdue', '2026-11-01T12:00:00Z'),
    ]
    const g = groupByKind(items)
    expect(g.map((x) => [x.kind, x.items.length, x.unread])).toEqual([
      ['report_revised', 1, 0],
      ['audit_overdue', 3, 2],
    ])
    expect(g[1]!.items.map((n: { id: string }) => n.id)).toEqual(['c', 'd', 'a'])
    expect(g[1]!.newest).toBe('2026-11-02T08:00:00Z')
  })
  it('is empty for no notifications', () => {
    expect(groupByKind(undefined)).toEqual([])
  })
})
