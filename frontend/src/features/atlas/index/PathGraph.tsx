import { useMemo, useState } from 'react'
import { StateBadge } from '@/components/primitives'
import { cn } from '@/lib/utils'
import { layout } from './graph'
import type { GNode } from './graph'

// Edges drawn left to right, nothing moving. An edge below full confidence
// (a call matched by name alone, a body matched by file name and size) is
// dashed. Hovering a node lights its edges and neighbours; a press opens
// that node's neighbourhood.

const COL_W = 236
const NODE_W = 196
const NODE_H = 38
const ROW_H = 50
const PAD = 12

export function PathGraph({ rows, cols, focus, onNode }: { rows: Record<string, unknown>[]; cols: { src: string; dst: string; rel: string }; focus?: string; onNode: (id: string) => void }) {
  const g = useMemo(() => layout(rows, cols), [rows, cols])
  const [hot, setHot] = useState<string | null>(null)
  const pos = useMemo(() => new Map(g.nodes.map((n) => [n.id, { x: PAD + n.col * COL_W, y: PAD + n.row * ROW_H }])), [g])
  const near = useMemo(() => {
    if (!hot) return null
    const s = new Set([hot])
    for (const e of g.edges) {
      if (e.src === hot) s.add(e.dst)
      if (e.dst === hot) s.add(e.src)
    }
    return s
  }, [hot, g])

  if (!g.nodes.length) return null
  const w = PAD * 2 + (g.cols - 1) * COL_W + NODE_W
  const h = PAD * 2 + (g.rows - 1) * ROW_H + NODE_H

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <StateBadge state="neutral"><span className="num">{g.nodes.length}</span> nodes</StateBadge>
        <StateBadge state="neutral"><span className="num">{g.edges.length}</span> edges</StateBadge>
        {g.dropped > 0 && <StateBadge state="warn"><span className="num">{g.dropped}</span> not drawn</StateBadge>}
      </div>
      <div className="max-h-[640px] overflow-auto rounded-md border border-border bg-card/50">
        <div className="relative" style={{ width: w, height: h }}>
          <svg width={w} height={h} className="absolute inset-0" aria-hidden="true">
            {g.edges.map((e, i) => {
              const a = pos.get(e.src)!
              const b = pos.get(e.dst)!
              const x1 = a.x + NODE_W
              const y1 = a.y + NODE_H / 2
              const x2 = b.x
              const y2 = b.y + NODE_H / 2
              const dx = Math.max(48, (x2 - x1) / 2)
              const lit = hot !== null && (e.src === hot || e.dst === hot)
              const sink = e.rel === 'sinks'
              return (
                <path
                  key={i}
                  d={`M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`}
                  fill="none"
                  strokeWidth={lit ? 1.6 : 1}
                  strokeDasharray={e.confidence !== undefined && e.confidence < 1 ? '3 3' : undefined}
                  className={cn(sink ? 'stroke-bad' : lit ? 'stroke-foreground' : 'stroke-muted-foreground', hot !== null && !lit && 'opacity-20', !lit && !sink && 'opacity-50')}
                >
                  <title>{e.confidence !== undefined ? `${e.rel} · ${e.confidence}` : e.rel}</title>
                </path>
              )
            })}
          </svg>
          {g.nodes.map((n) => (
            <Node key={n.id} n={n} at={pos.get(n.id)!} dim={near !== null && !near.has(n.id)} focused={focus === n.id} onHover={setHot} onPress={onNode} />
          ))}
        </div>
      </div>
    </div>
  )
}

function Node({ n, at, dim, focused, onHover, onPress }: { n: GNode; at: { x: number; y: number }; dim: boolean; focused: boolean; onHover: (id: string | null) => void; onPress: (id: string) => void }) {
  return (
    <button
      type="button"
      title={`${n.id}\n${n.detail}`}
      onMouseEnter={() => onHover(n.id)}
      onMouseLeave={() => onHover(null)}
      onFocus={() => onHover(n.id)}
      onBlur={() => onHover(null)}
      onClick={() => onPress(n.id)}
      style={{ left: at.x, top: at.y, width: NODE_W, height: NODE_H }}
      className={cn(
        'absolute flex flex-col justify-center gap-0.5 overflow-hidden rounded-md border bg-card px-2 text-left outline-none transition-opacity hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring',
        n.kind === 'sink' ? 'border-bad/50' : 'border-border',
        focused && 'ring-2 ring-primary',
        dim && 'opacity-30',
      )}
    >
      <span className="flex min-w-0 items-baseline gap-1.5 font-mono text-[9.5px] leading-none">
        <span className={cn('shrink-0 tracking-[0.1em] uppercase', n.kind === 'sink' ? 'text-bad' : 'text-faint')}>{n.kind}</span>
        {n.place && <span className="truncate text-muted-foreground">{n.place}</span>}
      </span>
      <span className={cn('truncate font-mono text-[12px] leading-tight', n.kind === 'sink' && 'text-bad')}>{n.label}</span>
    </button>
  )
}
