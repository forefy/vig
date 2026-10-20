import { describe, expect, it } from 'vitest'
import { unreadCount } from '@/lib/notifications'

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
