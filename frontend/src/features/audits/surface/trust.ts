import { short } from '@/lib/utils'

export type Trust = 'ok' | 'warn' | undefined

// What an external program's facts say about who can change it.
export function calleeTrust(f?: Record<string, string>): Trust {
  if (!f) return undefined
  if (f.immutable === 'true' || f.verified === 'true') return 'ok'
  if (f.loader === 'upgradeable' && f.verified !== 'true') return 'warn'
  return undefined
}

export function calleeTip(f?: Record<string, string>): string | undefined {
  if (!f) return undefined
  const parts: string[] = []
  if (f.authority) parts.push(`authority ${short(f.authority, 4, 4)}`)
  if (f.loader) parts.push(f.loader)
  if (f.immutable === 'true') parts.push('immutable')
  if (f.verified === 'true') parts.push('verified')
  if (f.last_upgrade) parts.push(`upgraded ${f.last_upgrade}`)
  return parts.length ? parts.join(' · ') : undefined
}
