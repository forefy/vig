import { useState } from 'react'
import { Link } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { Plus } from 'lucide-react'
import { $api } from '@/api/hooks'
import { Page, PageHeader } from '@/app/Shell'
import { StateBadge, auditState, stageWord, Empty } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SkeletonRows, ProblemBadge } from '@/components/ui/states'
import { countdown, relTime, short, cn } from '@/lib/utils'
import { useNow } from '@/lib/useNow'

type Filter = 'all' | 'active' | 'delivered'
const terminal = (s: string) => s === 'delivered' || s === 'failed' || s === 'cancelled'

// Ledger archetype: one table, filters as tabs.
export function AuditsList() {
  const audits = $api.useQuery('get', '/v1/audits', {}, {
    refetchInterval: (query) => ((query.state.data?.items ?? []).some((a) => !terminal(a.state)) ? 15_000 : false),
  })
  const [filter, setFilter] = useState<Filter>('all')
  const all = audits.data?.items ?? []
  const hasCountdown = all.some((a) => !terminal(a.state) && !!a.eta_at)
  const now = useNow(hasCountdown ? 1000 : 60_000)
  const items = all.filter((a) => (filter === 'all' ? true : filter === 'active' ? !terminal(a.state) : a.state === 'delivered'))
  return (
    <Page>
      <Helmet>
        <title>Audits</title>
      </Helmet>
      <PageHeader
        eyebrow="audits"
        title="Audits"
        aside={
          <Button render={<Link to="/app/audits/new" />}>
            <Plus className="size-4" /> New audit
          </Button>
        }
      >
        <Tabs value={filter} onValueChange={(v) => setFilter(v as Filter)} className="mt-2">
          <TabsList>
            <TabsTrigger value="all">All</TabsTrigger>
            <TabsTrigger value="active">Active</TabsTrigger>
            <TabsTrigger value="delivered">Delivered</TabsTrigger>
          </TabsList>
        </Tabs>
      </PageHeader>
      {audits.isLoading ? (
        <SkeletonRows rows={5} />
      ) : audits.isError ? (
        <ProblemBadge error={audits.error} onRetry={() => void audits.refetch()} />
      ) : items.length === 0 ? (
        <Empty title={filter === 'all' ? 'No audits yet' : `No ${filter} audits`} action={<Button size="sm" variant="outline" render={<Link to="/app/audits/new" />}>Start an audit</Button>} />
      ) : (
        <div className="overflow-x-auto rounded-md border border-border bg-card">
          <table className="w-full min-w-[640px] text-sm">
            <thead>
              <tr className="label-caps border-b border-border text-left">
                <th className="px-4 py-2.5 font-medium">repo</th>
                <th className="px-4 py-2.5 font-medium">commit</th>
                <th className="px-4 py-2.5 font-medium">coverage</th>
                <th className="px-4 py-2.5 font-medium">state</th>
                <th className="px-4 py-2.5 text-right font-medium">eta</th>
                <th className="px-4 py-2.5 text-right font-medium">findings</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {items.map((a) => (
                <tr key={a.id} className="hover:bg-accent/60">
                  <td className="px-4 py-2.5 font-mono">
                    <Link to={`/app/audits/${a.id}`} className="hover:underline">{a.repo}</Link>
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{a.base_sha ? `${short(a.base_sha, 7, 0)}..` : ''}{short(a.head_sha, 7, 0)}</td>
                  <td className="px-4 py-2.5 text-xs text-muted-foreground">{a.coverage}</td>
                  <td className="px-4 py-2.5"><StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge></td>
                  <td className={cn('num px-4 py-2.5 text-right text-xs', a.state === 'delayed' ? 'text-warn' : 'text-muted-foreground')}>
                    {terminal(a.state) ? relTime(a.updated_at, now) : a.eta_at ? countdown(a.eta_at, now) : ''}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    {a.report_id ? (
                      <Link to={`/app/audits/${a.id}/report`} className="text-xs text-primary hover:underline">report</Link>
                    ) : (
                      <span className="text-xs text-faint">-</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  )
}
