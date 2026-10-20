import type { NodeStats } from './types'

// The track's full width: a transaction's compute budget, or two seconds
// of a request's time.
const capOf = (unit: string) => (unit === 'ms' ? 2_000 : 1_400_000)

function cu(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 10_000) return `${Math.round(n / 1000)}k`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

// Compute spread on a 0..1.4M track: the min..max span filled, a tick at
// the median, the numbers beside it.
export function UnitsRange({ stats, unit = 'cu' }: { stats?: NodeStats; unit?: string }) {
  const min = stats?.units_min, med = stats?.units_med, max = stats?.units_max
  const lo = min ?? med ?? max
  const hi = max ?? med ?? min
  if (lo === undefined || hi === undefined) return null
  const cap = capOf(unit)
  const x = (v: number) => Math.min(100, (v / cap) * 100)
  const exact = [min, med, max].filter((v): v is number => v !== undefined).map((v) => v.toLocaleString('en-US')).join(' · ')
  return (
    <span className="inline-flex items-center gap-2" title={`${exact} ${unit}`}>
      <span className="relative h-1.5 w-28 shrink-0 rounded-full bg-muted">
        <span className="absolute inset-y-0 rounded-full bg-primary/70" style={{ left: `${x(lo)}%`, width: `${Math.max(0, x(hi) - x(lo))}%`, minWidth: 2 }} />
        {med !== undefined && <span className="absolute -inset-y-0.5 w-px bg-foreground" style={{ left: `${x(med)}%` }} />}
      </span>
      <span className="num font-mono text-[11px] whitespace-nowrap text-muted-foreground">
        {lo === hi ? cu(lo) : `${cu(lo)}–${cu(hi)}`}
        {med !== undefined && lo !== hi && <span className="text-faint"> · {cu(med)}</span>}
        <span className="text-faint"> {unit}</span>
      </span>
    </span>
  )
}
