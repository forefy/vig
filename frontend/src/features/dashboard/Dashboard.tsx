import { Link } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { Plus, ArrowUpRight } from 'lucide-react'
import { $api } from '@/api/hooks'
import { useMe } from '@/features/auth/useMe'
import { Page, PageHeader } from '@/app/Shell'
import { Stat, StateBadge, auditState, stageWord, Empty, Mark } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { SkeletonRows, ProblemBadge } from '@/components/ui/states'
import { countdown, relTime, short } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { TweetClaimCard } from '@/features/billing/TweetClaimCard'

const terminal = (s: string) => s === 'delivered' || s === 'failed' || s === 'cancelled'

// Board archetype: what needs attention, then the list.
export function Dashboard() {
  const { me } = useMe()
  const audits = $api.useQuery('get', '/v1/audits', {}, {
    refetchInterval: (query) => ((query.state.data?.items ?? []).some((a) => !terminal(a.state)) ? 15_000 : false),
  })
  const items = audits.data?.items ?? []
  const active = items.filter((a) => !terminal(a.state))
  const delivered = items.filter((a) => a.state === 'delivered')
  const plan = me?.plan
  // A countdown is on screen when a run is pending or a plan slot is closed.
  const hasCountdown = active.some((a) => !!a.eta_at) || !!plan
  const now = useNow(hasCountdown ? 1000 : 60_000)
  const credits = me?.credits ?? 0
  const slotOpen = plan ? new Date(plan.next_slot_at).getTime() <= now : false

  return (
    <Page>
      <Helmet>
        <title>Dashboard</title>
      </Helmet>
      <PageHeader
        eyebrow="dashboard"
        title={me?.user.display_name || 'Dashboard'}
        aside={
          <Button render={<Link to="/app/audits/new" />}>
            <Plus className="size-4" /> New audit
          </Button>
        }
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="credits" value={credits} tone={credits === 0 && !plan ? 'warn' : undefined} hint={plan ? undefined : '1 per audit'} />
        {plan ? (
          <Stat label="next run" value={slotOpen ? 'now' : countdown(plan.next_slot_at, now)} tone={slotOpen ? 'ok' : undefined} hint={`plan ends ${relTime(plan.ends_at, now)}`} />
        ) : (
          <Stat label="plan" value="none" hint={<Link to="/app/billing" className="text-primary hover:underline">plans</Link>} />
        )}
        <Stat label="in progress" value={audits.isSuccess ? active.length : '-'} />
        <Stat label="delivered" value={audits.isSuccess ? delivered.length : '-'} />
      </div>

      {credits === 0 && !plan && (
        <div className="mt-6">
          <TweetClaimCard />
        </div>
      )}

      <section className="mt-10 flex flex-col gap-3">
        <div className="flex items-center justify-between">
          <span className="label-caps">recent</span>
          <Link to="/app/audits" className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            all <ArrowUpRight className="size-3" />
          </Link>
        </div>
        {audits.isLoading ? (
          <SkeletonRows rows={3} className="gap-px" />
        ) : audits.isError ? (
          <ProblemBadge error={audits.error} onRetry={() => void audits.refetch()} />
        ) : items.length === 0 ? (
          <Empty title="No audits yet" action={<Button size="sm" variant="outline" render={<Link to="/app/audits/new" />}>Start one</Button>} />
        ) : (
          <div className="divide-y divide-border rounded-md border border-border bg-card">
            {items.slice(0, 6).map((a) => {
              const ids = a.program_ids ?? []
              return (
                <Link key={a.id} to={`/app/audits/${a.id}`} className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-4 px-4 py-3 hover:bg-accent/60">
                  <Mark size={16} className={a.state === 'delivered' ? 'text-primary' : a.state === 'failed' || a.state === 'cancelled' ? 'text-bad' : a.state === 'delayed' ? 'text-warn' : 'animate-[spin_6s_linear_infinite] text-muted-foreground'} />
                  <span className="min-w-0">
                    <span className="block truncate font-mono text-sm">{a.repo}</span>
                    <span className="block font-mono text-[11px] text-muted-foreground">{short(a.head_sha, 7, 0)} · {ids.length} program{ids.length === 1 ? '' : 's'}</span>
                  </span>
                  <StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge>
                  <span className="num w-20 text-right text-xs text-faint">{terminal(a.state) ? relTime(a.updated_at, now) : a.eta_at ? countdown(a.eta_at, now) : ''}</span>
                </Link>
              )
            })}
          </div>
        )}
      </section>
    </Page>
  )
}
