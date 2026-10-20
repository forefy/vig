import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, Eye, Trash2 } from 'lucide-react'
import { Textarea } from '@/components/ui/textarea'
import { $api } from '@/api/hooks'
import { api, apiOriginAbs } from '@/api/client'
import { queryClient } from '@/app/queryClient'
import { StateBadge, auditState, stageWord, Timeline, Stat, Copy, Secret } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SummaryStrip } from '@/features/audits/Report'
import { AdminAuditSurface } from '@/features/audits/Surface'
import { AuditTabs } from '@/features/audits/AuditDetail'
import { countdown, relTime, short, cn } from '@/lib/utils'
import { chainWords } from '@/lib/chain'
import { useNow } from '@/lib/useNow'
import { useBusy } from '@/lib/useBusy'
import { problemDetail } from '@/lib/problem'
import { toast } from 'sonner'
import type { components } from '@/api/client'

type AdminAudit = components['schemas']['AdminAudit']

const MAX_HOURS = 48
const VIEW_MINUTES = 15
const terminal = (s: string) => s === 'delivered' || s === 'failed' || s === 'cancelled'
const releasable = (s: string) => s !== 'queued' && !terminal(s)

// The admin's side of one audit: the timeline the customer sees, the
// operator controls, and the door to the customer's own view.
export function AdminAuditSurfaceView() {
  return <AdminAuditDetail view="surface" />
}

export function AdminAuditDetail({ view = 'timeline' }: { view?: 'timeline' | 'surface' }) {
  const { id = '' } = useParams()
  const q = $api.useQuery(
    'get',
    '/v1/admin/audits/{id}',
    { params: { path: { id } } },
    {
      refetchInterval: (query) => (query.state.data && !terminal(query.state.data.state) ? 15_000 : false),
    },
  )
  const now = useNow(1000)
  if (q.isLoading) return <SkeletonRows rows={6} />
  if (q.isError || !q.data) return <ProblemBadge error={q.error ?? { status: 404, title: 'Not found' }} onRetry={() => void q.refetch()} className="w-fit" />
  const a = q.data
  const live = !terminal(a.state)
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/admin/audits" className="font-mono text-xs text-muted-foreground hover:text-foreground">
          audits /
        </Link>
        <span className="font-mono text-lg">{a.repo}</span>
        <span className="font-mono text-xs text-muted-foreground">
          {a.base_sha ? `${short(a.base_sha, 7, 0)}..` : ''}
          {short(a.head_sha, 7, 0)}
        </span>
        <StateBadge state={auditState[a.state] ?? 'neutral'}>{stageWord[a.state] ?? a.state}</StateBadge>
        {a.hidden_at && <StateBadge state="bad" tip={a.hidden_reason}>hidden</StateBadge>}
        <Copy value={a.id} className="text-[11px]">
          {short(a.id, 8, 4)}
        </Copy>
        <Button size="sm" variant="ghost" className="ml-auto" render={<Link to={`/admin/users/${a.user_id}`} />}>
          User
        </Button>
        <ViewAs userId={a.user_id} auditId={a.id} to={`/app/audits/${a.id}`} />
      </div>
      <AuditTabs id={a.id} adapter={a.adapter} admin className="mb-2" />
      {view === 'surface' ? (
        <AdminAuditSurface auditId={a.id} />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            {live ? (
              <Stat
                label={a.state === 'delayed' ? 'new eta' : 'eta'}
                value={a.eta_at ? countdown(a.eta_at, now) : '-'}
                tone={a.eta_at && new Date(a.eta_at).getTime() < now ? 'bad' : a.state === 'delayed' ? 'warn' : undefined}
              />
            ) : (
              <Stat label={a.state} value={relTime(a.updated_at, now)} tone={a.state === 'delivered' ? 'ok' : 'bad'} />
            )}
            <Stat label="scope" value={a.scope.lines.toLocaleString('en-US')} hint={`code lines · ${(a.scope.files ?? []).length} files`} />
            <Stat label="cost" value={a.cost_kind === 'credit' ? '1 credit' : 'daily slot'} />
            <Stat label="queued" value={relTime(a.created_at, now)} />
          </div>
          {a.summary && <SummaryStrip summary={a.summary} />}
          <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_320px]">
            <Timeline state={a.state} resume={a.resume_state} events={a.events ?? []} adapter={a.adapter} className="max-w-[420px] pt-2" />
            <div className="flex flex-col gap-4">
              <OperatorCard audit={a} onChange={() => void q.refetch()} />
              <AdminCard audit={a} onChange={() => void q.refetch()} />
              {a.state === 'delivered' && a.report_id && <ReviseCard audit={a} onChange={() => void q.refetch()} />}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

// Starts a bounded, read-only view as the customer. The whole app switches
// identity, so the query cache is dropped before landing on their page.
function ViewAs({ userId, auditId, to }: { userId: string; auditId?: string; to: string }) {
  const nav = useNavigate()
  const { busy, run } = useBusy()
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={busy}
      onClick={() =>
        run(async () => {
          const r = await api.POST('/v1/admin/impersonate', {
            body: { user_id: userId, audit_id: auditId, minutes: VIEW_MINUTES },
          })
          if (r.error) {
            toast.error(problemDetail(r.error, 'Failed'))
            return
          }
          queryClient.clear()
          nav(to)
        })
      }
    >
      <Eye className="size-4" /> View as customer
    </Button>
  )
}

export { ViewAs }

// Mints the job-scoped operator token for one audit and shows it once. On
// a queued audit the token claims it; on a job in progress it replaces the
// token that claimed it, so a lost or expired token never re-queues work.
export function MintToken({ auditId, state, hours = 24, disabled, className }: { auditId: string; state: string; hours?: number; disabled?: boolean; className?: string }) {
  const [minted, setMinted] = useState<{
    token: string
    expires_at: string
  } | null>(null)
  const { busy, run } = useBusy()
  if (terminal(state)) return null
  const renew = state !== 'queued'
  return (
    <>
      <Button
        size="sm"
        variant={renew ? 'outline' : 'default'}
        className={className}
        disabled={disabled || busy}
        onClick={() =>
          run(async () => {
            const r = await api.POST('/v1/admin/operator-tokens', {
              body: { audit_id: auditId, hours },
            })
            if (!r.data) toast.error(problemDetail(r.error, 'Failed'))
            else setMinted({ token: r.data.token, expires_at: r.data.expires_at })
          })
        }
      >
        {renew ? 'Renew token' : 'Mint token'}
      </Button>
      <Dialog open={!!minted} onOpenChange={(o) => !o && setMinted(null)}>
        <DialogContent className="sm:max-w-xl">
          <DialogTitle className="label-caps">Shown once</DialogTitle>
          {minted && (
            <>
              <Secret value={bootstrap(minted.token, renew)} lang="sh" />
              <StateBadge state="pending" className="w-fit">
                expires {relTime(minted.expires_at)}
              </StateBadge>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

// One action that needs a reason: the button opens the reason, the reason
// fires the call. What the call does is in the button's word; what it
// touches is in the facts above the reason.
function ReasonAction({ label, tone = 'outline', onRun, disabled, icon, facts }: { label: string; tone?: 'outline' | 'destructive'; onRun: (reason: string) => Promise<void>; disabled?: boolean; icon?: React.ReactNode; facts?: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const { busy, run } = useBusy()
  return (
    <>
      {icon ? (
        <Button size="icon-xs" variant="ghost" aria-label={label} disabled={disabled} className={cn('text-muted-foreground', tone === 'destructive' ? 'hover:text-bad' : 'hover:text-foreground')} onClick={() => setOpen(true)}>{icon}</Button>
      ) : (
        <Button size="sm" variant={tone} disabled={disabled} onClick={() => setOpen(true)}>{label}</Button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">{label}</DialogTitle>
          {facts}
          <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="reason" aria-label="Reason" className="h-9" autoFocus />
          <Button
            size="sm"
            variant={tone}
            className="w-fit"
            disabled={reason.trim().length === 0 || busy}
            onClick={() =>
              run(async () => {
                await onRun(reason.trim())
                setReason('')
                setOpen(false)
              })
            }
          >
            {label}
          </Button>
        </DialogContent>
      </Dialog>
    </>
  )
}

const settable = ['queued', 'claimed', 'cloning', 'programs_verified', 'analysis', 'human_review', 'delivered', 'failed', 'cancelled'] as const
type Settable = (typeof settable)[number]
type AuditFacts = Pick<AdminAudit, 'id' | 'state' | 'report_id' | 'repo' | 'cost_kind' | 'adapter'>

const chip = (kind: 'off' | 'current' | 'on', tone?: 'bad') =>
  cn(
    'rounded-sm border px-2 py-0.5 font-mono text-[11px] disabled:cursor-not-allowed disabled:opacity-40',
    kind === 'on' ? (tone === 'bad' ? 'border-bad text-bad' : 'border-primary text-foreground') : kind === 'current' ? 'border-dashed border-foreground/40 text-foreground' : 'border-border text-muted-foreground hover:text-foreground',
  )

// Puts the audit in a state. Pipeline states are reachable from anywhere
// (delivered only with a report); failed and cancelled keep their rules,
// so the chips an audit cannot reach are dimmed. The owner sees the move
// on the timeline.
export function StateAction({ audit, onDone, icon }: { audit: AuditFacts; onDone: () => void; icon?: boolean }) {
  const [open, setOpen] = useState(false)
  const [to, setTo] = useState<Settable | null>(null)
  const [reason, setReason] = useState('')
  const { busy, run } = useBusy()
  const words = chainWords(audit.adapter)
  const refunded = audit.state === 'failed' || audit.state === 'cancelled'
  const reachable = (s: Settable) => s !== audit.state && !(s === 'delivered' && !audit.report_id) && !(s === 'failed' && terminal(audit.state)) && !(s === 'cancelled' && refunded)
  const word = (s: string) => (s === 'programs_verified' ? words.stageVerified : stageWord[s] ?? s)
  const paysAgain = refunded && to !== null && to !== 'failed' && to !== 'cancelled'
  const close = (o: boolean) => {
    setOpen(o)
    if (!o) {
      setTo(null)
      setReason('')
    }
  }
  return (
    <>
      {icon ? (
        <Button size="icon-xs" variant="ghost" aria-label="State" className="text-muted-foreground hover:text-foreground" onClick={() => setOpen(true)}><ArrowRightLeft /></Button>
      ) : (
        <Button size="sm" variant="outline" onClick={() => setOpen(true)}>State</Button>
      )}
      <Dialog open={open} onOpenChange={close}>
        <DialogContent className="sm:max-w-sm">
          <DialogTitle className="label-caps">state</DialogTitle>
          <div className="flex flex-wrap gap-1.5">
            {settable.map((s) => (
              <button key={s} type="button" disabled={!reachable(s)} onClick={() => setTo(s)} className={chip(s === audit.state ? 'current' : to === s ? 'on' : 'off', s === 'failed' || s === 'cancelled' ? 'bad' : undefined)}>
                {word(s)}
              </button>
            ))}
          </div>
          <div className="flex flex-wrap gap-1.5 empty:hidden">
            {to === 'delivered' && <StateBadge state="ok">report</StateBadge>}
            {(to === 'failed' || to === 'cancelled') && <StateBadge state="ok">{audit.cost_kind === 'credit' ? 'credit restored' : 'slot restored'}</StateBadge>}
            {paysAgain && <StateBadge state="warn">{audit.cost_kind === 'credit' ? 'credit consumed' : 'slot consumed'}</StateBadge>}
            {audit.state === 'delivered' && to !== null && <StateBadge state="warn">report private</StateBadge>}
          </div>
          <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="reason" aria-label="Reason" className="h-9" />
          <Button
            size="sm"
            variant={to === 'failed' || to === 'cancelled' ? 'destructive' : 'default'}
            className="w-fit"
            disabled={!to || reason.trim().length === 0 || busy}
            onClick={() =>
              run(async () => {
                if (!to) return
                const r = await api.POST('/v1/admin/audits/{id}/state', { params: { path: { id: audit.id } }, body: { state: to, reason: reason.trim() } })
                if (r.error) toast.error(problemDetail(r.error, 'Failed'))
                else {
                  toast.success(`${word(to).toLowerCase()}${to === 'failed' || to === 'cancelled' ? ' · cost restored' : paysAgain ? ' · paid again' : ''}`)
                  close(false)
                  onDone()
                }
              })
            }
          >
            {to ? word(to) : 'State'}
          </Button>
        </DialogContent>
      </Dialog>
    </>
  )
}

// Deletes the row and everything under it. The facts in the dialog are
// what goes and what comes back; the action log keeps the receipt.
export function DeleteAction({ audit, onDone, icon }: { audit: AuditFacts; onDone: () => void; icon?: boolean }) {
  const refunded = audit.state === 'failed' || audit.state === 'cancelled'
  return (
    <ReasonAction
      label="Delete"
      tone="destructive"
      icon={icon ? <Trash2 /> : undefined}
      facts={
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="font-mono text-xs">{audit.repo}</span>
          <StateBadge state={auditState[audit.state] ?? 'neutral'}>{stageWord[audit.state] ?? audit.state}</StateBadge>
          {audit.report_id && <StateBadge state="bad">report</StateBadge>}
          {!refunded && <StateBadge state="ok">{audit.cost_kind === 'credit' ? 'credit restored' : 'slot restored'}</StateBadge>}
        </div>
      }
      onRun={async (reason) => {
        const r = await api.DELETE('/v1/admin/audits/{id}', { params: { path: { id: audit.id } }, body: { reason } })
        if (r.error || !r.data) toast.error(problemDetail(r.error, 'Failed'))
        else {
          toast.success(`deleted · ${r.data.events} events${r.data.reports ? ` · ${r.data.reports} report${r.data.reports === 1 ? '' : 's'}` : ''}${r.data.refunded ? ' · cost restored' : ''}`)
          onDone()
        }
      }}
    />
  )
}

// The admin's own decisions on one audit, each a timeline event the owner
// sees: put it in a state, take it off the owner's list (cancelling first
// if the cost is still held) or put it back, or delete the row outright.
function AdminCard({ audit, onChange }: { audit: AdminAudit; onChange: () => void }) {
  const qc = useQueryClient()
  const nav = useNavigate()
  const refunded = audit.state === 'failed' || audit.state === 'cancelled'
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ['get', '/v1/admin/queue'] })
    void qc.invalidateQueries({ queryKey: ['get', '/v1/admin/audits'] })
    onChange()
  }
  const post = async (path: '/v1/admin/audits/{id}/cancel' | '/v1/admin/audits/{id}/hide', body: Record<string, unknown>, done: string) => {
    const r = await api.POST(path, { params: { path: { id: audit.id } }, body: body as never })
    if (r.error) toast.error(problemDetail(r.error, 'Failed'))
    else {
      toast.success(done)
      changed()
    }
  }
  return (
    <div className="flex flex-col gap-3 self-start rounded-md border border-border bg-card p-4">
      <span className="label-caps">admin</span>
      <div className="flex flex-wrap gap-2">
        <StateAction audit={audit} onDone={changed} />
        {!audit.hidden_at && (
          <ReasonAction
            label="Hide"
            facts={!refunded ? <div className="flex gap-1.5"><StateBadge state="bad">cancelled</StateBadge><StateBadge state="ok">{audit.cost_kind === 'credit' ? 'credit restored' : 'slot restored'}</StateBadge></div> : undefined}
            onRun={(reason) => (refunded ? post('/v1/admin/audits/{id}/hide', { hidden: true, reason }, 'hidden') : post('/v1/admin/audits/{id}/cancel', { reason, hide: true }, 'cancelled · cost restored · hidden'))}
          />
        )}
        {audit.hidden_at && <ReasonAction label="Unhide" onRun={(reason) => post('/v1/admin/audits/{id}/hide', { hidden: false, reason }, 'visible again')} />}
        <DeleteAction
          audit={audit}
          onDone={() => {
            void qc.invalidateQueries({ queryKey: ['get', '/v1/admin/queue'] })
            void qc.invalidateQueries({ queryKey: ['get', '/v1/admin/audits'] })
            nav('/admin/audits')
          }}
        />
      </div>
    </div>
  )
}

// A delivered report is revised by posting the findings document back:
// load the current one, edit it, post. The owner sees rev N on the report
// and the change on the timeline; statuses follow matching findings.
function ReviseCard({ audit, onChange }: { audit: AdminAudit; onChange: () => void }) {
  const [text, setText] = useState('')
  const { busy, run } = useBusy()
  const href = `${apiOriginAbs()}/v1/admin/audits/${audit.id}/report.json`
  let parsed: unknown = null
  let parseError = ''
  if (text.trim()) {
    try {
      parsed = JSON.parse(text)
    } catch (e) {
      parseError = e instanceof Error ? e.message : 'invalid JSON'
    }
  }
  const findings = parsed && typeof parsed === 'object' && Array.isArray((parsed as { findings?: unknown }).findings) ? (parsed as { findings: unknown[] }).findings.length : null
  return (
    <div className="flex flex-col gap-3 self-start rounded-md border border-border bg-card p-4">
      <div className="flex items-center gap-2">
        <span className="label-caps">revise report</span>
        <Button
          size="xs"
          variant="ghost"
          className="ml-auto"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const r = await api.GET('/v1/admin/audits/{id}/report.json', { params: { path: { id: audit.id } } })
              if (r.error || !r.data) toast.error(problemDetail(r.error, 'Failed'))
              else {
                setText(JSON.stringify(r.data, null, 2))
              }
            })
          }
        >
          Load current
        </Button>
        <a href={href} target="_blank" rel="noreferrer" className="font-mono text-[11px] text-muted-foreground hover:text-foreground">report.json</a>
      </div>
      <Textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} aria-label="Findings document" className={cn('min-h-40 font-mono text-[11px]', parseError && 'border-bad')} />
      <div className="flex items-center gap-2">
        {findings !== null && <StateBadge state="neutral">{findings} findings</StateBadge>}
        {parseError && <StateBadge state="bad" tip={parseError}>invalid JSON</StateBadge>}
        <Button
          size="sm"
          className="ml-auto"
          disabled={!parsed || !!parseError || busy}
          onClick={() =>
            run(async () => {
              const r = await api.POST('/v1/admin/audits/{id}/report', { params: { path: { id: audit.id } }, body: parsed as never })
              if (r.error || !r.data) toast.error(problemDetail(r.error, 'Failed'))
              else {
                toast.success(`rev ${r.data.report.revision} · +${r.data.added} −${r.data.removed} · ${r.data.kept} kept`)
                setText(JSON.stringify(await api.GET('/v1/admin/audits/{id}/report.json', { params: { path: { id: audit.id } } }).then((x) => x.data ?? parsed), null, 2))
                onChange()
              }
            })
          }
        >
          Revise
        </Button>
      </div>
    </div>
  )
}

// Everything the sandbox needs, in one paste: where the API is, the token,
// where to learn the rest, and the first command. A renewed token is
// already bound, so it goes straight to the token file.
function bootstrap(token: string, renew: boolean): string {
  const origin = apiOriginAbs()
  return [
    `export VIG_API=${origin}`,
    renew ? `printf '%s' ${token} > "\${VIG_OP_HOME:-/work}/token"` : `export VIG_OP_TOKEN=${token}`,
    `curl -s $VIG_API/llms.txt`,
    renew ? 'vig-op status' : 'vig-op claim',
  ].join('\n')
}

// Operator controls for one audit: mint its token, release a stuck claim,
// or push the ETA. Every action lands on the customer's timeline.
function OperatorCard({ audit, onChange }: { audit: AdminAudit; onChange: () => void }) {
  const qc = useQueryClient()
  const { busy, run } = useBusy()
  const [hours, setHours] = useState('24')
  const [delayHours, setDelayHours] = useState('12')
  const [reason, setReason] = useState('')
  const h = Number(hours)
  const dh = Number(delayHours)
  const hoursOk = Number.isInteger(h) && h >= 1 && h <= MAX_HOURS
  const delayOk = Number.isInteger(dh) && dh >= 1 && reason.trim().length > 0
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ['get', '/v1/admin/queue'] })
    onChange()
  }
  if (terminal(audit.state)) return null
  return (
    <div className="flex flex-col gap-3 self-start rounded-md border border-border bg-card p-4">
      <span className="label-caps">operator</span>
      <div className="flex items-center gap-2">
        <Input
          value={hours}
          onChange={(e) => setHours(e.target.value)}
          type="number"
          min={1}
          max={MAX_HOURS}
          step={1}
          aria-label="Token hours"
          className={cn('h-8 w-20 font-mono text-xs', !hoursOk && 'border-bad')}
        />
        <span className="font-mono text-xs text-muted-foreground">h</span>
        <MintToken auditId={audit.id} state={audit.state} hours={h} disabled={!hoursOk} className="ml-auto" />
      </div>
      {releasable(audit.state) && (
        <Button
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() =>
            run(async () => {
              const r = await api.POST('/v1/admin/audits/{id}/reassign', {
                params: { path: { id: audit.id } },
                body: { reason: 'released from admin' },
              })
              if (r.error) toast.error(problemDetail(r.error, 'Failed'))
              else changed()
            })
          }
        >
          Release to queue
        </Button>
      )}
      <div className="flex flex-col gap-2 border-t border-border pt-3">
        <div className="flex items-center gap-2">
          <Input value={delayHours} onChange={(e) => setDelayHours(e.target.value)} type="number" min={1} step={1} aria-label="Delay hours" className="h-8 w-20 font-mono text-xs" />
          <span className="font-mono text-xs text-muted-foreground">h</span>
          <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="reason" aria-label="Delay reason" className="h-8 text-xs" />
        </div>
        <Button
          size="sm"
          variant="outline"
          className="w-fit"
          disabled={!delayOk || busy}
          onClick={() =>
            run(async () => {
              const eta = new Date(Date.now() + dh * 3_600_000).toISOString()
              const r = await api.POST('/v1/admin/audits/{id}/delay', {
                params: { path: { id: audit.id } },
                body: { eta_at: eta, reason: reason.trim() },
              })
              if (r.error) toast.error(problemDetail(r.error, 'Failed'))
              else {
                setReason('')
                changed()
              }
            })
          }
        >
          Delay
        </Button>
      </div>
    </div>
  )
}
