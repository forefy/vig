import { useEffect, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { hostOf, placeOf, routeOf, shortRoute } from '@/lib/web'
import { Helmet } from 'react-helmet-async'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowRight, ChevronDown, ChevronRight, Download, Globe, GitCommitHorizontal } from 'lucide-react'
import { $api } from '@/api/hooks'
import { api } from '@/api/client'
import { Page, PageHeader } from '@/app/Shell'
import { Loading } from '@/features/auth/guards'
import { Pin, StateBadge, severityState, severityInk, severityDot, MethodsStrip, EvidenceBadge, FindingCode, FactGroup, FactRow, Prose, Reference, ExhaustionDots, ExhaustionList } from '@/components/primitives'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { relTime } from '@/lib/utils'
import { ProblemBadge } from '@/components/ui/states'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { chainWords } from '@/lib/chain'
import type { ChainWords } from '@/lib/chain'
import { QueryLine, countFacet, matches, parseQuery, formatQuery } from '@/components/primitives/query'
import type { Facet as FacetOf, Query } from '@/components/primitives/query'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type Finding = components['schemas']['Finding']
type Report = components['schemas']['Report']

// A published report serves its captures through the public slug route (works
// for anyone); an unpublished one only through the owner-scoped audit route.
function visualSrcFor(report: Report): (id: string) => string {
  const base = report.published_slug ? `/v1/reports/${report.published_slug}` : `/v1/audits/${report.audit_id}`
  return (id) => `${base}/visuals/${id}`
}
const order = ['critical', 'high', 'medium', 'low', 'info'] as const
// The finding names a program; the pin is what says how it matched.
function programTip(pin: { programs?: { id: string; slot: number; match: string }[] | null }, id: string, words: ChainWords) {
  const p = (pin.programs ?? []).find((x) => x.id === id)
  return p ? [p.match.replace('_', '-'), p.slot > 0 ? `${words.height} ${p.slot.toLocaleString('en-US')}` : ''].filter(Boolean).join(' \u00b7 ') : id
}

// The status control: a select; archiving opens the reason first, since a
// finding judged not relevant is a lesson for the next report and the
// reason is the lesson.
function StatusPick({ finding, onStatus }: { finding: Finding; onStatus: (f: Finding, status: Finding['status'], reason?: string) => Promise<void> | void }) {
  const [archiving, setArchiving] = useState(false)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <>
      <Select value={finding.status} onValueChange={(v) => { if (!v || v === finding.status) return; if (v === 'archived') setArchiving(true); else void onStatus(finding, v as Finding['status']) }}>
        <SelectTrigger size="sm" className="w-36" aria-label="Finding status"><SelectValue /></SelectTrigger>
        <SelectContent>
          {(Object.keys(statusWord) as Finding['status'][]).map((s) => (
            <SelectItem key={s} value={s}>{statusWord[s]}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Dialog open={archiving} onOpenChange={(o) => { if (!o) { setArchiving(false); setReason('') } }}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">archive</DialogTitle>
          <div className="flex items-center gap-2">
            <StateBadge state={severityState[finding.severity]}>{finding.severity}</StateBadge>
            <span className="truncate text-[13px] text-foreground/80">{finding.title}</span>
          </div>
          <Input value={reason} onChange={(e) => setReason(e.target.value.slice(0, 140))} placeholder="why it is not relevant" aria-label="Reason" className="h-9" maxLength={140} autoFocus />
          <Button
            size="sm"
            className="w-fit"
            disabled={reason.trim().length < 3 || busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onStatus(finding, 'archived', reason.trim())
                setArchiving(false)
                setReason('')
              } catch {
                /* the toast said it */
              } finally {
                setBusy(false)
              }
            }}
          >
            Archive
          </Button>
        </DialogContent>
      </Dialog>
    </>
  )
}

// The severity control: a select that opens the reason once a different
// rating is picked, and only fires with the reason filled. The current
// rating stays until the server has the change.
function Rerate({ finding, onSeverity }: { finding: Finding; onSeverity: (f: Finding, severity: Finding['severity'], reason: string) => Promise<void> }) {
  const [to, setTo] = useState<Finding['severity'] | null>(null)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const close = () => { setTo(null); setReason('') }
  return (
    <>
      <Select value={finding.severity} onValueChange={(v) => v && v !== finding.severity && setTo(v as Finding['severity'])}>
        <SelectTrigger size="sm" className="w-28" aria-label="Finding severity"><SelectValue /></SelectTrigger>
        <SelectContent>
          {order.map((s) => (
            <SelectItem key={s} value={s}>{s}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Dialog open={to !== null} onOpenChange={(o) => !o && close()}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">{to && order.indexOf(to) < order.indexOf(finding.severity) ? 'upgrade' : 'downgrade'}</DialogTitle>
          <div className="flex items-center gap-2">
            <StateBadge state={severityState[finding.severity]}>{finding.severity}</StateBadge>
            <ArrowRight className="size-3 text-faint" aria-hidden="true" />
            {to && <StateBadge state={severityState[to]}>{to}</StateBadge>}
          </div>
          <Input value={reason} onChange={(e) => setReason(e.target.value.slice(0, 140))} placeholder="reason" aria-label="Reason" className="h-9" maxLength={140} autoFocus />
          <Button
            size="sm"
            className="w-fit"
            disabled={reason.trim().length < 3 || busy || !to}
            onClick={async () => {
              if (!to) return
              setBusy(true)
              try {
                await onSeverity(finding, to, reason.trim())
                close()
              } catch {
                /* the toast said it */
              } finally {
                setBusy(false)
              }
            }}
          >
            {to && order.indexOf(to) < order.indexOf(finding.severity) ? 'Upgrade' : 'Downgrade'}
          </Button>
        </DialogContent>
      </Dialog>
    </>
  )
}

const exploitableState: Record<string, 'bad' | 'warn' | 'neutral'> = {
  anyone: 'bad',
  any_user: 'bad',
  any_signer: 'bad',
  internal: 'warn',
  account_owner: 'warn',
  api_key: 'warn',
  internal_network: 'warn',
  other_program: 'warn',
  admin: 'warn',
  upgrade_authority: 'warn',
  none: 'neutral',
}

const statusWord = { open: 'open', fixed: 'fixed', acknowledged: 'acknowledged', disputed: 'disputed', archived: 'archived' } as const
const statusState: Record<string, 'ok' | 'warn' | 'neutral' | 'pending'> = { open: 'neutral', fixed: 'ok', acknowledged: 'neutral', disputed: 'warn', archived: 'pending' }

// Document archetype: pinning header, findings stream, side index.
export function ReportPage() {
  const { id = '' } = useParams()
  const qc = useQueryClient()
  const q = $api.useQuery('get', '/v1/audits/{id}/report', { params: { path: { id } } })
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['get', '/v1/audits/{id}/report'] })
    void qc.invalidateQueries({ queryKey: ['get', '/v1/reports/{slug}'] })
  }
  const publish = $api.useMutation('post', '/v1/audits/{id}/report/publish', { onSuccess: invalidate, onError: () => toast.error('Could not publish') })
  const unpublish = $api.useMutation('delete', '/v1/audits/{id}/report/publish', { onSuccess: invalidate, onError: () => toast.error('Could not unpublish') })
  if (q.isLoading) return <Loading />
  if (q.isError) return <Page><ProblemBadge error={q.error} onRetry={() => void q.refetch()} /></Page>
  const r = q.data
  if (!r) return <Page><StateBadge state="pending">no report yet</StateBadge></Page>
  const doc = r.document
  const pin = doc.report.pin
  const isPublic = !!r.published_slug
  const reaudit = `/app/audits/new?${new URLSearchParams({
    from: id,
    repo: pin.repo,
    base: pin.commit,
    programs: (pin.programs ?? []).map((p) => p.id).join(','),
    coverage: 'diff',
  }).toString()}`
  return (
    <Page wide>
      <Helmet>
        <title>Report {pin.repo}</title>
      </Helmet>
      <PageHeader
        eyebrow="report"
        title={<span className="font-mono">{pin.repo}{r.revision > 1 && <StateBadge state="warn" className="ml-3 align-middle">rev {r.revision}</StateBadge>}</span>}
        aside={
          <>
            <Button variant="outline" size="sm" render={<Link to={reaudit} />}>
              <GitCommitHorizontal className="size-4" /> Audit new commit
            </Button>
            <Button variant="outline" size="sm" onClick={() => void downloadJSON(id, pin.repo)}>
              <Download className="size-4" /> JSON
            </Button>
            {r.is_owner && (
              <label className="flex h-7 items-center gap-2 rounded-md border border-border px-2.5 text-xs">
                <Globe className={cn('size-3.5', isPublic ? 'text-ok' : 'text-muted-foreground')} />
                {isPublic ? (
                  <a href={`/r/${r.published_slug}`} target="_blank" rel="noreferrer" className="font-mono hover:underline">/r/{r.published_slug}</a>
                ) : (
                  <span className="text-muted-foreground">private</span>
                )}
                <Switch
                  checked={isPublic}
                  disabled={publish.isPending || unpublish.isPending}
                  onCheckedChange={(v) => (v ? publish.mutate({ params: { path: { id } } }) : unpublish.mutate({ params: { path: { id } } }))}
                />
              </label>
            )}
          </>
        }
      >
        <div className="mt-2 flex flex-col gap-2">
          <Pin pin={pin} adapter={r.adapter} />
          <MethodsStrip methods={doc.report.methods} />
        </div>
      </PageHeader>
      <ReportBody
        report={r}
        surfaceHref={`/app/audits/${id}/surface`}
        onStatus={async (f, status, reason) => {
          if (!f.id) return
          const res = await api.PATCH('/v1/audits/{id}/report/findings/{fid}', { params: { path: { id, fid: f.id } }, body: { status, reason } })
          if (res.error) {
            toast.error('Could not update')
            throw new Error('status failed')
          }
          invalidate()
        }}
        onSeverity={async (f, severity, reason) => {
          if (!f.id) return
          const res = await api.PATCH('/v1/audits/{id}/report/findings/{fid}/severity', { params: { path: { id, fid: f.id } }, body: { severity, reason } })
          if (res.error) {
            toast.error('Could not re-rate')
            throw new Error('re-rate failed')
          }
          invalidate()
        }}
      />
    </Page>
  )
}

// snake folds an instruction name into the surface's node spelling.
const snake = (s: string) => s.replace(/[A-Z]/g, (m, i) => (i > 0 ? '_' : '') + m.toLowerCase())

export function ReportBody({ report, onStatus, onSeverity, surfaceHref }: { report: Report; onStatus?: (f: Finding, status: Finding['status'], reason?: string) => Promise<void> | void; onSeverity?: (f: Finding, severity: Finding['severity'], reason: string) => Promise<void>; surfaceHref?: string }) {
  const doc = report.document
  const reportPin = doc.report.pin
  const words = chainWords(report.adapter)
  const all = doc.findings ?? []
  const [params, setParams] = useSearchParams()
  // The query lives in the URL, so a link carries it; the browser also
  // remembers the last one per audit, so reopening the report without a
  // link brings it back. Clearing the line forgets it.
  const remembered = `vig.report.q.${report.audit_id}`
  useEffect(() => {
    const inUrl = params.get('q')
    try {
      if (inUrl) {
        localStorage.setItem(remembered, inUrl)
        return
      }
      const last = localStorage.getItem(remembered)
      if (last) {
        const p = new URLSearchParams(params)
        p.set('q', last)
        setParams(p, { replace: true })
      }
    } catch { /* storage may be unavailable */ }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [remembered])
  const query = parseQuery(params.get('q') ?? '')
  const setQuery = (next: Query) => {
    const p = new URLSearchParams(params)
    const q = next.items.length ? formatQuery(next) : ''
    if (q) p.set('q', q)
    else p.delete('q')
    setParams(p, { replace: true })
    try {
      if (q) localStorage.setItem(remembered, q)
      else localStorage.removeItem(remembered)
    } catch { /* storage may be unavailable */ }
  }
  const facets = facetsOf(all, words)
  const archived = all.filter((f) => f.status === 'archived')
  // What a [[Title]] in prose can point at: every finding of the report,
  // archived ones included, since they stay on the page.
  const links = all.map((f) => ({ ordinal: f.ordinal, title: f.title }))
  const findings = all.filter((f) => f.status !== 'archived' && matches(f, facets, query, findingText))
  const groups = order.map((s) => ({ s, items: findings.filter((f) => f.severity === s) })).filter((g) => g.items.length)
  // Every correction on this report, newest first: what moved, why, who.
  const changes = all
    .flatMap((f) => [
      ...(f.severity_changes ?? []).map((c) => ({ f, kind: 'severity' as const, from: c.from, to: c.to, reason: c.reason, by: c.by, at: c.at })),
      ...(f.status_changes ?? []).map((c) => ({ f, kind: 'status' as const, from: c.from, to: c.to, reason: c.reason ?? '', by: c.by, at: c.at })),
    ])
    .sort((a, b) => b.at.localeCompare(a.at))
  const [active, setActive] = useState<number | null>(all[0]?.ordinal ?? null)
  // The finding whose exhaustion items are open under its header.
  const [exhausted, setExhausted] = useState<number | null>(null)
  const stream = useRef<HTMLDivElement>(null)

  // The side index follows the reader: the topmost finding in view is active.
  useEffect(() => {
    const root = stream.current
    if (!root || typeof IntersectionObserver === 'undefined') return
    const articles = Array.from(root.querySelectorAll<HTMLElement>('article[data-ordinal]'))
    if (articles.length === 0) return
    const visible = new Map<number, number>()
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const n = Number((e.target as HTMLElement).dataset.ordinal)
          if (e.isIntersecting) visible.set(n, e.boundingClientRect.top)
          else visible.delete(n)
        }
        if (visible.size === 0) return
        let top: number | null = null
        let best = Infinity
        for (const [n, y] of visible) if (y < best) { best = y; top = n }
        if (top !== null) setActive(top)
      },
      { rootMargin: '-10% 0px -60% 0px', threshold: [0, 0.25] },
    )
    articles.forEach((a) => io.observe(a))
    return () => io.disconnect()
  }, [findings.length])

  return (
    <div className="grid grid-cols-1 gap-10 lg:grid-cols-[minmax(0,1fr)_260px]">
      <div ref={stream} className="flex flex-col gap-10">
        <SummaryStrip summary={doc.report.summary} />
        {(doc.report.known ?? []).length > 0 && <KnownStrip known={doc.report.known ?? []} repo={reportPin.repo} commit={reportPin.commit} />}
        {/* The report's own log, folded: every re-rating and status move on
            any finding, newest first. The strip says how many; a click opens
            them. */}
        {changes.length > 0 && (
          <details className="group rounded-md border border-border bg-card">
            <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-[12px] text-muted-foreground [&::-webkit-details-marker]:hidden">
              <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
              <span className="label-caps">changes</span>
              <span className="num">{changes.length}</span>
              <span className="ml-auto truncate font-mono text-[11px] text-faint">{relTime(changes[0]?.at ?? '')}</span>
            </summary>
            <ol className="divide-y divide-border border-t border-border">
              {changes.map((c, i) => (
                <li key={i} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-[12.5px]">
                  <a href={`#f-${c.f.ordinal}`} className="num text-muted-foreground hover:text-foreground">{String(c.f.ordinal).padStart(2, '0')}</a>
                  <span className="truncate text-foreground/80">{c.f.title}</span>
                  <StateBadge state={c.kind === 'severity' ? severityState[c.from] : (statusState[c.from] ?? 'neutral')} className="h-5 px-1.5 text-[10.5px]">{c.from}</StateBadge>
                  <ArrowRight className="size-3 text-faint" aria-hidden="true" />
                  <StateBadge state={c.kind === 'severity' ? severityState[c.to] : (statusState[c.to] ?? 'neutral')} className="h-5 px-1.5 text-[10.5px]">{c.to}</StateBadge>
                  {c.reason && <span className="text-muted-foreground">{c.reason}</span>}
                  <span className="ml-auto font-mono text-[11px] text-faint">{c.by} · {relTime(c.at)}</span>
                </li>
              ))}
            </ol>
          </details>
        )}
        {all.length > 0 && <QueryLine facets={facets} all={all} query={query} onChange={setQuery} shown={findings.length} total={all.length} textOf={findingText} />}
        {findings.length === 0 && all.length > 0 && (
          <div className="flex items-center justify-center rounded-md border border-dashed border-border px-6 py-14">
            <StateBadge state="neutral">0 of {all.length} shown</StateBadge>
          </div>
        )}
        {all.length === 0 && (
          <div className="flex items-center justify-center rounded-md border border-dashed border-border px-6 py-14">
            <StateBadge state="ok">0 findings</StateBadge>
          </div>
        )}
        {groups.map((g) => (
          <section key={g.s} className="flex flex-col gap-4">
            <StateBadge state={severityState[g.s]} className="self-start">{g.s}</StateBadge>
            {g.items.map((f) => (
              <article
                key={f.id ?? f.ordinal}
                id={`f-${f.ordinal}`}
                data-ordinal={f.ordinal}
                tabIndex={-1}
                className={cn('flex scroll-mt-6 flex-col gap-4 rounded-md border bg-card/50 p-5 outline-none', f.status === 'fixed' ? 'border-ok/30' : f.status === 'disputed' ? 'border-warn/30' : 'border-border')}
              >
                <header className="flex flex-wrap items-start justify-between gap-3">
                  {/* flex-1 so the status keeps its corner instead of being
                      pushed under the chips when they run long. */}
                  <div className="flex min-w-0 flex-1 flex-col gap-2">
                    <h3 className="text-[15px] font-medium">
                      <span className="num mr-2 text-muted-foreground">{String(f.ordinal).padStart(2, '0')}</span>
                      {f.title}
                    </h3>
                    {/* No file:line here: a finding can point at several
                        places and the one on screen changes with the tab, so
                        the location is printed with the code it belongs to.
                        What stays here is the scope the finding sits in and
                        what established it, in the report header's own shape.
                        Every chip carries its detail on hover. */}
                    <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5">
                      {(f.location.program_id || f.location.package || f.location.instruction) && (
                        <FactGroup label="scope" dense>
                          {f.location.instruction && (
                            <FactRow kind="function" dense>
                              {surfaceHref ? (
                                <Link to={`${surfaceHref}#n-ix:${f.location.program_id ?? ''}:${snake(f.location.instruction)}`} className="hover:underline">
                                  <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]" tip={`${f.location.file}:${f.location.line_start}`}>{f.location.instruction}</StateBadge>
                                </Link>
                              ) : (
                                <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]" tip={`${f.location.file}:${f.location.line_start}`}>{f.location.instruction}</StateBadge>
                              )}
                            </FactRow>
                          )}
                          {f.location.package && (
                            <FactRow kind="package" dense>
                              <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]" tip={f.location.file.slice(0, f.location.file.indexOf(f.location.package) + f.location.package.length)}>{f.location.package}</StateBadge>
                            </FactRow>
                          )}
                          {f.location.program_id && (
                            <FactRow kind={words.program} dense>
                              <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]" tip={programTip(reportPin, f.location.program_id, words)}>{f.location.program_id}</StateBadge>
                            </FactRow>
                          )}
                        </FactGroup>
                      )}
                      {f.exploitable_by && (
                        <FactGroup label="exploitable" dense>
                          <FactRow kind="by" dense>
                            <StateBadge state={exploitableState[f.exploitable_by]} className="h-5 px-1.5 text-[10.5px]">
                              {words.exploitable[f.exploitable_by] ?? f.exploitable_by.replace(/_/g, ' ')}
                            </StateBadge>
                          </FactRow>
                        </FactGroup>
                      )}
                      {f.evidence && (
                        <FactGroup label="methods" dense>
                          <FactRow kind={f.evidence.tool ? 'tools' : 'evidence'} dense>
                            <EvidenceBadge evidence={f.evidence} />
                          </FactRow>
                        </FactGroup>
                      )}
                      {f.carried_from_id && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">carried</StateBadge>}
                      {f.exhaustion && f.exhaustion.length > 0 && (
                        <FactGroup label="ruled out" dense>
                          <ExhaustionDots answers={f.exhaustion} open={exhausted === f.ordinal} onToggle={() => setExhausted(exhausted === f.ordinal ? null : f.ordinal)} />
                        </FactGroup>
                      )}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {onSeverity && f.id && <Rerate finding={f} onSeverity={onSeverity} />}
                    {onStatus && f.id ? (
                      <StatusPick finding={f} onStatus={onStatus} />
                    ) : (
                      <StateBadge state={statusState[f.status] ?? 'neutral'}>{f.status}</StateBadge>
                    )}
                  </div>
                </header>
                {exhausted === f.ordinal && f.exhaustion && <ExhaustionList answers={f.exhaustion} />}
                {/* Every re-rating, oldest first: what it was, what it became,
                    why, who, when. Append-only on the server; a revision that
                    rates differently is one more row here. */}
                {changes.some((c) => c.f === f) && (
                  <ol className="flex flex-col gap-1 border-l border-border pl-3">
                    {changes.filter((c) => c.f === f).slice().reverse().map((c, i) => (
                      <li key={i} className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted-foreground">
                        <StateBadge state={c.kind === 'severity' ? severityState[c.from] : (statusState[c.from] ?? 'neutral')} className="h-5 px-1.5 text-[10.5px]">{c.from}</StateBadge>
                        <ArrowRight className="size-3 text-faint" aria-hidden="true" />
                        <StateBadge state={c.kind === 'severity' ? severityState[c.to] : (statusState[c.to] ?? 'neutral')} className="h-5 px-1.5 text-[10.5px]">{c.to}</StateBadge>
                        {c.reason && <span className="text-foreground/80">{c.reason}</span>}
                        <span className="font-mono text-[11px]">{c.by}</span>
                        <span className="font-mono text-[11px]" title={c.at}>{relTime(c.at)}</span>
                      </li>
                    ))}
                  </ol>
                )}
                <Prose text={f.description} links={links} />
                {/* The reproduction chain: expected behaviour, the code steps
                    with one attachment each, impact with the finding's own
                    attachments. Attachment bytes come from the same origin as
                    the API (dev proxy included); owner-or-admin by audit id,
                    public by the published slug. */}
                {(f.snippet || (f.sites ?? []).length > 0 || f.normal_use || f.expected || f.impact || (f.visuals ?? []).length > 0) && (
                  <FindingCode finding={f} visualSrc={visualSrcFor(report)} pin={{ repo: report.document.report.pin.repo, commit: report.document.report.pin.commit }} links={links} />
                )}
                {f.exploit_path && (
                  <div className="flex flex-col gap-1.5">
                    <span className="label-caps text-bad">exploit path</span>
                    <pre className="overflow-x-auto rounded-md border border-bad/20 bg-bad-soft/40 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">{f.exploit_path}</pre>
                  </div>
                )}
                <div className="flex flex-col gap-1.5">
                  <span className="label-caps text-ok">fix</span>
                  <Prose
                    text={f.fix}
                    bullets
                    links={links}
                    patches={{
                      sites: [
                        ...(f.fix_sites ?? []),
                        { location: f.location, snippet: f.snippet },
                        ...(f.sites ?? []).map((st) => ({ location: st.location, snippet: st.snippet })),
                      ],
                      pin: { repo: report.document.report.pin.repo, commit: report.document.report.pin.commit },
                    }}
                    className="rounded-md border border-border bg-background p-3 pl-7 text-[13px]"
                  />
                </div>
                {f.references && f.references.length > 0 && (
                  <div className="flex flex-wrap gap-x-3 gap-y-2">
                    {f.references.map((u) => {
                      const code = f.reference_code?.find((c) => c.url === u)
                      return <Reference key={u} url={u} code={code} className={code ? 'basis-full' : undefined} />
                    })}
                  </div>
                )}
              </article>
            ))}
          </section>
        ))}
        {/* Archived: judged not relevant, with the reason, never counted and
            never gone. The status control brings one back. */}
        {archived.length > 0 && (
          <section className="flex flex-col gap-2">
            <span className="label-caps text-faint">archived</span>
            <ol className="divide-y divide-border rounded-md border border-dashed border-border">
              {archived.map((f) => {
                const last = [...(f.status_changes ?? [])].reverse().find((c) => c.to === 'archived')
                return (
                  <li key={f.id ?? f.ordinal} id={`f-${f.ordinal}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-[13px]">
                    <span className="num text-muted-foreground">{String(f.ordinal).padStart(2, '0')}</span>
                    <StateBadge state={severityState[f.severity]} className="h-5 px-1.5 text-[10.5px] opacity-70">{f.severity}</StateBadge>
                    <span className="text-foreground/80">{f.title}</span>
                    {last && (
                      <>
                        <span className="text-muted-foreground">{last.reason}</span>
                        <span className="font-mono text-[11px] text-faint">{last.by}</span>
                        <span className="font-mono text-[11px] text-faint" title={last.at}>{relTime(last.at)}</span>
                      </>
                    )}
                    {onStatus && f.id && <span className="ml-auto"><StatusPick finding={f} onStatus={onStatus} /></span>}
                  </li>
                )
              })}
            </ol>
          </section>
        )}
      </div>
      <aside className="hidden lg:block lg:sticky lg:top-8 lg:self-start">
        <nav aria-label="Findings">
          <ol className="flex flex-col gap-0.5 border-l border-border">
            {/* The index reads in the stream's order: severity groups, then
                ordinals within each, never bare ordinal order. */}
            {groups.flatMap((g) => g.items).map((f) => (
              <li key={f.id ?? f.ordinal}>
                <a
                  href={`#f-${f.ordinal}`}
                  aria-current={active === f.ordinal ? 'true' : undefined}
                  onFocus={() => setActive(f.ordinal)}
                  onClick={(e) => {
                    e.preventDefault()
                    setActive(f.ordinal)
                    const el = document.getElementById(`f-${f.ordinal}`)
                    el?.scrollIntoView({ block: 'start' })
                    el?.focus({ preventScroll: true })
                  }}
                  className={cn(
                    '-ml-px flex items-center gap-2 border-l py-1 pl-3 text-xs outline-none hover:text-foreground focus-visible:border-primary focus-visible:text-foreground',
                    active === f.ordinal ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground',
                  )}
                >
                  <span className={cn('size-1.5 shrink-0 rounded-full', severityDot[f.severity])} />
                  <span className="num">{String(f.ordinal).padStart(2, '0')}</span>
                  <span className="truncate">{f.title}</span>
                  {f.carried_from_id && <StateBadge state="neutral" className="ml-auto h-4 px-1 text-[9.5px]">carried</StateBadge>}
                </a>
              </li>
            ))}
          </ol>
        </nav>
      </aside>
    </div>
  )
}

// What earlier reports raised on this code, each with its state at the
// pinned commit. Not ours: not counted in the tiles, not in the findings,
// not in the query line. A reference that is a path opens the file at
// the commit; a URL opens as is.
const knownState = { fixed: 'ok', open: 'bad', acknowledged: 'neutral' } as const
function KnownStrip({ known, repo, commit }: { known: components['schemas']['Known'][]; repo: string; commit: string }) {
  const [open, setOpen] = useState(false)
  const href = (ref?: string) => (!ref ? undefined : /^https?:\/\//.test(ref) ? ref : `https://github.com/${repo}/blob/${commit}/${ref}`)
  return (
    <div className="flex flex-col gap-2">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-2 self-start rounded-sm outline-none hover:text-foreground focus-visible:text-foreground"
      >
        <span className="label-caps text-faint">known</span>
        <span className="num text-[11px] text-faint">{known.length}</span>
        {open ? <ChevronDown className="size-3.5 text-faint" /> : <ChevronRight className="size-3.5 text-faint" />}
      </button>
      {open && (
      <ul className="divide-y divide-border rounded-md border border-border bg-card">
        {known.map((k, i) => {
          const link = href(k.reference)
          return (
            <li key={i} className="grid grid-cols-[22px_10px_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-3.5 py-2 text-[12.5px] sm:grid-cols-[22px_10px_minmax(0,1fr)_auto_auto]">
              <span className="num text-faint">{String(i + 1).padStart(2, '0')}</span>
              <span className={cn('size-[7px] rounded-full', severityDot[k.severity])} title={k.severity} />
              <span className="min-w-0 truncate">{k.title}</span>
              {link ? (
                <a href={link} target="_blank" rel="noreferrer" className="rounded-sm border border-border px-1.5 font-mono text-[10.5px] text-muted-foreground hover:text-foreground">{k.source}</a>
              ) : (
                <span className="rounded-sm border border-border px-1.5 font-mono text-[10.5px] text-muted-foreground">{k.source}</span>
              )}
              <StateBadge state={knownState[k.status]} className="h-5 px-1.5 text-[10.5px]">{k.status === 'fixed' ? 'fixed at commit' : k.status}</StateBadge>
              {k.code && k.reference && <Reference url={k.reference} code={k.code} className="col-span-full pt-1" />}
            </li>
          )
        })}
      </ul>
      )}
    </div>
  )
}

export function SummaryStrip({ summary }: { summary: components['schemas']['Summary'] }) {
  return (
    <div className="grid grid-cols-3 gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-5">
      {order.map((s) => (
        <div key={s} className="flex flex-col gap-1 bg-card/50 px-4 py-3">
          <span className="label-caps">{s}</span>
          <span className={cn('num text-2xl leading-none', summary[s] === 0 ? 'text-faint' : severityInk[s])}>{summary[s]}</span>
        </div>
      ))}
    </div>
  )
}

async function downloadJSON(id: string, repo: string) {
  const res = await api.GET('/v1/audits/{id}/report.json', { params: { path: { id } } })
  if (!res.data) return toast.error('Export failed')
  const blob = new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${repo.replace('/', '-')}-findings.json`
  a.click()
  URL.revokeObjectURL(a.href)
}


// A facet is one enum of the findings schema, with the values these
// findings actually carry. Hiding a value removes its findings from the
// page; the set of hidden values lives in the URL so a view can be shared.
type Facet = FacetOf<Finding>

function facetsOf(findings: Finding[], words: ChainWords): Facet[] {
  const facet = (key: string, label: string, get: (f: Finding) => string, word: (v: string) => string, orderOf?: readonly string[], ranked = false): Facet => countFacet(findings, key, label, get, word, orderOf, ranked)
  const statuses = ['open', 'acknowledged', 'fixed', 'disputed'] as const
  // Least to most privileged, so the chips read as a ladder.
  const exploit = ['anyone', 'any_user', 'any_signer', 'account_owner', 'api_key', 'internal_network', 'internal', 'admin', 'upgrade_authority', 'other_program', 'none']
  const short = (p: string) => p.slice(p.lastIndexOf('/') + 1)
  // A file chip shows the base name. When base names collide across the
  // report, the nearest directory that differs is shown beside it, so five
  // lib.rs chips read as five packages rather than one word five times.
  const files = [...new Set(findings.map((f) => placeOf(f.location).file).filter(Boolean))]
  const fileWord = (p: string) => {
    const base = short(p)
    const twins = files.filter((o) => o !== p && short(o) === base)
    if (twins.length === 0) return base
    const parts = p.split('/')
    for (let i = parts.length - 2; i >= 0; i--) {
      const seg = parts[i] ?? ''
      if (twins.every((o) => o.split('/')[i] !== seg)) return `${seg} · ${base}`
    }
    return p
  }
  return [
    facet('severity', 'severity', (f) => f.severity, (v) => v, order, true),
    facet('status', 'status', (f) => f.status, (v) => v, statuses),
    facet('exploitable', 'exploitable by', (f) => f.exploitable_by ?? 'unset', (v) => words.exploitable[v] ?? v, [...exploit, 'unset']),
    facet('evidence', 'evidence', (f) => f.evidence?.kind ?? 'none', (v) => (v === 'onchain' ? 'on-chain' : v), ['static', 'fuzz', 'onchain', 'bytecode', 'live', 'none']),
    facet('tool', 'tool', (f) => f.evidence?.tool ?? 'none', (v) => v),
    // A web finding's place is its endpoint: the host and the route are
    // what the reader folds by, with ids folded out of the route.
    facet(words.instruction, words.instruction, (f) => (words.web ? routeOf(placeOf(f.location).url) : f.location.instruction ?? 'none'), (v) => (words.web ? shortRoute(v) : v)),
    ...(words.web ? [facet('method', 'method', (f) => methodOf(f), (v) => v)] : []),
    facet('file', 'file', (f) => placeOf(f.location).file || 'none', (v) => (v === 'none' ? 'no source' : fileWord(v))),
    facet(words.program, words.program, (f) => (words.web ? hostOf(placeOf(f.location).url) : f.location.program_id ?? 'none'), (v) => (words.web || v.length <= 12 ? v : `${v.slice(0, 6)}…${v.slice(-4)}`)),
    facet('sites', 'sites', (f) => ((f.sites ?? []).length > 0 ? 'several' : 'one'), (v) => v, ['one', 'several']),
    facet('exploit path', 'exploit path', (f) => (f.exploit_path || f.exploit_path_withheld ? 'yes' : 'no'), (v) => v, ['yes', 'no']),
  ].filter((g) => g.values.length > 1)
}

const findingText = (f: Finding) => `${f.title} ${f.description}`

function methodOf(f: Finding): string {
  const text = `${f.location.instruction ?? ''} ${f.sites?.[0]?.note ?? ''}`
  const m = /\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b/i.exec(text)
  return m ? (m[1] ?? '').toUpperCase() : 'none'
}

// A clause is one comparison against a facet, or a text match on the
// finding's words. Clauses are joined left to right by and / or, where
// and binds tighter: "a and b or c" reads as (a and b) or c.
