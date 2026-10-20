import { StateBadge } from '@/components/primitives'
import { cn, relTime, short } from '@/lib/utils'
import { Callee } from './Callee'
import { calleeTrust } from './trust'
import { SIGNERS, isSide } from './model'
import type { Depth, Model, Outcome, Outside } from './model'
import type { Ports, Pt } from './usePorts'
import { strokeOf, wire } from './usePorts'
import { isWhatIf } from './types'
import type { Replay } from './types'

export type Level = 'system' | 'program' | 'instruction'
// One wire, picked: enough to filter the log and say what the wire is.
export type EdgePick = { id: string; label: string; count: number; signed: boolean; sigs: string[]; ops: string[]; sites: string[]; tip?: string }
export type Lens = { level: Level; prog: string; sel: string; tx: string | null; acct: string | null; edge: EdgePick | null; tab: 'tx' | 'acct'; open: Set<string> }
export type Act = {
  level: (l: Level) => void
  program: (pid: string) => void
  select: (ixId: string) => void
  pickTx: (sig: string | null) => void
  pickAcct: (name: string | null) => void
  pickEdge: (e: EdgePick | null) => void
  tab: (t: 'tx' | 'acct') => void
  toggle: (groupKey: string) => void
}

// How well one instruction is known: declared, observed, replayed, reviewed.
export function DepthMark({ depth, className }: { depth: Depth; className?: string }) {
  const names = ['declared', 'observed', 'replayed', 'reviewed']
  return (
    <span className={cn('inline-grid shrink-0 grid-cols-4 gap-[1.5px]', className)} title={names.filter((_, i) => depth[i]).join(' · ') || 'unknown'}>
      {depth.map((on, i) => (
        <span key={i} className={cn('h-[7px] w-[5px] rounded-[1px] border', on ? 'border-primary bg-primary' : 'border-input')} />
      ))}
    </span>
  )
}

// The point a wire attaches to. Zero layout weight beyond its own dot.
export function Port({ id, tone = 'muted', hollow, className }: { id: string; tone?: 'muted' | 'warn' | 'fg'; hollow?: boolean; className?: string }) {
  return (
    <span
      data-port={id}
      className={cn(
        'block size-[7px] shrink-0 rounded-full border',
        tone === 'warn' ? 'border-warn' : tone === 'fg' ? 'border-foreground' : 'border-muted-foreground',
        hollow ? 'bg-background' : tone === 'warn' ? 'bg-warn' : tone === 'fg' ? 'bg-foreground' : 'bg-muted-foreground',
        className,
      )}
    />
  )
}

// An invisible anchor for wires that attach to an edge of a card.
export function Anchor({ id, className }: { id: string; className?: string }) {
  return <span data-port={id} aria-hidden className={cn('pointer-events-none absolute size-0', className)} />
}

export type Wire = {
  id: string
  from: string
  to: string
  kind: 'across' | 'loop' | 'down'
  count: number
  signed?: boolean
  dashed?: boolean
  state: 'idle' | 'on' | 'lit' | 'dim' | 'picked'
  step?: number
  title?: string
  fromPt?: Pt
}

// Wires over DOM. The svg ignores the pointer; only a fat invisible stroke
// along each wire takes clicks, so rows under it stay clickable.
export function Wires({ size, ports, wires, onPick }: { size: { w: number; h: number }; ports: Ports; wires: Wire[]; onPick?: (id: string) => void }) {
  if (size.w === 0) return null
  const order = { dim: 0, idle: 1, on: 2, lit: 3, picked: 4 } as const
  const drawn = wires
    .map((w) => ({ w, a: w.fromPt ?? ports[w.from], b: ports[w.to] }))
    .filter((x): x is { w: Wire; a: Pt; b: Pt } => Boolean(x.a && x.b))
    .sort((x, y) => order[x.w.state] - order[y.w.state])
  return (
    <svg className="pointer-events-none absolute inset-0 z-10 overflow-visible" width={size.w} height={size.h} aria-hidden>
      {drawn.map(({ w, a, b }) => {
        const g = wire(a, b, w.kind)
        const strong = w.state === 'lit' || w.state === 'picked'
        return (
          <g key={w.id}>
            <path
              d={g.d}
              fill="none"
              strokeWidth={strong ? Math.max(2.2, strokeOf(w.count)) : strokeOf(w.count)}
              strokeDasharray={w.dashed ? '4 4' : undefined}
              className={cn(
                w.state === 'picked' ? 'stroke-foreground' : (w.state === 'lit' || w.state === 'on') && w.signed ? 'stroke-warn' : w.state === 'lit' || w.state === 'on' ? 'stroke-primary' : 'stroke-muted-foreground',
                w.state === 'dim' ? 'opacity-10' : w.state === 'idle' ? 'opacity-25' : w.state === 'on' ? 'opacity-90' : 'opacity-100',
              )}
            />
            {onPick && (
              <path d={g.d} fill="none" stroke="transparent" strokeWidth={12} className="pointer-events-[stroke] cursor-pointer" style={{ pointerEvents: 'stroke' }} onClick={() => onPick(w.id)}>
                {w.title && <title>{w.title}</title>}
              </path>
            )}
            {w.step !== undefined && (
              <>
                <circle cx={g.mid.x} cy={g.mid.y} r={8} className="fill-primary" />
                <text x={g.mid.x} y={g.mid.y + 3.5} textAnchor="middle" className="fill-primary-foreground font-mono text-[10px] font-semibold">{w.step}</text>
              </>
            )}
          </g>
        )
      })}
    </svg>
  )
}

// A program outside the audit, on either side of the map.
export function OutsideNode({ o, port, side, count, dim, on, onClick, word = 'program' }: { o: Outside; port: string; side: 'left' | 'right'; count?: number; dim?: boolean; on?: boolean; onClick?: () => void; word?: string }) {
  const trust = calleeTrust(o.node?.external ? o.node.facts : undefined)
  const seen = (count ?? 0) > 0
  const dot = <Port id={port} tone={seen ? 'fg' : 'muted'} hollow={!seen} />
  const Tag = onClick ? 'button' : 'div'
  return (
    <div className={cn('flex items-center', side === 'left' ? 'flex-row' : 'flex-row-reverse', dim && 'opacity-35')}>
      <Tag
        type={onClick ? 'button' : undefined}
        onClick={onClick}
        aria-pressed={onClick ? on : undefined}
        className={cn(
          'flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border px-2.5 py-1.5 text-left font-mono text-[12px]',
          o.fog ? 'border-dashed border-input bg-[repeating-linear-gradient(45deg,transparent_0_4px,var(--border)_4px_5px)] text-muted-foreground' : seen ? 'border-border bg-card' : 'border-dashed border-border text-muted-foreground',
          onClick && 'hover:border-muted-foreground',
          on && 'border-primary',
        )}
      >
        <span className="flex min-w-0 items-center gap-1.5">
          {trust && <span className={cn('size-1.5 shrink-0 rounded-full', trust === 'ok' ? 'bg-ok' : 'bg-warn')} />}
          <span className="truncate">{o.fog ? 'dynamic' : <Callee node={o.node} id={isSide(o.id) ? o.id.slice(3) : o.id} dot={false} />}</span>
          {o.both && <span className="text-faint" title="calls in and is called">⇄</span>}
        </span>
        <span className="num shrink-0 text-[11px] text-faint">{o.fog ? `any ${word}` : seen ? `×${count}` : 'source'}</span>
      </Tag>
      <span className={side === 'left' ? '-mr-[3.5px] ml-0' : '-ml-[3.5px]'}>{dot}</span>
    </div>
  )
}

export function SignersNode({ count, dim, on, onClick, word = 'signers' }: { count: number; dim?: boolean; on?: boolean; onClick?: () => void; word?: string }) {
  return (
    <div className={cn('relative flex items-center', dim && 'opacity-35')}>
      <button type="button" onClick={onClick} aria-pressed={on} className={cn('flex min-w-0 flex-1 items-center justify-between gap-2 rounded-md border bg-card px-2.5 py-1.5 text-left font-mono text-[12px] hover:border-muted-foreground', on ? 'border-primary' : 'border-border')}>
        <span>{word}</span>
        <span className="num text-[11px] text-faint">×{count}</span>
      </button>
      <span className="-mr-[3.5px]"><Port id={`o:${SIGNERS}`} tone="fg" /></span>
      <Anchor id={`s:${SIGNERS}`} className="bottom-0 left-1/2" />
    </div>
  )
}

export function OutcomeMark({ o, none, unit = 'cu' }: { o?: Outcome; none: string; unit?: string }) {
  if (!o) return <StateBadge state="pending" className="h-5 px-1.5 text-[10.5px]">{none}</StateBadge>
  const pct = Math.min(100, Math.round(((o.units ?? 0) / 400_000) * 100))
  return (
    <span className="inline-flex items-center gap-2">
      <StateBadge state={o.ok ? 'ok' : 'bad'} className="h-5 px-1.5 text-[10.5px]">{o.word}</StateBadge>
      {o.units !== undefined && (
        <>
          <span className="h-1 w-20 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-primary/70" style={{ width: `${pct}%` }} /></span>
          <span className="num font-mono text-[11px] text-muted-foreground">{o.units.toLocaleString('en-US')} {unit}</span>
        </>
      )}
    </span>
  )
}

// The baseline replay of one signature on one instruction: the newest that
// is not a what-if.
export function baselineReplay(replays: Replay[], sig: string): Replay | undefined {
  const mine = replays.filter((r) => r.replay === sig)
  return mine.find((r) => !isWhatIf(r)) ?? mine[0]
}

export const programName = (model: Model, pid: string): string => {
  const n = model.byId.get(`${model.words.boxPrefix}${pid}`)
  return n?.facts?.crate || n?.name || (model.words.web ? pid : short(pid, 4, 4))
}
// An instruction named with its program when the map has several; a route
// reads as host and path.
export const ixLabel = (model: Model, ixId: string): string => {
  const ix = model.ixs.get(ixId)
  if (!ix) return ixId
  if (model.words.web) return model.programs.length > 1 ? `${programName(model, ix.pid)}${ix.node.name}` : ix.node.name
  return `${model.programs.length > 1 ? `${programName(model, ix.pid)}.` : ''}${ix.node.name}`
}
// Any end of a wire or a hop, named: the signer entry, a folded group or
// program, an instruction, or a program outside the audit.
export function endLabel(model: Model, id: string): string {
  if (id === SIGNERS) return model.words.signers
  if (id === 'more:in') return 'other callers'
  if (id === 'more:out') return `other ${model.words.programs}`
  if (id.startsWith('g:')) {
    const [, pid, key] = id.split(':')
    return `${programName(model, pid)} · ${key}`
  }
  if (id.startsWith('h:')) return programName(model, id.slice(2))
  if (model.ixs.has(id)) return ixLabel(model, id)
  const nodeId = isSide(id) ? id.slice(3) : id
  const pid = nodeId.replace(/^p:/, '')
  if (model.programs.some((p) => p.pid === pid)) return programName(model, pid)
  return model.byId.get(nodeId)?.name ?? short(nodeId.replace(/^p:(static:)?/, ''), 4, 4)
}

// What stands for the program's interface, in the chain's own word: an
// IDL on Solana, a verified ABI on an EVM chain.
export function interfaceWord(idlPresent: boolean, version: string | undefined, loader: string | undefined): string {
  if (loader?.startsWith('evm')) return idlPresent ? 'ABI' : 'no ABI'
  return idlPresent ? `IDL${version ? ` ${version}` : ''}` : 'no IDL'
}

// What an empty sample says: how far back it could see, when it knows.
export function sampleEmpty(sampling?: { horizon?: string; blocks?: number } | null, web = false): string {
  if (web) return '0 in capture'
  if (!sampling?.horizon) return '0 in sample'
  return `none in ${relTime(sampling.horizon).replace(' ago', '')}`
}
