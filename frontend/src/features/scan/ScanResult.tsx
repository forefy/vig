import { sourceSummaryWords } from '@/lib/chain'
import { Link, useParams } from 'react-router'
import { Seo } from '@/components/primitives/Seo'
import { severityState } from '@/components/primitives'
import { $api } from '@/api/hooks'
import { Loading } from '@/features/auth/guards'
import { Panel, panelAction, StateBadge, Meter, Mark } from '@/components/primitives'
import { Tree } from '@/features/audits/Tree'
import { Button } from '@/components/ui/button'
import { ProblemBadge } from '@/components/ui/states'
import { useMe } from '@/features/auth/useMe'
import { short } from '@/lib/utils'

// Anonymous scans per day. The Scan type carries what is left, not the cap.
const SCANS_PER_DAY = 5

// Scan result: the pre-flight facts, and one thing to do next.
export function ScanResult() {
  const { token = '' } = useParams()
  const { me } = useMe()
  const q = $api.useQuery('get', '/v1/scans/{token}', { params: { path: { token } } }, {
    refetchInterval: (query) => (query.state.data?.state === 'queued' || query.state.data?.state === 'running' ? 2000 : false),
  })
  if (q.isLoading) return <Loading />
  if (q.isError) return <div className="mx-auto max-w-[1000px] px-6 py-10 md:px-10"><ProblemBadge error={q.error} onRetry={() => void q.refetch()} /></div>
  const s = q.data
  if (!s) return <div className="flex justify-center px-10 py-20"><StateBadge state="neutral">scan expired</StateBadge></div>
  const r = s.result
  const next = `/app/audits/new?repo=${encodeURIComponent(s.repo)}&commit=${s.commit}${s.program_id ? `&programs=${s.program_id}` : ''}`
  return (
    <div className="mx-auto max-w-[1000px] px-6 pb-20 md:px-10">
      <Seo title={`Scan ${s.repo}`} path={`/s/${s.token}`} noindex />
      <header className="flex flex-wrap items-end justify-between gap-4 border-b border-border py-8">
        <div className="flex min-w-0 flex-col gap-2">
          <span className="label-caps">scan</span>
          <h1 className="truncate font-mono text-xl">{s.repo}</h1>
          <span className="font-mono text-xs text-muted-foreground">{short(s.commit, 7, 0)}{s.program_id && <> · {short(s.program_id, 4, 4)}</>}</span>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <StateBadge state={s.state === 'done' ? 'ok' : s.state === 'failed' ? 'bad' : 'pending'}>{s.state}</StateBadge>
          <Meter value={s.remaining_today} max={SCANS_PER_DAY} kind="remaining" label="scans left today" className="w-44" />
        </div>
      </header>

      {s.state !== 'done' && s.state !== 'failed' && (
        <div className="flex flex-col items-center justify-center gap-5 py-16">
          <Mark size={28} className="animate-[spin_6s_linear_infinite] text-primary" />
          <div className="flex flex-wrap items-center justify-center gap-2">
            <StateBadge state="pending">repo {short(s.commit, 7, 0)}</StateBadge>
            <StateBadge state="pending">chain{s.program_id ? ` ${short(s.program_id, 4, 4)}` : ''}</StateBadge>
          </div>
        </div>
      )}
      {s.state === 'failed' && !r && (
        <div className="flex items-center justify-center py-16">
          <StateBadge state="bad">scan failed</StateBadge>
        </div>
      )}

      {r && (
        <div className="grid grid-cols-1 gap-8 py-8 lg:grid-cols-[minmax(0,1fr)_360px]">
          <div className="flex min-w-0 flex-col gap-8">
            {(r.panels ?? []).map((p) => (
              <section key={p.subject} className="flex flex-col gap-3">
                <span className="label-caps">{short(p.subject, 6, 6)}</span>
                <Panel panel={p} onAction={(action, row) => void panelAction(action, row)} />
              </section>
            ))}
            <section className="flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <span className="label-caps">scope</span>
                <span className="num text-xs text-muted-foreground">{r.code.toLocaleString('en-US')} code · {r.lines.toLocaleString('en-US')} lines</span>
              </div>
              <Tree nodes={r.tree} readOnly />
            </section>
            {r.source && r.source.programs > 0 && (
              <section className="flex flex-col gap-3">
                <span className="label-caps">source</span>
                <div className="flex flex-wrap gap-2">
                  {sourceSummaryWords(r.adapter).map(({ key, word, tone }) => {
                    const n = (r.source as unknown as Record<string, number>)[key] ?? 0
                    return <StateBadge key={key} state={tone && n > 0 ? tone : 'neutral'}>{n} {word}</StateBadge>
                  })}
                </div>
              </section>
            )}
            {r.advisories && r.advisories.length > 0 && (
              <section className="flex flex-col gap-3">
                <span className="label-caps">advisories</span>
                <div className="divide-y divide-border rounded-md border border-border bg-card">
                  {r.advisories.map((a) => (
                    <a key={a.id} href={a.url} target="_blank" rel="noreferrer" className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3.5 py-2 text-sm hover:bg-accent">
                      <StateBadge state={severityState[a.severity] ?? 'warn'}>{a.id}</StateBadge>
                      <span className="font-mono text-xs">{a.package} {a.version}</span>
                      <span className="text-xs text-muted-foreground">{a.severity}</span>
                    </a>
                  ))}
                </div>
              </section>
            )}
          </div>
          <aside className="lg:sticky lg:top-8 lg:self-start">
            <div className="flex flex-col gap-4 rounded-md border border-border bg-card p-5">
              <span className="label-caps">next</span>
              <span className="num text-2xl">1 audit</span>
              <div className="flex flex-wrap gap-2">
                <StateBadge state="neutral">{short(s.commit, 7, 0)}</StateBadge>
                {s.program_id && <StateBadge state="neutral">{short(s.program_id, 4, 4)}</StateBadge>}
                <StateBadge state="neutral">24h</StateBadge>
              </div>
              <Button size="lg" className="w-full" render={<Link to={me ? next : '/login'} state={{ from: next }} />}>
                {me ? 'Start an audit' : 'Sign in to audit'}
              </Button>
            </div>
          </aside>
        </div>
      )}
    </div>
  )
}
