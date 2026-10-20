import { $api } from '@/api/hooks'
import { StateBadge, Copy } from '@/components/primitives'
import { ProblemBadge, SkeletonRows } from '@/components/ui/states'
import { short } from '@/lib/utils'
import type { components } from '@/api/client'
import type { Program } from './surface/types'
import { Ports } from './surface/Ports'
import { interfaceWord } from './surface/parts'
import { useLive } from './surface/useLive'

type View = components['schemas']['SurfaceView']

const matchState = { verified: 'ok', self_built: 'ok', mismatch: 'bad', unverified: 'neutral' } as const
const matchWord = { verified: 'verified build', self_built: 'self-built', mismatch: 'mismatch', unverified: 'unverified' } as const

const poll = (q: { state: { data?: View } }) => (q.state.data?.state === 'pending' ? 10_000 : false)

export function AuditSurface({ auditId, reportHref }: { auditId: string; reportHref?: string }) {
  const q = $api.useQuery('get', '/v1/audits/{id}/surface', { params: { path: { id: auditId } } }, { refetchInterval: poll })
  return <SurfaceBody q={q} reportHref={reportHref} auditId={auditId} admin={false} />
}

export function AdminAuditSurface({ auditId, reportHref }: { auditId: string; reportHref?: string }) {
  const q = $api.useQuery('get', '/v1/admin/audits/{id}/surface', { params: { path: { id: auditId } } }, { refetchInterval: poll })
  return <SurfaceBody q={q} reportHref={reportHref} auditId={auditId} admin />
}

type Q = { isLoading: boolean; isError: boolean; error: unknown; data?: View; refetch: () => Promise<unknown> }

function SurfaceBody({ q, reportHref, auditId, admin }: { q: Q; reportHref?: string; auditId: string; admin: boolean }) {
  const live = useLive(auditId, admin)
  if (q.isLoading) return <SkeletonRows rows={3} />
  if (q.isError) return <ProblemBadge error={q.error} onRetry={() => void q.refetch()} className="w-fit" />
  const v = q.data
  if (!v) return null
  return (
    <div className="flex flex-col gap-4">
      {v.state !== 'ready' && <Identity programs={v.programs ?? []} />}
      {v.state === 'pending' && <StateBadge state="pending" className="w-fit">mapping</StateBadge>}
      {v.state === 'failed' && <StateBadge state="bad" className="w-fit" tip={v.error}>map failed</StateBadge>}
      {v.state === 'ready' && v.surface && <Ports surface={v.surface} evidence={v.evidence ?? []} programs={v.programs ?? []} reportHref={reportHref} live={live} auditId={auditId} admin={admin} />}
    </div>
  )
}

// While the map is still being built, the programs' identity is all there
// is to show. Once it is ready the same facts sit on each program's box.
function Identity({ programs }: { programs: Program[] }) {
  return (
    <div className="flex flex-col gap-2">
      {programs.map((p) => (
        <div key={p.id} className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-card px-3.5 py-2.5">
          <Copy value={p.id} className="text-[12.5px] text-foreground">{short(p.id, 4, 4)}</Copy>
          <StateBadge state={matchState[p.match]}>{matchWord[p.match]}{p.verified_commit ? ` · ${short(p.verified_commit, 7, 0)}` : ''}</StateBadge>
          <StateBadge state={p.idl_present ? 'neutral' : 'pending'}>{interfaceWord(p.idl_present, p.anchor_version, p.loader)}</StateBadge>
          {p.upgrade_authority && <StateBadge state={p.ownership_proven ? 'ok' : 'neutral'} tip={p.upgrade_authority}>authority {short(p.upgrade_authority, 4, 4)}</StateBadge>}
        </div>
      ))}
    </div>
  )
}
