import { cn } from '@/lib/utils'
import type { NodeStats } from './types'

const CAP = 5

// ok and each error as a stacked bar, counts beside it, the error names as
// chips under it. compact keeps the bar and moves the names into the title.
export function OutcomeBar({ stats, compact, className }: { stats?: NodeStats; compact?: boolean; className?: string }) {
  if (!stats) return null
  const total = stats.ok + stats.failed
  if (!(total > 0)) return null
  const errors = Object.entries(stats.errors ?? {}).sort((a, b) => b[1] - a[1])
  const named = errors.reduce((n, [, c]) => n + c, 0)
  const other = Math.max(0, stats.failed - named)
  const pct = (n: number) => `${(n / total) * 100}%`
  const tip = errors.map(([n, c]) => `${n} ${c}`).join(' · ')
  return (
    <div className={cn('flex flex-col items-end gap-1', className)}>
      <div className="flex items-center gap-2" title={compact ? tip || undefined : undefined}>
        <span className="flex h-1.5 w-24 shrink-0 overflow-hidden rounded-full bg-muted">
          {stats.ok > 0 && <span className="h-full flex-none bg-ok" style={{ width: pct(stats.ok) }} />}
          {errors.map(([n, c], i) => (
            <span key={n} className="h-full flex-none bg-bad" style={{ width: pct(c), opacity: 1 - Math.min(i, 4) * 0.16 }} />
          ))}
          {other > 0 && <span className="h-full flex-none bg-bad/30" style={{ width: pct(other) }} />}
        </span>
        <span className="num font-mono text-[11px] whitespace-nowrap">
          <span className="text-ok">{stats.ok.toLocaleString('en-US')} ok</span>
          {stats.failed > 0 && <span className="text-bad"> · {stats.failed.toLocaleString('en-US')} failed</span>}
        </span>
      </div>
      {!compact && errors.length > 0 && (
        <div className="flex flex-wrap justify-end gap-1">
          {errors.slice(0, CAP).map(([n, c]) => (
            <span key={n} className="rounded-sm bg-bad-soft px-1.5 font-mono text-[10.5px] text-bad">
              {n} <span className="num opacity-70">{c}</span>
            </span>
          ))}
          {errors.length > CAP && <span className="font-mono text-[10.5px] text-faint">+{errors.length - CAP}</span>}
        </div>
      )}
    </div>
  )
}
