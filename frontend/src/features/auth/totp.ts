import type { components } from '@/api/client'

type Me = components['schemas']['Me']

// Admin routes need a TOTP check on this session within the last 12h.
export const TOTP_WINDOW_MS = 12 * 60 * 60 * 1000

export function totpFresh(me: Pick<Me, 'totp_verified_at'>, now = Date.now()): boolean {
  if (!me.totp_verified_at) return false
  const t = new Date(me.totp_verified_at).getTime()
  return Number.isFinite(t) && now - t < TOTP_WINDOW_MS
}

export const CODE_RE = /^\d{6}$/
