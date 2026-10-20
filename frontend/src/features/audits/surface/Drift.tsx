import { StateBadge } from '@/components/primitives'
import { cn, dur, relTime } from '@/lib/utils'
import { outcomeWord, time, type Replay, type ReplayAccount } from './types'

// Two replays of one signature side by side: outcome pair, units delta,
// the accounts whose change sets differ, and the time between them.
export function Drift({ now, base, label }: { now: Replay; base: Replay; label: string }) {
  const d = (now.units ?? 0) - (base.units ?? 0)
  const gap = time(now.at) - time(base.at)
  const diffs = accountDiffs(base.accounts ?? [], now.accounts ?? [])
  const when = (r: Replay) => r.tag || relTime(r.at) || '·'
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-dashed border-border px-2.5 py-1.5">
      <span className="label-caps">{label}</span>
      <StateBadge state={base.outcome === 'ok' ? 'ok' : 'bad'} className="h-5 px-1.5 text-[10.5px]" tip={base.error?.msg ?? base.error?.raw}>{outcomeWord(base)}</StateBadge>
      <span className="text-faint">→</span>
      <StateBadge state={now.outcome === 'ok' ? 'ok' : 'bad'} className="h-5 px-1.5 text-[10.5px]" tip={now.error?.msg ?? now.error?.raw}>{outcomeWord(now)}</StateBadge>
      <span className={cn('num font-mono text-[11px]', d === 0 ? 'text-faint' : 'text-muted-foreground')}>{d > 0 ? '+' : ''}{d.toLocaleString('en-US')} cu</span>
      {diffs.map((a) => (
        <span key={a.key} className="inline-flex items-center gap-1 rounded-sm border border-warn/50 px-1.5 py-px font-mono text-[10.5px] text-warn" title={`${a.base.join(', ') || '·'} → ${a.now.join(', ') || '·'}`}>
          {a.name}
          {a.plus.map((k) => <span key={`+${k}`} className="text-muted-foreground">+{k}</span>)}
          {a.minus.map((k) => <span key={`-${k}`} className="text-muted-foreground">−{k}</span>)}
        </span>
      ))}
      <span className="flex-grow" />
      <span className="num font-mono text-[11px] text-faint" title={`${when(base)} → ${when(now)}`}>
        vs {when(base)}{gap ? ` · ${gap > 0 ? '+' : '−'}${dur(gap)}` : ''}
      </span>
    </div>
  )
}

type AccountDiff = { key: string; name: string; base: string[]; now: string[]; plus: string[]; minus: string[] }

function accountDiffs(base: ReplayAccount[], now: ReplayAccount[]): AccountDiff[] {
  const id = (a: ReplayAccount) => a.name ?? a.pubkey
  const b = new Map(base.map((a) => [id(a), a]))
  const n = new Map(now.map((a) => [id(a), a]))
  const keys = [...new Set([...b.keys(), ...n.keys()])]
  const out: AccountDiff[] = []
  for (const k of keys) {
    const bs = [...(b.get(k)?.changed ?? [])].sort()
    const ns = [...(n.get(k)?.changed ?? [])].sort()
    if (bs.join('|') === ns.join('|')) continue
    const bset = new Set(bs), nset = new Set(ns)
    out.push({ key: k, name: k.length > 20 ? `${k.slice(0, 4)}…${k.slice(-4)}` : k, base: bs, now: ns, plus: ns.filter((x) => !bset.has(x)), minus: bs.filter((x) => !nset.has(x)) })
  }
  return out
}
