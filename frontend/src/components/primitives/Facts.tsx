import { cn } from '@/lib/utils'

// A group of facts under a heading: the heading names the whole, each row
// names one kind, and the values sit beside the kind. The report header and
// every finding use this, so a program or a tool is presented the same way
// wherever it appears.
//
// Dense lays the rows along one line instead of stacking them, for the places
// that carry a few short facts rather than the pinning block.
export function FactGroup({ label, dense, children }: { label: string; dense?: boolean; children: React.ReactNode }) {
  return (
    <div className={cn('flex gap-2', dense ? 'flex-wrap items-baseline gap-x-3' : 'flex-col')}>
      <span className={cn('label-caps text-faint', dense && 'shrink-0')}>{label}</span>
      <ul className={cn('flex gap-2', dense ? 'flex-wrap items-center gap-x-4 gap-y-1' : 'flex-col pl-4')}>{children}</ul>
    </div>
  )
}

export function FactRow({ kind, dense, children }: { kind?: string; dense?: boolean; children: React.ReactNode }) {
  return (
    <li className={cn('flex flex-wrap gap-x-3 gap-y-1', dense ? 'items-center gap-x-1.5' : 'items-baseline')}>
      <span className={cn('label-caps shrink-0 text-faint', dense ? 'text-[9.5px]' : 'w-[4.5rem]')}>{kind}</span>
      <div className={cn('flex min-w-0', dense ? 'items-center' : 'flex-col gap-1')}>{children}</div>
    </li>
  )
}
