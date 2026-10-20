import { Link, NavLink, useParams } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { ArrowRight } from 'lucide-react'
import { $api } from '@/api/hooks'
import { Page, PageHeader } from '@/app/Shell'
import { Loading } from '@/features/auth/guards'
import { StateBadge, auditState, stageWord, Timeline, Stat } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { ProblemBadge } from '@/components/ui/states'
import { countdown, relTime, short, cn } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { AuditSurface } from './Surface'

const terminal = (s: string | undefined) => s === 'delivered' || s === 'failed' || s === 'cancelled'

// The audit's two views, timeline and surface, switch on the header's own
// rule: an underline that meets the divider, nothing boxed. The report is a
// document of its own, reached by the button.
export function AuditTabs({ id, admin = false, aside, className }: { id: string; admin?: boolean; adapter?: string; aside?: React.ReactNode; className?: string }) {
  const base = admin ? `/admin/audits/${id}` : `/app/audits/${id}`
  // The surface map speaks the target's words: programs, instructions
  // and transactions on a chain; hosts, routes and requests on the web.
  const tabs = [
    { to: base, label: 'Timeline', end: true },
    { to: `${base}/surface`, label: 'Surface' },
  ]
  return (
    <nav className={cn('flex items-end gap-6 border-b border-border', className)}>
      {tabs.map((t) => (
        <NavLink
          key={t.to}
          to={t.to}
          end={t.end}
          className={({ isActive }) => cn('-mb-px border-b-2 pb-3 text-sm transition-colors', isActive ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}
        >
          {t.label}
        </NavLink>
      ))}
      {aside && <div className="ml-auto pb-2.5">{aside}</div>}
    </nav>
  )
}

export function AuditSurfaceView() {
  return <AuditDetail view="surface" />
}

// Ledger archetype: the timeline is the content, the summary rail is the facts.
export function AuditDetail({ view = 'timeline' }: { view?: 'timeline' | 'surface' }) {
  const { id = '' } = useParams()
  const q = $api.useQuery(
    'get',
    '/v1/audits/{id}',
    { params: { path: { id } } },
    {
      refetchInterval: (query) => (query.state.data && !terminal(query.state.data.state) ? 15_000 : false),
    },
  )
  const live = !!q.data && !terminal(q.data.state)
  // Countdown ticks by the second; relative times only need the minute.
  const now = useNow(live && q.data?.eta_at ? 1000 : 60_000)
  if (q.isLoading) return <Loading />
  if (q.isError)
    return (
      <Page>
        <ProblemBadge error={q.error} onRetry={() => void q.refetch()} />
      </Page>
    )
  const a = q.data
  if (!a)
    return (
      <Page>
        <StateBadge state="bad">not found</StateBadge>
      </Page>
    )
  const failed = a.state === 'failed'
  const events = a.events ?? []
  const failEvent = events.find((e) => e.kind === 'failed')
  const refunded = typeof failEvent?.payload?.refund_ledger_id === 'string'
  const slotRestored = failEvent?.payload?.slot_restored === true
  return (
    // The surface is a map beside a log; it needs the wide page.
    <Page wide={view === 'surface'}>
      <Helmet>
        <title>{a.repo}</title>
      </Helmet>
      <PageHeader eyebrow="audit" title={<span className="font-mono">{a.repo}</span>} className="mb-0 border-b-0 pb-5">
        <div className="mt-1 flex flex-wrap items-center gap-3 font-mono text-xs text-muted-foreground">
          <span>
            {a.base_sha ? `${short(a.base_sha, 7, 0)}..` : ''}
            {short(a.head_sha, 7, 0)}
          </span>
          <span>{a.coverage}</span>
          {(a.program_ids ?? []).map((p) => (
            <span key={p} title={p}>
              {short(p, 4, 4)}
            </span>
          ))}
          <StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge>
        </div>
      </PageHeader>
      <AuditTabs
        id={a.id}
        adapter={a.adapter}
        className="mb-8"
        aside={
          a.report_id ? (
            <Button size="sm" render={<Link to={`/app/audits/${a.id}/report`} />}>
              Report <ArrowRight className="size-4" />
            </Button>
          ) : undefined
        }
      />

      {view === 'surface' ? (
        <AuditSurface auditId={a.id} reportHref={a.report_id ? `/app/audits/${a.id}/report` : undefined} />
      ) : (
        <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_320px]">
          <section className="flex flex-col gap-6">
            <Timeline state={a.state} resume={a.resume_state} events={events} adapter={a.adapter} className="max-w-[420px] pt-2" />
            <div className="max-w-[420px] border-t border-border pt-4 text-xs text-muted-foreground">Run by our agents with human review.</div>
            {a.state === 'delayed' && a.delayed_reason && (
              <div className="flex max-w-[420px] flex-col gap-2 rounded-md border border-warn/40 bg-warn-soft px-4 py-3">
                <StateBadge state="warn">delayed</StateBadge>
                <span className="text-sm text-warn">{a.delayed_reason}</span>
              </div>
            )}
            {failed && (
              <div className="flex max-w-[420px] flex-col gap-2 rounded-md border border-bad/40 bg-bad-soft px-4 py-3">
                <StateBadge state="bad">failed</StateBadge>
                {a.failed_reason && <span className="text-sm text-bad">{a.failed_reason}</span>}
              </div>
            )}
          </section>
          <aside className="flex flex-col gap-3 lg:sticky lg:top-8 lg:self-start">
            {live ? (
              <Stat label={a.state === 'delayed' ? 'new eta' : 'eta'} value={a.eta_at ? countdown(a.eta_at, now) : '-'} tone={a.state === 'delayed' ? 'warn' : undefined} />
            ) : (
              <Stat label={a.state} value={relTime(a.updated_at, now)} tone={a.state === 'delivered' ? 'ok' : 'bad'} />
            )}
            <Stat label="scope" value={a.scope.lines.toLocaleString('en-US')} hint={`${(a.scope.files ?? []).length} files`} />
            <Stat
              label="cost"
              value={a.cost_kind === 'credit' ? '1 credit' : 'daily slot'}
              className={cn(failed && 'opacity-70')}
              hint={
                failed ? (
                  <StateBadge state={refunded || slotRestored ? 'ok' : 'neutral'} className="h-5 px-1.5 text-[10.5px]">
                    {refunded ? 'refunded' : slotRestored ? 'slot restored' : 'not refunded'}
                  </StateBadge>
                ) : undefined
              }
            />
          </aside>
        </div>
      )}
    </Page>
  )
}
