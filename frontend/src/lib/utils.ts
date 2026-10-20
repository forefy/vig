export { cn } from 'cn'

export const USDC = 1_000_000

export function usdc(baseUnits: number, opts: { compact?: boolean } = {}): string {
  const v = baseUnits / USDC
  if (opts.compact && Number.isInteger(v)) return `$${v.toLocaleString('en-US')}`
  return `$${v.toLocaleString('en-US', { minimumFractionDigits: 0, maximumFractionDigits: 2 })}`
}

export function short(s: string | undefined | null, head = 4, tail = 4): string {
  if (!s) return ''
  if (s.length <= head + tail + 1) return s
  if (tail <= 0) return s.slice(0, head)
  return `${s.slice(0, head)}…${s.slice(-tail)}`
}

export function relTime(iso: string | undefined | null, now = Date.now()): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  const d = Math.round((t - now) / 1000)
  const abs = Math.abs(d)
  // Within a few seconds either way is now: a row written by the server a
  // moment ago reads "now", not "in 0s" from clock skew.
  if (abs < 5) return 'now'
  const unit = abs < 60 ? [abs, 's'] : abs < 3600 ? [Math.round(abs / 60), 'm'] : abs < 86400 ? [Math.round(abs / 3600), 'h'] : [Math.round(abs / 86400), 'd']
  return d < 0 ? `${unit[0]}${unit[1]} ago` : `in ${unit[0]}${unit[1]}`
}

export function countdown(iso: string | undefined | null, now = Date.now()): string {
  if (!iso) return ''
  let s = Math.max(0, Math.round((new Date(iso).getTime() - now) / 1000))
  const h = Math.floor(s / 3600)
  s -= h * 3600
  const m = Math.floor(s / 60)
  s -= m * 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

export function fmtDate(iso: string | undefined | null): string {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })
}

export function kb(bytes: number | undefined): string {
  if (!bytes) return '0 KB'
  return `${(bytes / 1024).toFixed(1)} KB`
}

// A token amount in base units, as the decimal string the mint's decimals
// imply. String arithmetic: amounts past 2^53 stay exact. Fractions are cut
// to maxFrac unless the whole part is zero, where the full fraction stays so
// dust never rounds to nothing.
export function baseUnits(amount: string, decimals: number, maxFrac = 4): string {
  let s = amount.trim()
  const neg = s.startsWith('-')
  if (neg || s.startsWith('+')) s = s.slice(1)
  if (!/^\d+$/.test(s)) return amount
  s = s.replace(/^0+(?=\d)/, '')
  let out: string
  if (decimals > 0) {
    s = s.padStart(decimals + 1, '0')
    const whole = s.slice(0, -decimals)
    let frac = s.slice(-decimals).replace(/0+$/, '')
    if (whole !== '0') frac = frac.slice(0, maxFrac).replace(/0+$/, '')
    out = group(whole) + (frac ? `.${frac}` : '')
  } else out = group(s)
  return neg && out !== '0' ? `-${out}` : out
}

function group(whole: string): string {
  return whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

// A duration, in the same one-unit vocabulary relTime uses.
export function dur(ms: number): string {
  const s = Math.round(Math.abs(ms) / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
}
