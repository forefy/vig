import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { ArrowUpRight } from 'lucide-react'
import { $api } from '@/api/hooks'
import { Page, PageHeader } from '@/app/Shell'
import { StateBadge, severityState, auditState } from '@/components/primitives'
import { QueryLine, matches, parseQuery, formatQuery } from '@/components/primitives/query'
import type { Query } from '@/components/primitives/query'
import { DepthMark } from '@/features/audits/surface/parts'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { SkeletonRows, ProblemBadge } from '@/components/ui/states'
import { relTime, cn } from '@/lib/utils'
import { PLACES, UNDER, key, seen, methodsOf, factValues, nodeText, nodeFacets, wordsFor } from './model'
import type { IndexNode, IndexAudit } from './model'

// Ledger archetype: the report's query line over every node of every
// map, rows folded by target and by the place they hang under, a rail
// with the rollups, and a sheet for one node with its evidence.
export function SurfaceIndex({ admin = false }: { admin?: boolean }) {
  const q = admin ? $api.useQuery('get', '/v1/admin/surface') : $api.useQuery('get', '/v1/surface')
  const [params, setParams] = useSearchParams()
  const query = parseQuery(params.get('q') ?? '')
  const setQuery = (next: Query) => {
    const p = new URLSearchParams(params)
    const s = next.items.length ? formatQuery(next) : ''
    if (s) p.set('q', s)
    else p.delete('q')
    setParams(p, { replace: true })
  }
  const [open, setOpen] = useState<IndexNode | null>(null)
  const audits = useMemo(() => q.data?.audits ?? [], [q.data])
  const all = useMemo(() => (q.data?.nodes ?? []).filter((n) => !n.external), [q.data])
  const facets = useMemo(() => nodeFacets(all, audits), [all, audits])
  const live = useMemo(() => all.filter((n) => matches(n, facets, query, nodeText)), [all, facets, query])
  const words = useMemo(() => wordsFor(audits), [audits])

  if (q.isLoading) return <Page wide><SkeletonRows rows={8} /></Page>
  if (q.isError) return <Page wide><ProblemBadge error={q.error} onRetry={() => void q.refetch()} /></Page>

  const byKey = new Map(all.map((n) => [key(n), n]))
  const under = new Map<string, IndexNode[]>()
  for (const n of all) if (n.parent) under.set(`${n.audit_id}|${n.parent}`, [...(under.get(`${n.audit_id}|${n.parent}`) ?? []), n])
  const liveKeys = new Set(live.map(key))
  // A matching account or parameter lifts its place into view.
  const placeKeys = new Set<string>()
  for (const n of live) {
    if (PLACES.has(n.kind)) placeKeys.add(key(n))
    else if (UNDER.has(n.kind) && n.parent) placeKeys.add(`${n.audit_id}|${n.parent}`)
  }
  const isLeafPlace = (n: IndexNode) => n.kind === 'instruction' || n.kind === 'route' || !(under.get(key(n)) ?? []).some((k) => PLACES.has(k.kind))
  const places = all.filter((n) => PLACES.has(n.kind) && placeKeys.has(key(n)) && isLeafPlace(n))
  const byAudit = new Map<string, IndexNode[]>()
  for (const p of places) byAudit.set(p.audit_id, [...(byAudit.get(p.audit_id) ?? []), p])
  // An audit whose map holds only its root has nothing to browse yet.
  const order = audits.filter((a) => byAudit.has(a.id) && (a.map?.instructions ?? 0) > 0).sort((a, b) => (byAudit.get(b.id)?.length ?? 0) - (byAudit.get(a.id)?.length ?? 0))
  const withFindings = all.filter((n) => (n.findings ?? []).length > 0).length
  const reached = all.filter(seen).length
  const reviewed = all.filter((n) => n.attention === 'reviewed').length
  const targets = new Set(audits.map((a) => a.repo)).size
  const base = admin ? '/admin/audits' : '/app/audits'

  return (
    <Page wide>
      <Helmet><title>Surface</title></Helmet>
      <PageHeader
        eyebrow="surface"
        title="Across audits"
        aside={
          <div className="flex flex-wrap gap-2">
            <StateBadge state="neutral"><span className="num">{targets}</span> targets</StateBadge>
            <StateBadge state="neutral"><span className="num">{audits.length}</span> audits</StateBadge>
            <StateBadge state="neutral"><span className="num">{all.length}</span> nodes</StateBadge>
            <StateBadge state="ok"><span className="num">{reached}</span> reached</StateBadge>
            <StateBadge state="neutral"><span className="num">{reviewed}</span> reviewed</StateBadge>
            <StateBadge state="bad"><span className="num">{withFindings}</span> with findings</StateBadge>
          </div>
        }
      />
      <div className="grid items-start gap-7 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0">
          {all.length > 0 && <QueryLine facets={facets} all={all} query={query} onChange={setQuery} shown={live.length} total={all.length} textOf={nodeText} textLabel="name" placeholder="kind = route" ariaLabel="Filter the surface" />}
          {order.length === 0 && <StateBadge state="pending" className="mt-6 h-6 w-fit px-2 text-[11px]">{all.length === 0 ? 'no map yet' : 'nothing on the surface matches'}</StateBadge>}
          {order.map((a) => {
            const w = words(a.id)
            const rows = byAudit.get(a.id) ?? []
            const byParent = new Map<string, IndexNode[]>()
            for (const p of rows) byParent.set(p.parent || p.id, [...(byParent.get(p.parent || p.id) ?? []), p])
            const reachedHere = a.map?.instructions_observed ?? 0
            const totalHere = a.map?.instructions ?? 0
            return (
              <section key={a.id} className="mt-6">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-0.5 pb-2">
                  <StateBadge state="neutral">{a.adapter === 'webapp' ? 'web app' : a.adapter}</StateBadge>
                  <Link to={`${base}/${a.id}/surface`} className="font-mono text-[14px] font-medium hover:underline">{a.repo}</Link>
                  <span className="font-mono text-[11.5px] text-muted-foreground">{a.head_sha.slice(0, 7)} · {a.coverage}</span>
                  <StateBadge state={auditState[a.state] ?? 'neutral'}>{a.state.replace('_', ' ')}</StateBadge>
                  <span className="inline-flex items-center gap-2 font-mono text-[11px] text-muted-foreground">
                    <span className="h-[5px] w-[90px] overflow-hidden rounded-sm bg-border"><span className="block h-full bg-ok" style={{ width: `${totalHere ? Math.round((100 * reachedHere) / totalHere) : 0}%` }} /></span>
                    {reachedHere} of {totalHere} {w.instructions} reached
                  </span>
                </div>
                {[...byParent.entries()].map(([pk, list]) => {
                  const parent = byKey.get(`${a.id}|${pk}`)
                  return (
                    <div key={pk} className="mb-3 overflow-hidden rounded-md border border-border bg-card/50">
                      <div className="flex items-center gap-2.5 border-b border-border bg-muted px-3 py-1.5 font-mono text-[12px]">
                        <span className="label-caps">{parent ? parent.kind : w.program}</span>
                        <span>{parent ? parent.name : pk}</span>
                        {parent?.facts?.loader && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">{parent.facts.loader}</StateBadge>}
                        <span className="ml-auto text-muted-foreground">{list.length}</span>
                      </div>
                      {list.map((n) => (n.id === pk && list.length > 1 ? null : <Row key={n.id} n={n} under={under.get(key(n)) ?? []} liveKeys={liveKeys} narrowing={query.items.length > 0} words={w} max={maxOf(all, n)} selected={open ? key(open) === key(n) : false} onOpen={() => setOpen(n)} />))}
                    </div>
                  )
                })}
              </section>
            )
          })}
        </div>
        <Rail live={live} all={all} audits={audits} onFacet={(facet, value) => setQuery({ items: [...query.items, { facet, op: '=', value }], conns: query.items.length ? [...query.conns, 'and'] : [] })} />
      </div>
      <NodeSheet node={open} audit={open ? audits.find((a) => a.id === open.audit_id) : undefined} under={open ? (under.get(key(open)) ?? []) : []} admin={admin} onClose={() => setOpen(null)} />
    </Page>
  )
}

export function AdminSurfaceIndex() {
  return <SurfaceIndex admin />
}

const maxOf = (all: IndexNode[], n: IndexNode) => Math.max(1, ...all.filter((x) => x.audit_id === n.audit_id && x.kind === n.kind).map((x) => (x.ok ?? 0) + (x.failed ?? 0)))

function Row({ n, under, liveKeys, narrowing, words, max, selected, onOpen }: { n: IndexNode; under: IndexNode[]; liveKeys: Set<string>; narrowing: boolean; words: ReturnType<ReturnType<typeof wordsFor>>; max: number; selected: boolean; onOpen: () => void }) {
  const subs = under.filter((k) => UNDER.has(k.kind))
  const hits = narrowing ? subs.filter((k) => liveKeys.has(key(k))) : []
  const total = (n.ok ?? 0) + (n.failed ?? 0)
  const isSeen = seen(n)
  const accWord = subs.length === 1 ? (n.kind === 'route' ? 'parameter' : 'account') : n.kind === 'route' ? 'parameters' : 'accounts'
  return (
    <div
      role="button"
      tabIndex={0}
      aria-selected={selected}
      onClick={onOpen}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen() } }}
      className={cn('grid cursor-pointer items-center gap-2.5 border-b border-border px-3 py-1.5 outline-none last:border-b-0 hover:bg-accent/40 focus-visible:bg-accent/40 md:grid-cols-[18px_minmax(160px,1.4fr)_84px_minmax(0,1.2fr)_110px_70px_minmax(0,1fr)_96px]', selected && 'bg-accent/60')}
    >
      <DepthMark depth={[true, isSeen, (n.replays ?? 0) > 0, n.attention === 'reviewed']} />
      <span className="truncate font-mono text-[12.5px]" title={n.id}>{n.name}</span>
      <span className="font-mono text-[10.5px] text-muted-foreground">{n.kind}</span>
      <span className="flex min-w-0 flex-wrap gap-1">
        {methodsOf(n).map((m) => <StateBadge key={m} state="neutral" className="h-5 px-1.5 text-[10.5px]">{m}</StateBadge>)}
        {n.facts?.accounts && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px] text-muted-foreground">{n.facts.accounts} {words.web ? 'parameters' : 'accounts'}</StateBadge>}
        {n.kind === 'route' && subs.length > 0 && <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px] text-muted-foreground">{subs.length} {accWord}</StateBadge>}
        {n.facts?.auth && <StateBadge state={n.facts.auth === 'none' ? 'warn' : 'neutral'} className="h-5 px-1.5 text-[10.5px]">{n.facts.auth}</StateBadge>}
        {factValues(n.facts?.status).filter((c) => c >= '400').map((c) => <StateBadge key={c} state={c >= '500' ? 'bad' : 'neutral'} className="h-5 px-1.5 text-[10.5px]">{c}</StateBadge>)}
        {hits.slice(0, 3).map((k) => <StateBadge key={k.id} state="ok" className="h-5 px-1.5 text-[10.5px]">{k.name}{k.facts?.in ? ` · ${k.facts.in}` : ''}{k.facts?.type ? ` · ${k.facts.type}` : ''}{k.facts?.reflected === 'true' ? ' · reflected' : ''}{k.facts?.signer === 'true' ? ' · signer' : ''}{k.facts?.writable === 'true' ? ' · writable' : ''}</StateBadge>)}
      </span>
      <span className="inline-flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
        <span className="h-1 w-12 overflow-hidden rounded-sm bg-border"><span className="block h-full bg-ok" style={{ width: total ? `${Math.max(6, Math.round((100 * total) / max))}%` : '0%' }} /></span>
        <span className={cn('num', !total && 'text-faint')}>{total}</span>
      </span>
      <span className="font-mono text-[11px] text-muted-foreground">{total > 0 && <><span className="text-ok">{n.ok ?? 0}</span> ok{(n.failed ?? 0) > 0 && <> <span className="text-bad">{n.failed}</span></>}</>}</span>
      <span className="flex flex-wrap gap-1">{(n.findings ?? []).map((f) => <StateBadge key={f.ordinal} state={severityState[f.severity]} className="h-5 px-1.5 text-[10.5px]">#{String(f.ordinal).padStart(2, '0')} {f.severity}</StateBadge>)}</span>
      <span className="font-mono text-[10.5px] text-faint md:text-right">{n.last_seen ? relTime(n.last_seen) : ''}</span>
    </div>
  )
}

function Rail({ live, all, audits, onFacet }: { live: IndexNode[]; all: IndexNode[]; audits: IndexAudit[]; onFacet: (facet: string, value: string) => void }) {
  const att: Record<string, number> = {}
  for (const n of live) att[n.attention] = (att[n.attention] ?? 0) + 1
  const kinds: Record<string, number> = {}
  for (const n of live) kinds[n.kind] = (kinds[n.kind] ?? 0) + 1
  const total = Math.max(1, live.length)
  const tone: Record<string, string> = { finding: 'bg-bad', reviewed: 'bg-primary', observed: 'bg-ok', unseen: 'bg-border' }
  const mapped = audits.filter((a) => (a.map?.instructions ?? 0) > 0)
  return (
    <aside className="flex flex-col gap-3.5 lg:sticky lg:top-4">
      <div className="rounded-md border border-border bg-card/50 px-3.5 py-3">
        <span className="label-caps">in view</span>
        <div className="mt-1 font-mono text-[24px]">{live.length}<span className="text-[14px] text-faint"> / {all.length} nodes</span></div>
        <div className="mt-2.5 flex h-2 overflow-hidden rounded-sm bg-border">{['finding', 'reviewed', 'observed', 'unseen'].map((k) => <span key={k} className={cn('block h-full', tone[k])} style={{ width: `${(100 * (att[k] ?? 0)) / total}%` }} />)}</div>
        <div className="mt-2.5 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 font-mono text-[11px] text-muted-foreground">
          {['unseen', 'observed', 'reviewed', 'finding'].map((k) => <button key={k} type="button" onClick={() => onFacet('attention', k)} className="flex items-center gap-1.5 text-left hover:text-foreground"><span className={cn('inline-block size-[7px] rounded-full', tone[k])} />{k}</button>).flatMap((b, i) => [b, <span key={`n${i}`} className="num text-foreground">{att[['unseen', 'observed', 'reviewed', 'finding'][i] ?? ''] ?? 0}</span>])}
        </div>
      </div>
      <div className="rounded-md border border-border bg-card/50 px-3.5 py-3">
        <span className="label-caps">by kind</span>
        <div className="mt-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1.5 font-mono text-[11.5px]">
          {Object.entries(kinds).sort((x, y) => y[1] - x[1]).flatMap(([k, v]) => [<button key={k} type="button" onClick={() => onFacet('kind', k)} className="text-left hover:text-primary">{k}</button>, <span key={`${k}n`} className="num">{v}</span>])}
        </div>
      </div>
      {mapped.length > 0 && (
        <div className="rounded-md border border-border bg-card/50 px-3.5 py-3">
          <span className="label-caps">targets · {new Set(mapped.map((a) => a.repo)).size}</span>
          <div className="mt-2 grid grid-cols-[auto_1fr_auto] items-center gap-x-2.5 gap-y-1.5 font-mono text-[11px] text-muted-foreground">
            {mapped.map((a) => {
              const pct = Math.round((100 * (a.map?.instructions_observed ?? 0)) / (a.map?.instructions ?? 1))
              return [<span key={`${a.id}r`} className="truncate">{a.repo.slice(a.repo.indexOf('/') + 1)}</span>, <span key={`${a.id}b`} className="h-[5px] overflow-hidden rounded-sm bg-border"><span className="block h-full bg-ok" style={{ width: `${pct}%` }} /></span>, <span key={`${a.id}p`} className="num">{pct}%</span>]
            })}
          </div>
        </div>
      )}
    </aside>
  )
}

function NodeSheet({ node, audit, under, admin, onClose }: { node: IndexNode | null; audit?: IndexAudit; under: IndexNode[]; admin: boolean; onClose: () => void }) {
  const id = node?.audit_id ?? ''
  const view = admin
    ? $api.useQuery('get', '/v1/admin/audits/{id}/surface', { params: { path: { id } } }, { enabled: !!node })
    : $api.useQuery('get', '/v1/audits/{id}/surface', { params: { path: { id } } }, { enabled: !!node })
  const evidence = node ? (view.data?.evidence ?? []).filter((e) => e.node_id === node.id).slice(0, 8) : []
  const w = node && audit ? wordsFor([audit])(audit.id) : undefined
  const subs = under.filter((k) => UNDER.has(k.kind))
  return (
    <Sheet open={!!node} onOpenChange={(o) => { if (!o) onClose() }}>
      <SheetContent side="right" className="w-full overflow-y-auto sm:max-w-[520px]">
        {node && audit && (
          <div className="flex flex-col gap-4 p-5">
            <div>
              <span className="label-caps">{node.kind} · {audit.repo}</span>
              <SheetTitle className="mt-1 break-all font-mono text-[16px] font-medium">{node.name}</SheetTitle>
              <div className="mt-2.5 flex flex-wrap gap-2">
                <StateBadge state={node.attention === 'finding' ? 'bad' : node.attention === 'unseen' ? 'neutral' : 'ok'}>{node.attention}</StateBadge>
                {(node.replays ?? 0) > 0 && <StateBadge state="neutral">{node.replays} replays</StateBadge>}
                {(node.ok ?? 0) + (node.failed ?? 0) > 0 && <StateBadge state="neutral"><span className="text-ok">{node.ok}</span> ok{(node.failed ?? 0) > 0 && <> · <span className="text-bad">{node.failed} failed</span></>}</StateBadge>}
                {(node.signers ?? 0) > 0 && <StateBadge state="neutral">{node.signers} {w?.signers}</StateBadge>}
              </div>
            </div>
            <dl className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1.5 font-mono text-[12px]">
              <dt className="label-caps">audit</dt><dd>{audit.head_sha.slice(0, 7)} · {audit.coverage} · {audit.state.replace('_', ' ')}</dd>
              <dt className="label-caps">id</dt><dd className="break-all">{node.id}</dd>
              <dt className="label-caps">source</dt><dd>{node.source}</dd>
              {Object.entries(node.facts ?? {}).filter(([k]) => k !== 'program').map(([k, v]) => <Fragment2 key={k} k={k} v={v} />)}
              {node.last_seen && <><dt className="label-caps">last seen</dt><dd>{relTime(node.last_seen)}</dd></>}
            </dl>
            {subs.length > 0 && (
              <div>
                <span className="label-caps">{subs.length === 1 ? (node.kind === 'route' ? 'parameter' : 'account') : node.kind === 'route' ? 'parameters' : 'accounts'}</span>
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {subs.map((k) => <StateBadge key={k.id} state={k.attention === 'finding' ? 'bad' : 'neutral'} className="h-[22px]">{k.name}{k.facts?.in ? <span className="ml-1 text-faint">{k.facts.in}</span> : null}{k.facts?.type ? <span className="ml-1 text-faint">{k.facts.type}</span> : null}{k.facts?.reflected === 'true' ? <span className="ml-1 text-warn">reflected</span> : null}{k.facts?.signer === 'true' ? <span className="ml-1 text-warn">signer</span> : null}{k.facts?.writable === 'true' ? <span className="ml-1 text-faint">writable</span> : null}</StateBadge>)}
                </div>
              </div>
            )}
            {(node.findings ?? []).length > 0 && (
              <div>
                <span className="label-caps">findings here</span>
                {(node.findings ?? []).map((f) => (
                  <Link key={f.ordinal} to={`${base(admin)}/${audit.id}/report#f-${f.ordinal}`} className="flex items-center gap-2 border-t border-border py-1.5 text-[13px] hover:underline">
                    <StateBadge state={severityState[f.severity]} className="h-5 px-1.5 text-[10.5px]">#{String(f.ordinal).padStart(2, '0')} {f.severity}</StateBadge>
                    <span className="min-w-0 truncate">{f.title}</span>
                    <span className="ml-auto font-mono text-[11px] text-muted-foreground">{f.status}</span>
                  </Link>
                ))}
              </div>
            )}
            <div>
              <span className="label-caps">evidence · {node.observed}{node.observed > evidence.length && evidence.length > 0 ? `, last ${evidence.length}` : ''}</span>
              {view.isLoading && <SkeletonRows rows={3} />}
              {!view.isLoading && evidence.length === 0 && <StateBadge state="pending" className="mt-1.5 h-5 w-fit px-1.5 text-[10.5px]">nothing reached this node</StateBadge>}
              {evidence.map((e) => {
                const s = (e.summary ?? {}) as Record<string, unknown>
                const head = e.kind === 'request' ? `${String(s.method ?? '')} ${String(s.status ?? '')}` : e.kind
                const body = e.kind === 'request' ? `${String(s.path ?? '')}${s.ms ? ` · ${String(s.ms)} ms` : ''}` : e.kind === 'review' ? String(s.note ?? 'looked at') : `${(e.ref ?? '').slice(0, 14)}…${s.units ? ` · ${String(s.units)} cu` : ''}`
                return (
                  <div key={e.id ?? `${e.kind}${e.at}`} className="grid grid-cols-[80px_minmax(0,1fr)_auto] gap-x-2.5 border-t border-border py-1.5 font-mono text-[11.5px]">
                    <span className="text-muted-foreground">{head}</span><span className="truncate">{body}</span><span className="text-faint">{e.at ? relTime(e.at) : ''}</span>
                  </div>
                )
              })}
            </div>
            <Link to={`${base(admin)}/${audit.id}/surface#n-${encodeURIComponent(node.id)}`} className="inline-flex h-7 w-fit items-center gap-1 rounded-md border border-border px-2.5 font-mono text-[11.5px] hover:border-primary">open in the audit's map <ArrowUpRight className="size-3.5" /></Link>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

const base = (admin: boolean) => (admin ? '/admin/audits' : '/app/audits')

function Fragment2({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt className="label-caps">{k}</dt>
      <dd className="break-all">{v}</dd>
    </>
  )
}
