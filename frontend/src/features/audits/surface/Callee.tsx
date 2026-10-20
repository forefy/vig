import { cn, short } from '@/lib/utils'
import { calleeTrust, calleeTip } from './trust'
import type { Node } from './types'

// A program named anywhere outside the plate: trust dot, name, idl tag.
// dot is off inside a StateBadge, which draws its own.
export function Callee({ node, id, dot = true, className }: { node?: Node; id: string; dot?: boolean; className?: string }) {
  const f = node?.external ? node.facts : undefined
  const trust = calleeTrust(f)
  return (
    <span className={cn('inline-flex items-center gap-1', className)} title={calleeTip(f)}>
      {dot && trust && <span className={cn('size-1.5 shrink-0 rounded-full', trust === 'ok' ? 'bg-ok' : 'bg-warn')} />}
      {node?.name ?? short(id.startsWith('p:') ? id.slice(2) : id, 4, 4)}
      {f?.idl && <span className="text-[9.5px] uppercase tracking-wide text-faint">idl</span>}
    </span>
  )
}
