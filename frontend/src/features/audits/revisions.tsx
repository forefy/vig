import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { StateBadge } from '@/components/primitives'
import { fmtDate } from '@/lib/utils'
import type { components } from '@/api/client'

type Report = components['schemas']['Report']

// The report's version, where the page names it. The latest reason is on
// hover; a press opens the trail: every version, why it was made, when,
// and what it changed against the one before.
export function Revisions({ report, className }: { report: Report; className?: string }) {
  if (report.revision <= 1) return null
  const revs = report.revisions ?? []
  const latest = revs[0]
  return (
    <Popover>
      <PopoverTrigger className={className} aria-label={`revision ${report.revision}`}>
        <StateBadge state="warn" tip={latest?.reason || undefined}>
          rev {report.revision}
        </StateBadge>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[380px] p-0">
        <ol className="divide-y divide-border">
          {revs.map((r) => (
            <li key={r.revision} className="flex flex-col gap-1 px-3 py-2 text-[12.5px]">
              <div className="flex items-center gap-2">
                <StateBadge state={r.revision === report.revision ? 'warn' : 'neutral'} className="h-5 px-1.5 text-[10.5px]">rev {r.revision}</StateBadge>
                {(r.added > 0 || r.removed > 0) && <span className="num text-[11px] text-muted-foreground">+{r.added} −{r.removed}</span>}
                <span className="num ml-auto text-[11px] text-faint">{fmtDate(r.at)}</span>
              </div>
              {r.reason && <span className="text-foreground/80">{r.reason}</span>}
            </li>
          ))}
        </ol>
      </PopoverContent>
    </Popover>
  )
}
