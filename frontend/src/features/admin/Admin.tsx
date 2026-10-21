import { useState } from 'react'
import { Link, NavLink, Outlet, useParams, useSearchParams } from 'react-router'
import { Helmet } from 'react-helmet-async'
import { $api } from '@/api/hooks'
import { api } from '@/api/client'
import { Page, PageHeader } from '@/app/Shell'
import { StateBadge, auditState, stageWord, Empty, Copy, Stat, severityState } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { countdown, relTime, short, fmtDate, cn } from '@/lib/utils'
import { useNow } from '@/lib/useNow'
import { useBusy } from '@/lib/useBusy'
import { problemDetail } from '@/lib/problem'
import { toast } from 'sonner'
import { ViewAs, MintToken, StateAction, DeleteAction } from './AdminAudit'
import { chainWords } from '@/lib/chain'

export { AdminAuditDetail, AdminAuditSurfaceView } from './AdminAudit'

const tabs = [
  { to: '/admin', label: 'Queue', end: true },
  { to: '/admin/audits', label: 'Audits' },
  { to: '/admin/claims', label: 'Claims' },
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/actions', label: 'Actions' },
  { to: '/admin/lessons', label: 'Lessons' },
]

// Board archetype for operators. Every action here becomes a visible event.
export function Admin() {
  return (
    <Page wide>
      <Helmet>
        <title>Admin</title>
      </Helmet>
      <PageHeader eyebrow="admin" title="Operations" />
      <div className="grid grid-cols-1 gap-8 md:grid-cols-[180px_minmax(0,1fr)]">
        <nav className="flex flex-row gap-1 overflow-x-auto md:flex-col">
          {tabs.map((t) => (
            <NavLink key={t.to} to={t.to} end={t.end} className={({ isActive }) => cn('rounded-md px-3 py-1.5 text-sm whitespace-nowrap text-muted-foreground hover:text-foreground', isActive && 'bg-accent text-foreground')}>
              {t.label}
            </NavLink>
          ))}
        </nav>
        <div className="min-w-0">
          <Outlet />
        </div>
      </div>
    </Page>
  )
}

// Loading, failure and empty are three different facts. A 403 is never an
// empty list.
type Fetch = { isLoading: boolean; isError: boolean; error: unknown; refetch: () => Promise<unknown> }

function Fetched({ q, items, empty, rows = 4, children }: { q: Fetch; items: unknown[]; empty: string; rows?: number; children: React.ReactNode }) {
  if (q.isLoading) return <SkeletonRows rows={rows} />
  if (q.isError) return <ProblemBadge error={q.error} onRetry={() => void q.refetch()} className="w-fit" />
  if (items.length === 0) return <Empty title={empty} />
  return <>{children}</>
}

export function AdminQueue() {
  const q = $api.useQuery('get', '/v1/admin/queue', {}, { refetchInterval: 15_000 })
  const now = useNow(1000)
  const items = q.data?.items ?? []
  // The repo opens the admin's view of the audit; the token for a job is
  // minted or renewed right here.
  return (
    <Fetched q={q} items={items} empty="Queue is empty">
      <div className="divide-y divide-border rounded-md border border-border bg-card">
        {items.map((a) => (
          <div key={a.id} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-4 gap-y-1 px-4 py-3 text-sm sm:grid-cols-[minmax(0,1fr)_auto_auto_auto_auto]">
            <Link to={`/admin/audits/${a.id}`} className="flex min-w-0 flex-col gap-0.5 hover:underline">
              <span className="truncate font-mono">{a.repo} <span className="text-xs text-muted-foreground">{short(a.head_sha, 7, 0)}</span></span>
              <span className="truncate font-mono text-xs text-muted-foreground">{(a.program_ids ?? []).map((p) => short(p, 4, 4)).join(' ')}</span>
            </Link>
            <StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge>
            <span className={cn('num hidden w-16 text-right text-xs sm:block', a.eta_at && new Date(a.eta_at).getTime() < now ? 'text-bad' : 'text-muted-foreground')}>{a.eta_at ? countdown(a.eta_at, now) : '-'}</span>
            <span className="num hidden w-14 text-right text-xs text-faint sm:block">{relTime(a.created_at, now)}</span>
            <span className="flex w-40 items-center justify-end gap-0.5">
              <MintToken auditId={a.id} state={a.state} />
              <StateAction audit={a} onDone={() => void q.refetch()} icon />
            </span>
          </div>
        ))}
      </div>
    </Fetched>
  )
}

const states = ['queued', 'claimed', 'cloning', 'programs_verified', 'analysis', 'human_review', 'delivered', 'delayed', 'failed', 'cancelled'] as const
type AuditState = (typeof states)[number]
const isState = (s: string | null): s is AuditState => (states as readonly string[]).includes(s ?? '')

// Every audit across users. The filters live in the URL, so a view can be
// linked; the chip strip is the whole control surface.
export function AdminAudits() {
  const [sp, setSp] = useSearchParams()
  const state = sp.get('state')
  const adapter = sp.get('adapter')
  const hidden = sp.get('hidden')
  const userId = sp.get('user_id') ?? ''
  const qText = sp.get('q') ?? ''
  const set = (k: string, v: string | null) => {
    const next = new URLSearchParams(sp)
    if (v === null || v === '') next.delete(k)
    else next.set(k, v)
    setSp(next, { replace: true })
  }
  const q = $api.useQuery(
    'get',
    '/v1/admin/audits',
    { params: { query: { state: isState(state) ? state : undefined, adapter: adapter === 'solana' || adapter === 'ethereum' ? adapter : undefined, hidden: hidden === 'true' || hidden === 'false' ? hidden : undefined, user_id: userId || undefined, q: qText || undefined } } },
    { refetchInterval: 30_000 },
  )
  const now = useNow(60_000)
  const items = q.data?.items ?? []
  // Rows ticked for a bulk delete. One click deletes them all; the toast
  // counts what went and names what did not.
  const [picked, setPicked] = useState<Set<string>>(() => new Set())
  const { busy, run } = useBusy()
  const visible = items.filter((a) => picked.has(a.id)).length
  const toggle = (id: string) => setPicked((p) => { const n = new Set(p); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const deletePicked = () =>
    run(async () => {
      const ids = items.filter((a) => picked.has(a.id)).map((a) => a.id)
      const r = await api.POST('/v1/admin/audits/delete', { body: { ids } })
      if (r.error || !r.data) toast.error(problemDetail(r.error, 'Failed'))
      else {
        const n = (r.data.deleted ?? []).length
        const failed = r.data.failed ?? []
        if (failed.length) toast.error(`${n} deleted · ${failed.length} not: ${failed.map((f) => short(f.id, 4, 4)).join(' ')}`)
        else toast.success(`${n} deleted`)
        setPicked(new Set())
        void q.refetch()
      }
    })
  const chip = (on: boolean, tone?: 'bad' | 'warn') => cn('rounded-sm border px-2 py-0.5 font-mono text-[11px]', on ? (tone === 'bad' ? 'border-bad text-bad' : tone === 'warn' ? 'border-warn text-warn' : 'border-primary text-foreground') : 'border-border text-muted-foreground hover:text-foreground')
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {states.map((s) => (
          <button key={s} type="button" onClick={() => set('state', state === s ? null : s)} className={chip(state === s, s === 'failed' || s === 'cancelled' ? 'bad' : s === 'delayed' ? 'warn' : undefined)}>{stageWord[s] ?? s}</button>
        ))}
        <span className="mx-1 h-4 w-px bg-border" />
        {(['solana', 'ethereum'] as const).map((a) => (
          <button key={a} type="button" onClick={() => set('adapter', adapter === a ? null : a)} className={chip(adapter === a)}>{a}</button>
        ))}
        <span className="mx-1 h-4 w-px bg-border" />
        <button type="button" onClick={() => set('hidden', hidden === 'true' ? null : 'true')} className={chip(hidden === 'true', 'bad')}>hidden</button>
        <button type="button" onClick={() => set('hidden', hidden === 'false' ? null : 'false')} className={chip(hidden === 'false')}>visible</button>
        <Input value={qText} onChange={(e) => set('q', e.target.value)} placeholder="repo" aria-label="Repository" className="ml-auto h-7 w-40 font-mono text-xs" />
        {userId && <button type="button" onClick={() => set('user_id', null)} className={chip(true)}>user {short(userId, 4, 4)} ×</button>}
      </div>
      {visible > 0 && (
        <div className="flex items-center gap-3 rounded-md border border-bad/30 bg-bad-soft/30 px-3 py-1.5 text-sm">
          <span className="num">{visible}</span>
          <button type="button" onClick={() => setPicked(new Set())} className="font-mono text-xs text-muted-foreground hover:text-foreground">clear</button>
          <Button size="sm" variant="destructive" className="ml-auto" disabled={busy} onClick={deletePicked}>Delete {visible}</Button>
        </div>
      )}
      <Fetched q={q} items={items} empty="No audits">
        <div className="divide-y divide-border rounded-md border border-border bg-card">
          {items.map((a) => (
            <div key={a.id} className={cn('grid grid-cols-[auto_minmax(0,1fr)_auto_auto] items-center gap-x-4 gap-y-1 px-4 py-3 text-sm sm:grid-cols-[auto_minmax(0,1fr)_140px_auto_auto_auto_auto]', a.hidden_at && 'opacity-60', picked.has(a.id) && 'bg-bad-soft/20')}>
              <Checkbox checked={picked.has(a.id)} onCheckedChange={() => toggle(a.id)} aria-label={`Select ${a.repo}`} />
              <Link to={`/admin/audits/${a.id}`} className="flex min-w-0 flex-col gap-0.5 hover:underline">
                <span className="truncate font-mono">{a.repo} <span className="text-xs text-muted-foreground">{short(a.head_sha, 7, 0)}</span></span>
                <span className="truncate font-mono text-xs text-muted-foreground">{chainWords(a.adapter).programs} {(a.program_ids ?? []).map((p) => short(p, 4, 4)).join(' ') || '·'}</span>
              </Link>
              <button type="button" onClick={() => set('user_id', a.user_id)} className="hidden truncate text-left text-xs text-muted-foreground hover:text-foreground sm:block">{a.user_name || short(a.user_id, 4, 4)}</button>
              <span className="flex items-center gap-1.5">
                <StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge>
                {a.hidden_at && <StateBadge state="bad">hidden</StateBadge>}
              </span>
              <span className="num hidden w-14 text-right text-xs text-faint sm:block">{relTime(a.updated_at, now)}</span>
              <span className="num hidden w-10 text-right text-xs text-faint sm:block">{a.adapter === 'ethereum' ? 'evm' : 'sol'}</span>
              <span className="flex items-center gap-0.5">
                <StateAction audit={a} onDone={() => void q.refetch()} icon />
                <DeleteAction audit={a} onDone={() => void q.refetch()} icon />
              </span>
            </div>
          ))}
        </div>
      </Fetched>
    </div>
  )
}

export function AdminClaims() {
  const q = $api.useQuery('get', '/v1/admin/tweet-claims')
  const now = useNow(60_000)
  const items = q.data?.items ?? []
  async function act(id: string, action: 'approve' | 'reject' | 'revoke') {
    const r = await api.POST(`/v1/admin/tweet-claims/{id}/${action}`, { params: { path: { id } }, body: {} })
    if (r.error) toast.error(problemDetail(r.error, 'Failed'))
    else q.refetch()
  }
  return (
    <Fetched q={q} items={items} empty="No claims">
      <div className="divide-y divide-border rounded-md border border-border bg-card">
        {items.map((c) => (
          <div key={c.id} className="grid grid-cols-[auto_1fr_auto_auto] items-center gap-4 px-4 py-3 text-sm">
            <StateBadge state={c.state === 'approved' ? 'ok' : c.state === 'submitted' ? 'pending' : 'bad'}>{c.state}</StateBadge>
            <div className="flex min-w-0 flex-col">
              <a href={c.url} target="_blank" rel="noreferrer" className="truncate font-mono text-xs hover:underline">{c.url}</a>
              <span className="font-mono text-[11px] text-muted-foreground">{c.nonce}</span>
            </div>
            <span className="num text-xs text-faint">{relTime(c.created_at, now)}</span>
            <div className="flex gap-1">
              {c.state === 'submitted' && (
                <>
                  <Button size="xs" onClick={() => act(c.id, 'approve')}>Approve</Button>
                  <Button size="xs" variant="outline" onClick={() => act(c.id, 'reject')}>Reject</Button>
                </>
              )}
              {c.state === 'approved' && <Button size="xs" variant="ghost" onClick={() => act(c.id, 'revoke')}>Revoke</Button>}
            </div>
          </div>
        ))}
      </div>
    </Fetched>
  )
}

// Grant or revoke credits for one user, with the reason the ledger keeps.
function CreditsDialog({ userId, credits, onDone }: { userId: string; credits: number; onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [delta, setDelta] = useState('1')
  const [reason, setReason] = useState('')
  const { busy, run } = useBusy()
  const n = Number(delta)
  const valid = Number.isInteger(n) && n > 0 && reason.trim().length > 0
  function move(kind: 'grant' | 'revoke') {
    void run(async () => {
      const r = await api.POST(`/v1/admin/credits/${kind}`, { body: { user_id: userId, delta: n, reason: reason.trim() } })
      if (r.error) toast.error(problemDetail(r.error, 'Failed'))
      else {
        toast.success(kind === 'grant' ? `+${n}` : `-${n}`)
        setReason('')
        setOpen(false)
        onDone()
      }
    })
  }
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>Credits</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">credits</DialogTitle>
          <Stat label="balance" value={credits} />
          <div className="flex gap-2">
            <Input value={delta} onChange={(e) => setDelta(e.target.value)} type="number" min={1} step={1} aria-label="Credits" className="h-9 w-24 font-mono" />
            <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="reason" aria-label="Reason" className="h-9" />
          </div>
          <div className="flex gap-2">
            <Button size="sm" disabled={!valid || busy} onClick={() => move('grant')}>Grant</Button>
            <Button size="sm" variant="outline" disabled={!valid || busy} onClick={() => move('revoke')}>Revoke</Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function AdminUsers() {
  const q = $api.useQuery('get', '/v1/admin/users')
  const items = q.data?.items ?? []
  return (
    <Fetched q={q} items={items} empty="No users">
      <div className="overflow-x-auto rounded-md border border-border bg-card">
        <table className="w-full min-w-[640px] text-sm">
          <tbody className="divide-y divide-border">
            {items.map((u) => (
              <tr key={u.user.id}>
                <td className="px-4 py-2.5">
                  <Link to={`/admin/users/${u.user.id}`} className="hover:underline">{u.user.display_name}</Link>
                  {u.user.role === 'admin' && <StateBadge state="ok" className="ml-2">admin</StateBadge>}
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{(u.identities ?? []).map((i) => i.provider).join(' ')}</td>
                <td className="num px-4 py-2.5 text-right">{u.credits} cr</td>
                <td className="px-4 py-2.5 text-right text-xs text-muted-foreground">{u.plan?.kind ?? '-'}</td>
                <td className="num px-4 py-2.5 text-right text-xs">{u.audits} audits</td>
                <td className="px-4 py-2.5 text-right"><Copy value={u.user.id} className="text-[11px]">{short(u.user.id, 8, 4)}</Copy></td>
                <td className="px-4 py-2.5 text-right"><CreditsDialog userId={u.user.id} credits={u.credits} onDone={() => void q.refetch()} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Fetched>
  )
}

const providerWord: Record<string, string> = { google: 'Google', github: 'GitHub', solana: 'Wallet', discord: 'Discord' }

export function AdminUserDetail() {
  const { id = '' } = useParams()
  const q = $api.useQuery('get', '/v1/admin/users/{id}', { params: { path: { id } } })
  const now = useNow(60_000)
  if (q.isLoading) return <SkeletonRows rows={4} />
  if (q.isError || !q.data) return <ProblemBadge error={q.error ?? { status: 404, title: 'Not found' }} onRetry={() => void q.refetch()} className="w-fit" />
  const u = q.data
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/admin/users" className="font-mono text-xs text-muted-foreground hover:text-foreground">users /</Link>
        <span className="text-lg font-medium">{u.user.display_name}</span>
        <StateBadge state={u.user.role === 'admin' ? 'ok' : 'neutral'}>{u.user.role}</StateBadge>
        <Copy value={u.user.id} className="text-[11px]">{short(u.user.id, 8, 4)}</Copy>
        <span className="num text-xs text-faint">{fmtDate(u.user.created_at)}</span>
        <span className="ml-auto" />
        <CreditsDialog userId={u.user.id} credits={u.credits} onDone={() => void q.refetch()} />
        {u.user.role !== 'admin' && <ViewAs userId={u.user.id} to="/app" />}
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="credits" value={u.credits} />
        <Stat label="audits" value={u.audits} />
        {u.plan ? (
          <>
            <Stat label="plan" value={u.plan.kind} hint={`until ${fmtDate(u.plan.ends_at)}`} tone="ok" />
            <Stat label="next run" value={new Date(u.plan.next_slot_at).getTime() <= now ? 'now' : countdown(u.plan.next_slot_at, now)} />
          </>
        ) : (
          <Stat label="plan" value="none" className="md:col-span-2" />
        )}
      </div>
      <div className="flex flex-col gap-2">
        <span className="label-caps">identities</span>
        {(u.identities ?? []).length === 0 ? (
          <StateBadge state="neutral" className="w-fit">none</StateBadge>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border bg-card">
            {(u.identities ?? []).map((i) => (
              <div key={i.id} className="grid grid-cols-[100px_1fr_auto] items-center gap-3 px-4 py-2.5 text-sm">
                <span>{providerWord[i.provider] ?? i.provider}</span>
                <span className="truncate font-mono text-xs text-muted-foreground">{i.email || i.handle || i.provider_id}</span>
                <span className="num text-xs text-faint">{fmtDate(i.created_at)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

export function AdminActions() {
  const q = $api.useQuery('get', '/v1/admin/actions')
  const now = useNow(60_000)
  const items = q.data?.items ?? []
  return (
    <Fetched q={q} items={items} empty="No actions yet">
      <div className="divide-y divide-border rounded-md border border-border bg-card">
        {items.map((a) => (
          <div key={a.id} className="grid grid-cols-[auto_1fr_auto] items-center gap-4 px-4 py-2.5 text-sm">
            <StateBadge state="neutral">{a.kind}</StateBadge>
            <span className="truncate font-mono text-xs text-muted-foreground">
              {a.subject_kind === 'audit' && a.kind !== 'audit.delete' ? <Link to={`/admin/audits/${a.subject_id}`} className="hover:underline">{a.subject_kind} {short(a.subject_id, 8, 4)}</Link> : a.subject_kind === 'user' ? <Link to={`/admin/users/${a.subject_id}`} className="hover:underline">{a.subject_kind} {short(a.subject_id, 8, 4)}</Link> : `${a.subject_kind} ${short(a.subject_id, 8, 4)}`}
            </span>
            <span className="num text-xs text-faint">{relTime(a.created_at, now)}</span>
          </div>
        ))}
      </div>
    </Fetched>
  )
}

// What people corrected on delivered findings, across every repository:
// archived as not relevant, rated up or down, each with its reason. The
// operator reads these before the next audit of the same code; an agent
// gets them on the job. Filter narrows by repository or title.
export function AdminLessons() {
  const q = $api.useQuery('get', '/v1/admin/lessons', { params: { query: { limit: 300 } } })
  const now = useNow(60_000)
  const [filter, setFilter] = useState('')
  const items = (q.data?.items ?? []).filter((l) => !filter || `${l.repo} ${l.title} ${l.reason}`.toLowerCase().includes(filter.toLowerCase()))
  return (
    <Fetched q={q} items={q.data?.items ?? []} empty="No corrections yet">
      <div className="flex flex-col gap-3">
        <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="owner/name" aria-label="Filter lessons" className="h-9 max-w-xs font-mono text-xs" />
        <div className="divide-y divide-border rounded-md border border-border bg-card">
          {items.map((l, i) => (
            <div key={`${l.finding_id}-${i}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
              <Link to={`/admin/audits/${l.audit_id}`} className="font-mono text-xs text-muted-foreground hover:text-foreground">{l.repo}</Link>
              <span className="num text-xs text-muted-foreground">{String(l.ordinal).padStart(2, '0')}</span>
              <StateBadge state={severityState[l.severity]} className="h-5 px-1.5 text-[10.5px]">{l.severity}</StateBadge>
              <span className="truncate">{l.title}</span>
              <StateBadge state="neutral" className="h-5 px-1.5 text-[10.5px]">{l.kind}</StateBadge>
              <span className="font-mono text-xs text-muted-foreground">{l.from} → {l.to}</span>
              {l.reason && <span className="text-foreground/80">{l.reason}</span>}
              <span className="ml-auto num text-xs text-faint">{l.by} · {relTime(l.at, now)}</span>
            </div>
          ))}
        </div>
      </div>
    </Fetched>
  )
}
