import { useState } from 'react'
import { Link } from 'react-router'
import { Bell, CheckCheck, ChevronRight } from 'lucide-react'
import { $api } from '@/api/hooks'
import { api } from '@/api/client'
import { queryClient } from './queryClient'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { StateBadge, stageWord, type BadgeState } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { relTime, short, cn } from '@/lib/utils'

import { groupByKind, unreadCount, type Notification, type NotificationGroup } from '@/lib/notifications'

export const NOTIFICATIONS_POLL_MS = 30_000

// What each kind means, as a tone. The kind itself is the badge text.
const tone: Record<string, BadgeState> = {
  audit_delivered: 'ok',
  audit_failed: 'bad',
  audit_delayed: 'warn',
  audit_overdue: 'warn',
  audit_state: 'pending',
  claim_approved: 'ok',
  claim_rejected: 'bad',
  claim_revoked: 'bad',
  credits_granted: 'ok',
  credits_revoked: 'bad',
  purchase_confirmed: 'ok',
  purchase_expired: 'warn',
  surface_drift: 'bad',
  audit_cancelled: 'bad',
  report_revised: 'warn',
}

// The bell in the rail. Notifications of one kind fold into one row with
// their count, newest kind first; a row opens to each of them. One press
// marks everything read.
export function NotificationsBell({ className }: { className?: string }) {
  const [open, setOpen] = useState(false)
  const [marking, setMarking] = useState(false)
  const q = $api.useQuery('get', '/v1/me/notifications', {}, { refetchInterval: NOTIFICATIONS_POLL_MS })
  const items = q.data?.items ?? []
  const unread = unreadCount(items)
  const groups = groupByKind(items)

  async function markAllRead() {
    setMarking(true)
    try {
      await api.POST('/v1/me/notifications/read')
      await queryClient.invalidateQueries({ queryKey: ['get', '/v1/me/notifications'] })
    } finally {
      setMarking(false)
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label={unread > 0 ? `Notifications, ${unread} unread` : 'Notifications'}
        className={cn('relative flex size-9 items-center justify-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring', className)}
      >
        <Bell className="size-4" />
        {unread > 0 && (
          <span data-testid="unread" className="absolute top-0.5 right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 font-mono text-[10px] leading-none text-primary-foreground">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent side="right" align="end" className="w-[340px] p-0">
        <div className="flex items-center gap-2 px-3 py-2">
          <span className="label-caps">notifications</span>
          <span className="num text-xs text-faint">{items.length}</span>
          {unread > 0 && (
            <button
              type="button"
              onClick={() => void markAllRead()}
              disabled={marking}
              className="ml-auto inline-flex h-6 items-center gap-1 rounded-sm border border-border px-2 font-mono text-[11px] text-muted-foreground hover:border-primary hover:text-foreground disabled:opacity-50"
            >
              <CheckCheck className="size-3.5" />
              all read
            </button>
          )}
        </div>
        <div className="max-h-[360px] overflow-y-auto border-t border-border">
          {q.isLoading ? (
            <SkeletonRows rows={3} className="p-2" />
          ) : q.isError ? (
            <ProblemBadge error={q.error} onRetry={() => void q.refetch()} className="m-2" />
          ) : items.length === 0 ? (
            <div className="px-3 py-6 text-center">
              <StateBadge state="neutral">none</StateBadge>
            </div>
          ) : (
            <ul className="divide-y divide-border">
              {groups.map((g) => (g.items.length === 1 ? <Row key={g.kind} n={g.items[0]!} onNavigate={() => setOpen(false)} /> : <Group key={g.kind} g={g} onNavigate={() => setOpen(false)} />))}
            </ul>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

// A kind with several notifications: its badge, how many, how many unread,
// the newest time, and the audits it names; open, every one of them.
function Group({ g, onNavigate }: { g: NotificationGroup; onNavigate: () => void }) {
  const [open, setOpen] = useState(false)
  const audits = [...new Set(g.items.map((n) => (typeof n.payload?.audit_id === 'string' ? n.payload.audit_id : null)).filter((x): x is string => x !== null))]
  return (
    <li className={cn('flex flex-col', g.unread > 0 && 'bg-primary/[0.04]')}>
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="flex flex-col gap-1.5 px-3 py-2.5 text-left hover:bg-accent/40">
        <span className="flex items-center justify-between gap-2">
          <span className="flex items-center gap-1.5">
            <ChevronRight className={cn('size-3.5 text-faint transition-transform', open && 'rotate-90')} />
            <StateBadge state={tone[g.kind] ?? 'neutral'}>{g.kind.replace(/_/g, ' ')}</StateBadge>
            <span className="num rounded-sm border border-border px-1.5 py-0.5 font-mono text-[11px] text-foreground">×{g.items.length}</span>
            {g.unread > 0 && g.unread < g.items.length && <span className="num font-mono text-[11px] text-primary">{g.unread} new</span>}
          </span>
          <span className="num text-[11px] text-faint">{relTime(g.newest)}</span>
        </span>
        {audits.length > 0 && (
          <span className="flex flex-wrap items-center gap-1.5 pl-5 font-mono text-[11px] text-muted-foreground">
            {audits.slice(0, 3).map((a) => (
              <span key={a} className="rounded-sm border border-border px-1.5 py-0.5">
                {short(a, 8, 4)}
              </span>
            ))}
            {audits.length > 3 && <span className="text-faint">+{audits.length - 3}</span>}
          </span>
        )}
      </button>
      {open && (
        <ul className="divide-y divide-border border-t border-border pl-3">
          {g.items.map((n) => (
            <Row key={n.id} n={n} onNavigate={onNavigate} />
          ))}
        </ul>
      )}
    </li>
  )
}

function Row({ n, onNavigate }: { n: Notification; onNavigate: () => void }) {
  const p = n.payload ?? {}
  const auditId = typeof p.audit_id === 'string' ? p.audit_id : null
  const state = typeof p.state === 'string' ? p.state : null
  const sku = typeof p.sku === 'string' ? p.sku : null
  const delta = typeof p.delta === 'number' ? p.delta : null
  const eta = typeof p.eta_at === 'string' ? p.eta_at : null
  return (
    <li className={cn('flex flex-col gap-1.5 px-3 py-2.5', !n.read_at && 'bg-primary/[0.04]')}>
      <div className="flex items-center justify-between gap-2">
        <StateBadge state={tone[n.kind] ?? 'neutral'}>{n.kind.replace(/_/g, ' ')}</StateBadge>
        <span className="num text-[11px] text-faint">{relTime(n.created_at)}</span>
      </div>
      {(auditId || state || sku || delta != null || eta) && (
        <div className="flex flex-wrap items-center gap-1.5 font-mono text-[11px] text-muted-foreground">
          {auditId && (
            <Link to={`/app/audits/${auditId}`} onClick={onNavigate} className="rounded-sm border border-border px-1.5 py-0.5 hover:text-foreground">
              {short(auditId, 8, 4)}
            </Link>
          )}
          {state && <span className="rounded-sm border border-border px-1.5 py-0.5">{stageWord[state] ?? state}</span>}
          {sku && <span className="rounded-sm border border-border px-1.5 py-0.5">{sku}</span>}
          {delta != null && <span className={cn('rounded-sm border border-border px-1.5 py-0.5', delta > 0 ? 'text-ok' : 'text-bad')}>{delta > 0 ? `+${delta}` : delta}</span>}
          {eta && <span className="rounded-sm border border-border px-1.5 py-0.5">{relTime(eta)}</span>}
        </div>
      )}
    </li>
  )
}
