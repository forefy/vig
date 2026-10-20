import { baseUnits, cn, short } from '@/lib/utils'
import type { TokenMove, Volume } from './types'

// Signed token deltas: out in bad, in in ok, the mint shortened and whole
// on hover. A mint with no decimals shows its raw integer, tagged.
export function Moves({ moves, max = 4, className }: { moves?: TokenMove[]; max?: number; className?: string }) {
  if (!moves || moves.length === 0) return null
  return (
    <span className={cn('inline-flex flex-wrap items-center gap-1', className)}>
      {moves.slice(0, max).map((m, i) => {
        const amt = baseUnits(m.delta, m.decimals)
        const neg = amt.startsWith('-')
        return (
          <span
            key={`${m.mint}:${m.owner ?? ''}:${i}`}
            className={cn('inline-flex items-center gap-1 rounded-sm border px-1.5 py-px font-mono text-[10.5px] whitespace-nowrap', neg ? 'border-bad/40 text-bad' : 'border-ok/40 text-ok')}
            title={`${m.delta} ${m.mint}${m.owner ? ` · ${m.owner}` : ''}`}
          >
            <span className="num">{neg ? amt : `+${amt}`}</span>
            <span className="text-muted-foreground">{short(m.mint, 4, 4)}</span>
            {m.decimals === 0 && <span className="text-faint">raw</span>}
          </span>
        )
      })}
      {moves.length > max && <span className="font-mono text-[10.5px] text-faint">+{moves.length - max}</span>}
    </span>
  )
}

// Volume per mint over every sampled transaction of one instruction.
export function VolumeStrip({ moves, className }: { moves?: Volume[]; className?: string }) {
  if (!moves || moves.length === 0) return null
  return (
    <span className={cn('inline-flex flex-wrap items-center gap-1', className)}>
      <span className="label-caps">volume</span>
      {moves.map((m) => (
        <span key={m.mint} className="inline-flex items-center gap-1 rounded-sm border border-border px-1.5 py-px font-mono text-[10.5px] whitespace-nowrap text-muted-foreground" title={`${m.volume} ${m.mint}`}>
          <span className="num text-foreground">{baseUnits(m.volume, m.decimals)}</span>
          {short(m.mint, 4, 4)}
          {m.decimals === 0 && <span className="text-faint">raw</span>}
          <span className="num text-faint">×{m.count}</span>
        </span>
      ))}
    </span>
  )
}
