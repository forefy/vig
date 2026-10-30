import { chainWords, type ChainWords } from '@/lib/chain'
import { cn, relTime } from '@/lib/utils'
import { Mark } from './Mark'
import { StateBadge } from './StateBadge'
import type { components } from '@/api/client'

type AuditEvent = components['schemas']['AuditEvent']

export const pipeline = ['queued', 'claimed', 'cloning', 'programs_verified', 'analysis', 'human_review', 'delivered'] as const
type Stage = (typeof pipeline)[number]
const isStage = (s: unknown): s is Stage => typeof s === 'string' && (pipeline as readonly string[]).includes(s)

const stageWord: Record<string, string> = {
  queued: 'Queued',
  claimed: 'Claimed',
  cloning: 'Cloned',
  programs_verified: 'Programs verified',
  analysis: 'Analysis',
  human_review: 'Human review',
  delivered: 'Delivered',
  delayed: 'Delayed',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

const kindWord: Record<string, string> = {
  programs_recorded: 'programs recorded',
  report_stored: 'report stored',
  reassigned: 'reassigned',
  delayed: 'delayed',
  overdue: 'overdue',
  failed: 'failed',
  token_renewed: 'token renewed',
  surface_mapped: 'surface mapped',
  surface_enriched: 'surface enriched',
  surface_extended: 'surface extended',
  surface_drift: 'program changed',
  cancelled: 'cancelled',
  hidden: 'hidden',
  unhidden: 'unhidden',
  report_revised: 'report revised',
  state_set: 'moved',
}

// The audit timeline. The pipeline stages are the spine; every other
// audit_events row hangs under the stage it happened in. Nothing else appears.
export function Timeline({ state, resume, events, adapter, className }: { state: string; resume?: string; events: AuditEvent[]; adapter?: string; className?: string }) {
  const words = chainWords(adapter)
  const stageName = (s: string) => (s === 'programs_verified' ? words.stageVerified : stageWord[s as keyof typeof stageWord] ?? s)
  const kindName = (k: string) => (k === 'programs_recorded' ? words.recorded : k === 'surface_drift' ? words.drift : kindWord[k] ?? k.replace(/_/g, ' '))
  const effective = state === 'delayed' ? (resume ?? 'queued') : state
  const idx = pipeline.indexOf(effective as Stage)
  const current: Stage = isStage(effective) ? effective : 'queued'

  const stageEvent = new Map<Stage, AuditEvent>()
  const under = new Map<Stage, AuditEvent[]>()
  const failed: AuditEvent[] = []
  const over = state === 'failed' || state === 'cancelled'
  const push = (s: Stage, e: AuditEvent) => under.set(s, [...(under.get(s) ?? []), e])
  for (const e of events) {
    if (e.kind === 'failed' || e.kind === 'cancelled' || e.state === 'failed' || e.state === 'cancelled') {
      // An audit put back on the pipeline keeps its ending on the timeline,
      // under the stage it ended from.
      if (over) failed.push(e)
      else {
        const from = e.payload?.from
        push(isStage(from) ? from : current, e)
      }
    } else if (isStage(e.kind) && e.state === e.kind) {
      const prev = stageEvent.get(e.kind)
      if (prev) push(e.kind, prev)
      stageEvent.set(e.kind, e)
    } else {
      const rs = e.kind === 'delayed' ? e.payload?.resume_state : undefined
      push(isStage(rs) ? rs : isStage(e.state) ? e.state : current, e)
    }
  }

  return (
    <ol className={cn('relative flex flex-col', className)}>
      {pipeline.map((s, i) => {
        const done = i < idx || state === 'delivered'
        const isCurrent = i === idx && state !== 'delivered'
        const ev = stageEvent.get(s)
        const extras = under.get(s) ?? []
        return (
          <li key={s} className="relative grid grid-cols-[20px_1fr] gap-3 pb-5 last:pb-0">
            {i < pipeline.length - 1 && (
              <span className={cn('absolute top-5 left-[9px] h-[calc(100%-10px)] w-px', done ? 'bg-primary/60' : 'bg-border')} />
            )}
            <span className="relative z-10 flex size-5 items-center justify-center">
              {isCurrent ? (
                <Mark size={20} className={cn(state === 'delayed' ? 'text-warn' : 'text-primary', state !== 'delayed' && 'animate-[spin_6s_linear_infinite]')} />
              ) : (
                <span className={cn('size-2 rounded-full', done ? 'bg-primary' : 'border border-border bg-background')} />
              )}
            </span>
            <div className="flex min-w-0 flex-col gap-1">
              <div className="flex items-baseline justify-between gap-3">
                <span className={cn('text-[13.5px]', isCurrent ? 'text-foreground' : done ? 'text-foreground/80' : 'text-muted-foreground')}>
                  {stageName(s)}
                  {isCurrent && state === 'delayed' && <span className="ml-2 text-warn">delayed</span>}
                </span>
                {ev && <span className="num text-xs text-faint">{relTime(ev.created_at)}</span>}
              </div>
              {extras.length > 0 && (
                <ul className="flex flex-col gap-1">
                  {extras.map((e) => (
                    <EventRow key={e.id} event={e} kindName={kindName} words={words} />
                  ))}
                </ul>
              )}
            </div>
          </li>
        )
      })}
      {over && (
        <li className="grid grid-cols-[20px_1fr] gap-3 pt-1">
          <span className="flex size-5 items-center justify-center"><span className="size-2 rounded-full bg-bad" /></span>
          <div className="flex min-w-0 flex-col gap-1">
            <span className="text-[13.5px] text-bad">{stageWord[state]}</span>
            {failed.length > 0 && (
              <ul className="flex flex-col gap-1">
                {failed.map((e) => (
                  <EventRow key={e.id} event={e} kindName={kindName} words={words} />
                ))}
              </ul>
            )}
          </div>
        </li>
      )}
    </ol>
  )
}

// One non-pipeline event: what happened, who did it, the fact it carries.
function EventRow({ event: e, kindName, words }: { event: AuditEvent; kindName: (k: string) => string; words: ChainWords }) {
  const p = e.payload ?? {}
  return (
    <li className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
      <span className="text-muted-foreground">{isStage(e.kind) && !kindWord[e.kind] ? stageWord[e.kind].toLowerCase() : kindName(e.kind)}</span>
      <span className="label-caps rounded-sm border border-border px-1 py-0.5 text-[9.5px]">{e.actor}</span>
      {e.kind === 'delayed' && typeof p.eta_at === 'string' && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">eta {relTime(p.eta_at)}</StateBadge>}
      {(e.kind === 'reassigned' || e.kind === 'state_set') && typeof p.from === 'string' && stageWord[p.from] && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">from {stageWord[p.from].toLowerCase()}</StateBadge>}
      {e.kind === 'state_set' && (typeof p.consume_ledger_id === 'string' || p.slot_taken === true) && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">paid again</StateBadge>}
      {e.kind === 'programs_recorded' && Array.isArray(p.programs) && <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">{p.programs.length} {p.programs.length === 1 ? words.program : words.programs}</StateBadge>}
      {e.kind === 'overdue' && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]">overdue</StateBadge>}
      {e.kind === 'surface_mapped' && typeof p.nodes === 'number' && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">{p.nodes} nodes{typeof p.transactions === 'number' && p.transactions > 0 ? `, ${p.transactions} tx` : ''}</StateBadge>}
      {e.kind === 'surface_enriched' && typeof p.evidence === 'number' && <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">+{p.evidence}</StateBadge>}
      {e.kind === 'surface_extended' && typeof p.added === 'number' && <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">+{p.added} nodes</StateBadge>}
      {e.kind === 'surface_drift' && <StateBadge state={p.kind === 'idl' ? 'warn' : 'bad'} className="h-5 px-1.5 text-[10.5px]">{p.kind === 'idl' ? 'interface changed' : 'upgraded'}{typeof p.program === 'string' ? ` · ${p.program.slice(0, 4)}…${p.program.slice(-4)}` : ''}</StateBadge>}
      {(e.kind === 'failed' || e.kind === 'cancelled') && (typeof p.refund_ledger_id === 'string' || p.slot_restored === true) && <StateBadge state="ok" className="h-5 px-1.5 text-[10.5px]">restored</StateBadge>}
      {e.kind === 'report_revised' && typeof p.revision === 'number' && <StateBadge state="warn" className="h-5 px-1.5 text-[10.5px]" tip={typeof p.reason === 'string' && p.reason ? p.reason : undefined}>rev {p.revision}{typeof p.added === 'number' && typeof p.removed === 'number' ? ` · +${p.added} −${p.removed}` : ''}</StateBadge>}
      <span className="num ml-auto text-[11px] text-faint">{relTime(e.created_at)}</span>
    </li>
  )
}

export { stageWord }
